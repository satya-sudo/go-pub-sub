package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore uses Redis lists (one list per topic:partition) to store messages.
// Commits (consumer group offsets) are stored in a Redis hash.
type RedisStore struct {
	client *redis.Client
	// keyPrefix used to namespace keys (e.g. "gopub:")
	keyPrefix string
	ctx       context.Context
}

func NewRedisStore(addr, password string, db int, keyPrefix string) (*RedisStore, error) {
	opt := &redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	}
	client := redis.NewClient(opt)
	ctx := context.Background()

	// quick ping
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	if keyPrefix == "" {
		keyPrefix = "gopub:"
	}
	return &RedisStore{
		client:    client,
		keyPrefix: keyPrefix,
		ctx:       ctx,
	}, nil
}

func (r *RedisStore) makeListKey(topic string, partition int) string {
	return fmt.Sprintf("%smsg:%s:%d", r.keyPrefix, topic, partition)
}

func (r *RedisStore) commitHashKey(group string) string {
	return fmt.Sprintf("%scommit:%s", r.keyPrefix, group)
}

func (r *RedisStore) marshalMessage(m *Message) ([]byte, error) {
	return json.Marshal(m)
}

func (r *RedisStore) unmarshalMessage(b []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Append appends a message to topic:partition and returns assigned offset (len-1)
func (r *RedisStore) Append(topic string, partition int, msg *Message) (int64, error) {
	key := r.makeListKey(topic, partition)
	if msg == nil {
		return -1, errors.New("nil message")
	}
	// ensure timestamp
	if msg.Ts.IsZero() {
		msg.Ts = time.Now().UTC()
	}
	b, err := r.marshalMessage(msg)
	if err != nil {
		return -1, err
	}
	// RPUSH to append to the list
	if err := r.client.RPush(r.ctx, key, b).Err(); err != nil {
		return -1, err
	}
	// LEN gives new length; offset = len - 1
	l, err := r.client.LLen(r.ctx, key).Result()
	if err != nil {
		return -1, err
	}
	return l - 1, nil
}

// Read returns up to max messages starting at offset (inclusive). If max==0 returns all.
func (r *RedisStore) Read(topic string, partition int, offset int64, max int) ([]*Message, error) {
	key := r.makeListKey(topic, partition)
	// Redis LRANGE expects start & stop indices (0-based). Use -1 for end if max == 0.
	start := offset
	if start < 0 {
		start = 0
	}
	var stop int64
	if max <= 0 {
		stop = -1
	} else {
		stop = start + int64(max) - 1
	}
	raw, err := r.client.LRange(r.ctx, key, start, stop).Result()
	if err != nil {
		return nil, err
	}
	out := make([]*Message, 0, len(raw))
	for i := range raw {
		m, err := r.unmarshalMessage([]byte(raw[i]))
		if err != nil {
			// skip corrupted entry but continue
			continue
		}
		// ensure the offset is set (LRANGE doesn't give index, so compute)
		m.Offset = start + int64(i)
		out = append(out, m)
	}
	return out, nil
}

// CommitOffset uses Redis HASH (HSET) per group to store offsets keyed by "topic:partition"
func (r *RedisStore) CommitOffset(group, topic string, partition int, offset int64) error {
	if group == "" {
		return errors.New("group required")
	}
	hash := r.commitHashKey(group)
	field := fmt.Sprintf("%s:%d", topic, partition)
	return r.client.HSet(r.ctx, hash, field, strconv.FormatInt(offset, 10)).Err()
}

// GetCommittedOffset returns last committed offset for group/topic/partition or -1 if none.
func (r *RedisStore) GetCommittedOffset(group, topic string, partition int) (int64, error) {
	if group == "" {
		return -1, errors.New("group required")
	}
	hash := r.commitHashKey(group)
	field := fmt.Sprintf("%s:%d", topic, partition)
	val, err := r.client.HGet(r.ctx, hash, field).Result()
	if err != nil {
		if err == redis.Nil {
			return -1, nil
		}
		return -1, err
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return -1, err
	}
	return n, nil
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}
