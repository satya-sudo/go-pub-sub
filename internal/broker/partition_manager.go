package broker

import (
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"

	"github.com/satya-sudo/go-pub-sub/internal/storage"
)

// PartitionManager handles partition metadata, partition selection, and
// serializing appends per partition.
type PartitionManager struct {
	store storage.LogStorage

	// topics metadata
	mu sync.RWMutex
	// topic -> partition count
	partitions map[string]int

	// per-topic round-robin counter (for messages without key)
	rrMu sync.RWMutex
	rr   map[string]*int64

	// per-partition locks to serialize appends:
	// key = topic:partition -> *sync.Mutex
	lockMu sync.RWMutex
	locks  map[string]*sync.Mutex
}

// NewPartitionManager creates a PartitionManager that uses the provided LogStorage.
func NewPartitionManager(st storage.LogStorage) *PartitionManager {
	return &PartitionManager{
		store:      st,
		partitions: make(map[string]int),
		rr:         make(map[string]*int64),
		locks:      make(map[string]*sync.Mutex),
	}
}

// CreateTopic registers a topic with the given number of partitions.
// If topic already exists, it returns an error.
func (pm *PartitionManager) CreateTopic(topic string, partitions int) error {
	if partitions <= 0 {
		return fmt.Errorf("partitions must be > 0")
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if _, ok := pm.partitions[topic]; ok {
		return fmt.Errorf("topic already exists: %s", topic)
	}
	pm.partitions[topic] = partitions

	// init round-robin counter
	pm.rrMu.Lock()
	var zero int64 = 0
	pm.rr[topic] = &zero
	pm.rrMu.Unlock()

	// init locks for each partition
	pm.lockMu.Lock()
	for p := 0; p < partitions; p++ {
		key := pm.tpKey(topic, int32(p))
		pm.locks[key] = &sync.Mutex{}
	}
	pm.lockMu.Unlock()

	return nil
}

// GetPartitionCount returns the partition count for a topic or 0 if unknown.
func (pm *PartitionManager) GetPartitionCount(topic string) int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.partitions[topic]
}

// Append chooses a partition (respecting partitionHint if >=0), serializes
// the append for that partition, calls storage.Append and returns the assigned offset.
func (pm *PartitionManager) Append(topic string, partitionHint int, key []byte, msg *storage.Message) (int64, int32, error) {
	partitions := pm.GetPartitionCount(topic)
	if partitions == 0 {
		// topic unknown, for prototype create topic with 1 partition implicitly
		// or return error — choose to create implicitly
		_ = pm.CreateTopic(topic, 1)
		partitions = 1
	}

	var part int
	if partitionHint >= 0 {
		if partitionHint >= partitions {
			return -1, -1, fmt.Errorf("invalid partition hint %d for topic %s with %d partitions", partitionHint, topic, partitions)
		}
		part = partitionHint
	} else if len(key) > 0 {
		part = int(pm.hashToPartition(key, partitions))
	} else {
		part = pm.roundRobin(topic, partitions)
	}

	// get per-partition lock and serialize append
	lock := pm.getLock(topic, int32(part))
	lock.Lock()
	defer lock.Unlock()

	// fill message partition (storage.Append expects partition as int)
	msg.Partition = int32(part)
	off, err := pm.store.Append(topic, part, msg)
	if err != nil {
		return -1, -1, err
	}
	return off, int32(part), nil
}

// Read forwards to underlying storage.Read
func (pm *PartitionManager) Read(topic string, partition int, offset int64, max int) ([]*storage.Message, error) {
	return pm.store.Read(topic, partition, offset, max)
}

// CommitOffset forwards to storage commit
func (pm *PartitionManager) CommitOffset(group, topic string, partition int, offset int64) error {
	return pm.store.CommitOffset(group, topic, partition, offset)
}

// GetCommittedOffset forwards to storage
func (pm *PartitionManager) GetCommittedOffset(group, topic string, partition int) (int64, error) {
	return pm.store.GetCommittedOffset(group, topic, partition)
}

// tpKey returns the internal key for locks map
func (pm *PartitionManager) tpKey(topic string, partition int32) string {
	return fmt.Sprintf("%s:%d", topic, partition)
}

func (pm *PartitionManager) getLock(topic string, partition int32) *sync.Mutex {
	key := pm.tpKey(topic, partition)
	pm.lockMu.RLock()
	l, ok := pm.locks[key]
	pm.lockMu.RUnlock()
	if ok {
		return l
	}

	// create lazily if missing
	pm.lockMu.Lock()
	defer pm.lockMu.Unlock()
	// double-check
	l, ok = pm.locks[key]
	if ok {
		return l
	}
	l = &sync.Mutex{}
	pm.locks[key] = l
	return l
}

// hashToPartition uses FNV-1a to map a key to partition index
func (pm *PartitionManager) hashToPartition(key []byte, partitions int) int32 {
	h := fnv.New32a()
	h.Write(key)
	return int32(int(h.Sum32()) % partitions)
}

// roundRobin returns a partition using per-topic atomic counter
func (pm *PartitionManager) roundRobin(topic string, partitions int) int {
	pm.rrMu.RLock()
	ptr, ok := pm.rr[topic]
	pm.rrMu.RUnlock()
	if !ok {
		// init lazily
		pm.rrMu.Lock()
		var zero int64 = 0
		ptr, _ = pm.rr[topic]
		if ptr == nil {
			pm.rr[topic] = &zero
			ptr = &zero
		}
		pm.rrMu.Unlock()
	}
	// atomically increment and return modulo partitions
	n := atomic.AddInt64(ptr, 1)
	// map n to [0, partitions-1]
	return int(n % int64(partitions))
}
