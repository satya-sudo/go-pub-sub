package broker

import (
	"fmt"
	"sync"
	"time"
)

// TopicInfo represents metadata for a topic.
type TopicInfo struct {
	Name        string    `json:"name"`
	Partitions  int       `json:"partitions"`
	RetentionMs int64     `json:"retention_ms"`
	CreatedAt   time.Time `json:"created_at"`
}

// TopicManager manages topic metadata and delegates partition initialization to PartitionManager.
type TopicManager struct {
	mu sync.RWMutex
	// name -> metadata
	topics map[string]*TopicInfo

	// optional validations / defaults
	minPartitions      int
	maxPartitions      int
	defaultRetentionMs int64

	// dependency: partition manager
	pm *PartitionManager
}

// NewTopicManager constructs a TopicManager. It requires a PartitionManager instance.
func NewTopicManager(pm *PartitionManager) *TopicManager {
	return &TopicManager{
		topics:             make(map[string]*TopicInfo),
		minPartitions:      1,
		maxPartitions:      1024,                 // prototype limit
		defaultRetentionMs: 7 * 24 * 3600 * 1000, // 7 days in ms
		pm:                 pm,
	}
}

// CreateTopic creates topic metadata and calls PartitionManager.CreateTopic to initialize partitions.
// If topic already exists, it returns an error.
func (tm *TopicManager) CreateTopic(name string, partitions int, retentionMs int64) error {
	if name == "" {
		return fmt.Errorf("topic name required")
	}
	if partitions <= 0 {
		return fmt.Errorf("partitions must be > 0")
	}
	if partitions < tm.minPartitions || partitions > tm.maxPartitions {
		return fmt.Errorf("partitions must be between %d and %d", tm.minPartitions, tm.maxPartitions)
	}
	if retentionMs <= 0 {
		retentionMs = tm.defaultRetentionMs
	}

	tm.mu.Lock()
	_, exists := tm.topics[name]
	if exists {
		tm.mu.Unlock()
		return fmt.Errorf("topic already exists: %s", name)
	}
	ti := &TopicInfo{
		Name:        name,
		Partitions:  partitions,
		RetentionMs: retentionMs,
		CreatedAt:   time.Now().UTC(),
	}
	tm.topics[name] = ti
	tm.mu.Unlock()

	// Initialize partitions via partition manager.
	// PartitionManager.CreateTopic is idempotent in our design (it returns error if exists).
	if err := tm.pm.CreateTopic(name, partitions); err != nil {
		// if pm.CreateTopic failed, remove the metadata to keep consistent
		tm.mu.Lock()
		delete(tm.topics, name)
		tm.mu.Unlock()
		return fmt.Errorf("partition manager create topic failed: %w", err)
	}

	return nil
}

// GetTopicInfo returns metadata and whether it exists.
func (tm *TopicManager) GetTopicInfo(name string) (*TopicInfo, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	ti, ok := tm.topics[name]
	if !ok {
		return nil, false
	}
	// return a shallow copy to avoid external mutation
	c := *ti
	return &c, true
}

// ListTopics returns a snapshot of all topics.
func (tm *TopicManager) ListTopics() []TopicInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]TopicInfo, 0, len(tm.topics))
	for _, t := range tm.topics {
		out = append(out, *t)
	}
	return out
}

// TopicExists checks presence
func (tm *TopicManager) TopicExists(name string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	_, ok := tm.topics[name]
	return ok
}

// ValidatePartition checks that topic exists and partition index is in range.
func (tm *TopicManager) ValidatePartition(name string, partition int) error {
	if name == "" {
		return fmt.Errorf("topic required")
	}
	tm.mu.RLock()
	ti, ok := tm.topics[name]
	tm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown topic: %s", name)
	}
	if partition < 0 || partition >= ti.Partitions {
		return fmt.Errorf("invalid partition %d for topic %s (partitions=%d)", partition, name, ti.Partitions)
	}
	return nil
}
