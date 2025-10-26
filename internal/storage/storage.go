package storage

import "time"

// Message is the canonical stored message representation.
// Keep fields minimal and backend-agnostic.
type Message struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
	Headers   map[string]string
	Ts        time.Time
}

// LogStorage is the abstraction the broker depends on.
type LogStorage interface {
	// Append appends msg to topic:partition and returns the assigned offset (monotonic per partition).
	Append(topic string, partition int, msg *Message) (int64, error)

	// Read returns up to max messages starting at offset (inclusive).
	// If max == 0, return all available messages starting from offset.
	Read(topic string, partition int, offset int64, max int) ([]*Message, error)

	// CommitOffset persistently records that 'group' has processed up to offset for topic:partition.
	CommitOffset(group, topic string, partition int, offset int64) error

	// GetCommittedOffset returns the last committed offset for group/topic/partition.
	// If there's no committed offset yet, return -1 and nil error.
	GetCommittedOffset(group, topic string, partition int) (int64, error)

	// Close allows the store to release resources.
	Close() error
}
