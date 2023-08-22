package prunable

import (
	"os"

	"github.com/zyedidia/generic/cache"

	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/hive.go/runtime/syncutils"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/storage/database"
	iotago "github.com/iotaledger/iota.go/v4"
)

type SlotManager struct {
	openDBs      *cache.Cache[iotago.EpochIndex, *database.DBInstance]
	openDBsMutex syncutils.RWMutex

	lastPrunedEpoch *model.EvictionIndex[iotago.EpochIndex]
	lastPrunedMutex syncutils.RWMutex

	dbConfig     database.Config
	errorHandler func(error)

	dbSizes *shrinkingmap.ShrinkingMap[iotago.EpochIndex, int64]

	optsMaxOpenDBs int
}

func NewSlotManager(dbConfig database.Config, errorHandler func(error), opts ...options.Option[SlotManager]) *SlotManager {
	return options.Apply(&SlotManager{
		optsMaxOpenDBs:  10,
		dbConfig:        dbConfig,
		errorHandler:    errorHandler,
		dbSizes:         shrinkingmap.New[iotago.EpochIndex, int64](),
		lastPrunedEpoch: model.NewEvictionIndex[iotago.EpochIndex](),
	}, opts, func(m *SlotManager) {
		m.openDBs = cache.New[iotago.EpochIndex, *database.DBInstance](m.optsMaxOpenDBs)
		m.openDBs.SetEvictCallback(func(baseIndex iotago.EpochIndex, db *database.DBInstance) {
			db.Close()

			size, err := dbPrunableDirectorySize(dbConfig.Directory, baseIndex)
			if err != nil {
				errorHandler(ierrors.Wrapf(err, "failed to get size of prunable directory for base index %d", baseIndex))
			}

			m.dbSizes.Set(baseIndex, size)
		})
	})
}

// IsTooOld checks if the index is in a pruned epoch.
func (m *SlotManager) IsTooOld(index iotago.EpochIndex) (isTooOld bool) {
	m.lastPrunedMutex.RLock()
	defer m.lastPrunedMutex.RUnlock()

	return index < m.lastPrunedEpoch.NextIndex()
}

func (m *SlotManager) Get(index iotago.EpochIndex, realm kvstore.Realm) (kvstore.KVStore, error) {
	if m.IsTooOld(index) {
		return nil, ierrors.Wrapf(database.ErrEpochPruned, "epoch %d", index)
	}

	kv := m.getDBInstance(index).KVStore()

	return lo.PanicOnErr(kv.WithExtendedRealm(realm)), nil
}

func (m *SlotManager) Shutdown() {
	m.openDBsMutex.Lock()
	defer m.openDBsMutex.Unlock()

	m.openDBs.Each(func(index iotago.EpochIndex, db *database.DBInstance) {
		db.Close()
	})
}

// PrunableSlotStorageSize returns the size of the prunable storage containing all db instances.
func (m *SlotManager) PrunableSlotStorageSize() int64 {
	// Sum up all the evicted databases
	var sum int64
	m.dbSizes.ForEach(func(index iotago.EpochIndex, i int64) bool {
		sum += i
		return true
	})

	m.openDBsMutex.Lock()
	defer m.openDBsMutex.Unlock()

	// Add up all the open databases
	m.openDBs.Each(func(key iotago.EpochIndex, val *database.DBInstance) {
		size, err := dbPrunableDirectorySize(m.dbConfig.Directory, key)
		if err != nil {
			m.errorHandler(ierrors.Wrapf(err, "dbPrunableDirectorySize failed for %s: %s", m.dbConfig.Directory, key))
			return
		}
		sum += size
	})

	return sum
}

func (m *SlotManager) LastPrunedEpoch() (index iotago.EpochIndex, hasPruned bool) {
	m.lastPrunedMutex.RLock()
	defer m.lastPrunedMutex.RUnlock()

	return m.lastPrunedEpoch.Index()
}

func (m *SlotManager) RestoreFromDisk() (lastPrunedEpoch iotago.EpochIndex) {
	m.lastPrunedMutex.Lock()
	defer m.lastPrunedMutex.Unlock()

	dbInfos := getSortedDBInstancesFromDisk(m.dbConfig.Directory)

	// There are no dbInstances on disk -> nothing to restore.
	if len(dbInfos) == 0 {
		return
	}

	// Set the maxPruned epoch to the baseIndex-1 of the oldest dbInstance.
	// Leave the lastPrunedEpoch at the default value if the oldest dbInstance is at baseIndex 0, which is not pruned yet.
	if dbInfos[0].baseIndex > 0 {
		lastPrunedEpoch = dbInfos[0].baseIndex - 1
		m.lastPrunedEpoch.MarkEvicted(lastPrunedEpoch)
	}

	// Open all the dbInstances (perform health checks) and add them to the openDBs cache. Also fills the dbSizes map (when evicted from the cache).
	for _, dbInfo := range dbInfos {
		m.getDBInstance(dbInfo.baseIndex)
	}

	return
}

// getDBInstance returns the DB instance for the given epochIndex or creates a new one if it does not yet exist.
// DBs are created as follows where each db is located in m.basedir/<starting epochIndex>/
//
//	epochIndex 0 -> db 0
//	epochIndex 1 -> db 1
//	epochIndex 2 -> db 2
func (m *SlotManager) getDBInstance(index iotago.EpochIndex) (db *database.DBInstance) {
	m.openDBsMutex.Lock()
	defer m.openDBsMutex.Unlock()

	// check if exists again, as other goroutine might have created it in parallel
	db, exists := m.openDBs.Get(index)
	if !exists {
		db = database.NewDBInstance(m.dbConfig.WithDirectory(dbPathFromIndex(m.dbConfig.Directory, index)))

		// Remove the cached db size since we will open the db
		m.dbSizes.Delete(index)
		m.openDBs.Put(index, db)
	}

	return db
}

func (m *SlotManager) Prune(epoch iotago.EpochIndex) error {
	m.lastPrunedMutex.Lock()
	defer m.lastPrunedMutex.Unlock()

	if epoch < lo.Return1(m.lastPrunedEpoch.Index()) {
		return ierrors.Wrapf(database.ErrNoPruningNeeded, "epoch %d is already pruned", epoch)
	}

	m.openDBsMutex.Lock()
	defer m.openDBsMutex.Unlock()

	db, exists := m.openDBs.Get(epoch)
	if exists {
		db.Close()

		m.openDBs.Remove(epoch)
	}

	if err := os.RemoveAll(dbPathFromIndex(m.dbConfig.Directory, epoch)); err != nil {
		panic(err)
	}

	// Delete the db size since we pruned the whole directory
	m.dbSizes.Delete(epoch)
	m.lastPrunedEpoch.MarkEvicted(epoch)

	return nil
}

func (m *SlotManager) BucketSize(epoch iotago.EpochIndex) (int64, error) {
	m.openDBsMutex.RLock()
	defer m.openDBsMutex.RUnlock()

	size, exists := m.dbSizes.Get(epoch)
	if exists {
		return size, nil
	}

	_, exists = m.openDBs.Get(epoch)
	if !exists {
		return 0, nil
		// TODO: this should be fixed by https://github.com/iotaledger/iota.go/pull/480
		//  return 0, ierrors.Errorf("bucket does not exists: %d", epoch)
	}

	size, err := dbPrunableDirectorySize(m.dbConfig.Directory, epoch)
	if err != nil {
		return 0, ierrors.Wrapf(err, "dbPrunableDirectorySize failed for %s: %s", m.dbConfig.Directory, epoch)
	}

	return size, nil
}

func (m *SlotManager) Flush() error {
	m.openDBsMutex.RLock()
	defer m.openDBsMutex.RUnlock()

	var err error
	m.openDBs.Each(func(epoch iotago.EpochIndex, db *database.DBInstance) {
		if err = db.KVStore().Flush(); err != nil {
			return
		}
	})

	return err
}
