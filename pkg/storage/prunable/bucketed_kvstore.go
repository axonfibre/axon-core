package prunable

import "github.com/iotaledger/hive.go/kvstore"

type bucketedKVStore struct {
	bucketManager *BucketManager
	store         kvstore.KVStore
}

func newBucketedKVStore(bucketManager *BucketManager, store kvstore.KVStore) *bucketedKVStore {
	return &bucketedKVStore{
		bucketManager: bucketManager,
		store:         store,
	}
}

func (b *bucketedKVStore) WithRealm(realm kvstore.Realm) (kvstore.KVStore, error) {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	s, err := b.store.WithRealm(realm)
	if err != nil {
		return nil, err
	}

	return newBucketedKVStore(b.bucketManager, s), nil
}

func (b *bucketedKVStore) WithExtendedRealm(realm kvstore.Realm) (kvstore.KVStore, error) {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	s, err := b.store.WithExtendedRealm(realm)
	if err != nil {
		return nil, err
	}

	return newBucketedKVStore(b.bucketManager, s), nil
}

func (b *bucketedKVStore) Realm() kvstore.Realm {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Realm()
}

func (b *bucketedKVStore) Iterate(prefix kvstore.KeyPrefix, kvConsumerFunc kvstore.IteratorKeyValueConsumerFunc, direction ...kvstore.IterDirection) error {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Iterate(prefix, kvConsumerFunc, direction...)
}

func (b *bucketedKVStore) IterateKeys(prefix kvstore.KeyPrefix, consumerFunc kvstore.IteratorKeyConsumerFunc, direction ...kvstore.IterDirection) error {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.IterateKeys(prefix, consumerFunc, direction...)
}

func (b *bucketedKVStore) Clear() error {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Clear()
}

func (b *bucketedKVStore) Get(key kvstore.Key) (value kvstore.Value, err error) {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Get(key)
}

func (b *bucketedKVStore) Set(key kvstore.Key, value kvstore.Value) error {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Set(key, value)
}

func (b *bucketedKVStore) Has(key kvstore.Key) (bool, error) {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Has(key)
}

func (b *bucketedKVStore) Delete(key kvstore.Key) error {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Delete(key)
}

func (b *bucketedKVStore) DeletePrefix(prefix kvstore.KeyPrefix) error {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.DeletePrefix(prefix)
}

func (b *bucketedKVStore) Flush() error {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Flush()
}

func (b *bucketedKVStore) Close() error {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Close()
}

func (b *bucketedKVStore) Batched() (kvstore.BatchedMutations, error) {
	b.rLockBucketManager()
	defer b.rUnlockBucketManager()

	return b.store.Batched()
}

func (b *bucketedKVStore) rLockBucketManager() {
	b.bucketManager.mutex.RLock()
}

func (b *bucketedKVStore) rUnlockBucketManager() {
	b.bucketManager.mutex.RUnlock()
}
