package database

import (
	hivedb "github.com/iotaledger/hive.go/db"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/hive.go/kvstore/rocksdb"
)

var (
	AllowedEnginesDefault = []hivedb.Engine{
		hivedb.EngineAuto,
		hivedb.EngineMapDB,
		hivedb.EngineRocksDB,
	}

	AllowedEnginesStorage = []hivedb.Engine{
		hivedb.EngineRocksDB,
	}

	AllowedEnginesStorageAuto = append(AllowedEnginesStorage, hivedb.EngineAuto)
)

// CheckEngine is a wrapper around hivedb.CheckEngine to throw a custom error message in case of engine mismatch.
func CheckEngine(dbPath string, createDatabaseIfNotExists bool, dbEngine hivedb.Engine, allowedEngines ...hivedb.Engine) (hivedb.Engine, error) {
	tmpAllowedEngines := AllowedEnginesDefault
	if len(allowedEngines) > 0 {
		tmpAllowedEngines = allowedEngines
	}

	targetEngine, err := hivedb.CheckEngine(dbPath, createDatabaseIfNotExists, dbEngine, tmpAllowedEngines)
	if err != nil {
		if ierrors.Is(err, hivedb.ErrEngineMismatch) {
			return hivedb.EngineUnknown, ierrors.Errorf(`database (%s) engine does not match the configuration: '%v' != '%v'

			If you want to use another database engine, you can use the tool './iota-core tool db-migration' to convert the current database.`, dbPath, targetEngine, dbEngine)
		}

		return hivedb.EngineUnknown, err
	}

	return targetEngine, nil
}

// StoreWithDefaultSettings returns a kvstore with default settings.
// It also checks if the database engine is correct.
func StoreWithDefaultSettings(path string, createDatabaseIfNotExists bool, dbEngine hivedb.Engine, allowedEngines ...hivedb.Engine) (kvstore.KVStore, error) {
	tmpAllowedEngines := AllowedEnginesDefault
	if len(allowedEngines) > 0 {
		tmpAllowedEngines = allowedEngines
	}

	targetEngine, err := CheckEngine(path, createDatabaseIfNotExists, dbEngine, tmpAllowedEngines...)
	if err != nil {
		return nil, err
	}

	switch targetEngine {
	case hivedb.EngineRocksDB:
		db, err := NewRocksDB(path)
		if err != nil {
			return nil, err
		}

		return rocksdb.New(db), nil

	case hivedb.EngineMapDB:
		return mapdb.NewMapDB(), nil

	default:
		return nil, ierrors.Errorf("unknown database engine: %s, supported engines: pebble/rocksdb/mapdb", dbEngine)
	}
}
