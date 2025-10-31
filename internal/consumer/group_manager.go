package consumer

import (
	"fmt"
	"sort"
	"sync"

	"github.com/satya-sudo/go-pub-sub/internal/storage"
)

// GroupManager handles simple consumer-group membership and partition assignment.
// This is a single-node, in-memory implementation for prototypes.
type GroupManager struct {
	mu sync.RWMutex
	// groups[groupName][topic] => set of memberIDs (as map[string]struct{})
	groups map[string]map[string]map[string]struct{} // group -> topic -> memberID -> exists

	// assignments[group][topic] => map[memberID] -> []partition
	assignments map[string]map[string]map[string][]int

	// dependencies for offsets
	pm interface { /* optional access to partition manager if needed */
	}
	store storage.LogStorage
}

// NewGroupManager creates an instance. Provide storage to allow commits/lookups.
func NewGroupManager(st storage.LogStorage) *GroupManager {
	return &GroupManager{
		groups:      make(map[string]map[string]map[string]struct{}),
		assignments: make(map[string]map[string]map[string][]int),
		store:       st,
	}
}

// Join registers memberID to the group for a topic and returns assigned partitions.
// memberID must be unique per subscription (broker should generate).
func (gm *GroupManager) Join(group string, memberID string, topic string, partitions int) ([]int, error) {
	if group == "" || memberID == "" || topic == "" {
		return nil, fmt.Errorf("group, memberID and topic required")
	}
	if partitions <= 0 {
		return nil, fmt.Errorf("partitions must be > 0")
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()

	// ensure map structure exists
	if _, ok := gm.groups[group]; !ok {
		gm.groups[group] = make(map[string]map[string]struct{})
	}
	if _, ok := gm.groups[group][topic]; !ok {
		gm.groups[group][topic] = make(map[string]struct{})
	}

	// add member
	gm.groups[group][topic][memberID] = struct{}{}

	// recompute assignments for this (group, topic)
	gm.rebalanceLocked(group, topic, partitions)

	// return assignment for this member
	assign := gm.assignments[group][topic][memberID]
	// copy to avoid caller mutating internal slice
	out := make([]int, len(assign))
	copy(out, assign)
	return out, nil
}

// Leave removes member from group/topic and triggers rebalance.
func (gm *GroupManager) Leave(group string, memberID string, topic string, partitions int) {
	if group == "" || memberID == "" || topic == "" {
		return
	}
	gm.mu.Lock()
	defer gm.mu.Unlock()
	if _, ok := gm.groups[group]; !ok {
		return
	}
	if _, ok := gm.groups[group][topic]; !ok {
		return
	}
	delete(gm.groups[group][topic], memberID)
	// cleanup empty topic map
	if len(gm.groups[group][topic]) == 0 {
		delete(gm.groups[group], topic)
	}
	// cleanup group if empty
	if len(gm.groups[group]) == 0 {
		delete(gm.groups, group)
		delete(gm.assignments, group)
		return
	}
	// rebalance remaining members
	gm.rebalanceLocked(group, topic, partitions)
}

// GetAssignment returns a copy of assigned partitions for member, or nil if none.
func (gm *GroupManager) GetAssignment(group, memberID, topic string) []int {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	if _, ok := gm.assignments[group]; !ok {
		return nil
	}
	if _, ok := gm.assignments[group][topic]; !ok {
		return nil
	}
	assign, ok := gm.assignments[group][topic][memberID]
	if !ok {
		return nil
	}
	out := make([]int, len(assign))
	copy(out, assign)
	return out
}

// CommitOffset delegates to storage
func (gm *GroupManager) CommitOffset(group, topic string, partition int, offset int64) error {
	return gm.store.CommitOffset(group, topic, partition, offset)
}

func (gm *GroupManager) GetCommittedOffset(group, topic string, partition int) (int64, error) {
	return gm.store.GetCommittedOffset(group, topic, partition)
}

// rebalanceLocked recomputes assignments for the given group/topic.
// Must be called with gm.mu held (locked).
func (gm *GroupManager) rebalanceLocked(group, topic string, partitions int) {
	// collect members in deterministic order
	membersMap := gm.groups[group][topic]
	members := make([]string, 0, len(membersMap))
	for id := range membersMap {
		members = append(members, id)
	}
	sort.Strings(members) // deterministic ordering

	// prepare assignments tables
	if _, ok := gm.assignments[group]; !ok {
		gm.assignments[group] = make(map[string]map[string][]int)
	}
	if _, ok := gm.assignments[group][topic]; !ok {
		gm.assignments[group][topic] = make(map[string][]int)
	}
	// reset assignments
	for _, id := range members {
		gm.assignments[group][topic][id] = nil
	}

	if len(members) == 0 {
		return
	}

	// simple round-robin partition assignment:
	// partition i -> members[i % M]
	M := len(members)
	for p := 0; p < partitions; p++ {
		idx := p % M
		member := members[idx]
		gm.assignments[group][topic][member] = append(gm.assignments[group][topic][member], p)
	}
}
