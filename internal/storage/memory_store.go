package storage

import (
	"fmt"
	"sync"
	"time"
)

// InMemoryStore is a simple append-only store kept in memory.
type InMemoryStore struct {
	mu      sync.RWMutex
	logs    map[string][]*Message // key: topic:partition -> slice of messages
	commits map[string]int64      // key: group:topic:partition -> committed offset
	closed  bool
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		logs:    make(map[string][]*Message),
		commits: make(map[string]int64),
	}
}

func tkey(topic string, partition int) string {
	return fmt.Sprintf("%s:%d", topic, partition)
}

func ckey(group, topic string, partition int) string {
	return fmt.Sprintf("%s:%s:%d", group, topic, partition)
}

// Append appends a message to the given topic/partition and returns the new offset.
func (s *InMemoryStore) Append(topic string, partition int, msg *Message) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return -1, fmt.Errorf("store closed")
	}
	k := tkey(topic, partition)
	arr := s.logs[k]
	msg.Partition = int32(partition)
	msg.Offset = int64(len(arr))
	if msg.Ts.IsZero() {
		msg.Ts = time.Now().UTC()
	}
	// store a shallow copy pointer (ok for prototype)
	s.logs[k] = append(arr, msg)
	return msg.Offset, nil
}

// Read returns messages starting from offset (inclusive). If max==0 returns all.
func (s *InMemoryStore) Read(topic string, partition int, offset int64, max int) ([]*Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("store closed")
	}
	k := tkey(topic, partition)
	arr := s.logs[k]
	if offset < 0 {
		offset = 0
	}
	if int(offset) >= len(arr) {
		return []*Message{}, nil
	}
	end := len(arr)
	if max > 0 && int(offset)+max < end {
		end = int(offset) + max
	}
	res := make([]*Message, end-int(offset))
	copy(res, arr[offset:end])
	return res, nil
}

// CommitOffset persistently records group offset
func (s *InMemoryStore) CommitOffset(group, topic string, partition int, offset int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("store closed")
	}
	k := ckey(group, topic, partition)
	s.commits[k] = offset
	return nil
}

// GetCommittedOffset returns last committed offset or -1 if none.
func (s *InMemoryStore) GetCommittedOffset(group, topic string, partition int) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return -1, fmt.Errorf("store closed")
	}
	k := ckey(group, topic, partition)
	v, ok := s.commits[k]
	if !ok {
		return -1, nil
	}
	return v, nil
}

func (s *InMemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// drop references to allow GC
	s.logs = nil
	s.commits = nil
	return nil
}
