package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	_ "github.com/lib/pq"
)

// PostgresStore stores messages in Postgres with per-topic-partition offset assignment.
// It maintains three tables:
// - messages(topic text, partition int, offset bigint, payload jsonb, ts timestamptz)
// - offsets(topic text, partition int, last_offset bigint)  -- used to allocate new offsets atomically
// - commits(group text, topic text, partition int, offset bigint) -- consumer committed offsets
type PostgresStore struct {
	db  *sql.DB
	ctx context.Context
}

// NewPostgresStore connects and ensures tables exist.
func NewPostgresStore(connStr string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	ps := &PostgresStore{db: db, ctx: ctx}
	if err := ps.ensureSchema(); err != nil {
		return nil, err
	}
	return ps, nil
}

func (p *PostgresStore) ensureSchema() error {
	// messages table: unique constraint on (topic, partition, offset)
	queries := []string{
		`CREATE TABLE IF NOT EXISTS messages (
			topic TEXT NOT NULL,
			partition INT NOT NULL,
			offset BIGINT NOT NULL,
			payload JSONB NOT NULL,
			ts TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (topic, partition, offset)
		);`,
		`CREATE TABLE IF NOT EXISTS offsets (
			topic TEXT NOT NULL,
			partition INT NOT NULL,
			last_offset BIGINT NOT NULL,
			PRIMARY KEY (topic, partition)
		);`,
		`CREATE TABLE IF NOT EXISTS commits (
			group_id TEXT NOT NULL,
			topic TEXT NOT NULL,
			partition INT NOT NULL,
			offset BIGINT NOT NULL,
			PRIMARY KEY (group_id, topic, partition)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_topic_partition_offset ON messages(topic, partition, offset);`,
	}
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	for _, q := range queries {
		if _, err := tx.Exec(q); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Append assigns an atomic monotonic offset per (topic,partition) and inserts the message.
func (p *PostgresStore) Append(topic string, partition int, msg *Message) (int64, error) {
	if msg == nil {
		return -1, errors.New("nil message")
	}
	if msg.Ts.IsZero() {
		msg.Ts = time.Now().UTC()
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return -1, err
	}

	// Transaction: lock offsets row and increment
	tx, err := p.db.BeginTx(p.ctx, nil)
	if err != nil {
		return -1, err
	}
	defer func() {
		// rollback will be ignored if tx already committed
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	var lastOffset sql.NullInt64
	err = tx.QueryRow(`SELECT last_offset FROM offsets WHERE topic=$1 AND partition=$2 FOR UPDATE`, topic, partition).Scan(&lastOffset)
	if err != nil {
		if err == sql.ErrNoRows {
			// insert initial offset = 0
			if _, err := tx.Exec(`INSERT INTO offsets(topic, partition, last_offset) VALUES($1,$2,$3)`, topic, partition, 0); err != nil {
				return -1, err
			}
			lastOffset.Int64 = 0
			lastOffset.Valid = true
		} else {
			return -1, err
		}
	}
	newOffset := lastOffset.Int64
	// if row existed, bump by 1; but our convention: offsets start at 0 => we should set offset = last_offset + 1
	// If inserted above with 0, we use 0; else increment.
	if lastOffset.Valid {
		// if the row existed and last_offset >= 0, increment
		if !(lastOffset.Int64 == 0 && lastOffset.Valid && false) { /*noop*/
		}
		// compute newOffset = lastOffset + 1
		newOffset = lastOffset.Int64 + 1
	}

	// insert message with newOffset
	_, err = tx.Exec(`INSERT INTO messages(topic, partition, offset, payload, ts) VALUES($1,$2,$3,$4,$5)`,
		topic, partition, newOffset, payload, msg.Ts)
	if err != nil {
		return -1, err
	}

	// update offsets table
	_, err = tx.Exec(`UPDATE offsets SET last_offset=$1 WHERE topic=$2 AND partition=$3`, newOffset, topic, partition)
	if err != nil {
		return -1, err
	}

	if err := tx.Commit(); err != nil {
		return -1, err
	}
	// mark tx nil so deferred rollback doesn't run
	tx = nil
	return newOffset, nil
}

// Read returns messages starting from offset (inclusive), ordered by offset ascending.
// If max == 0 -> return all remaining messages.
func (p *PostgresStore) Read(topic string, partition int, offset int64, max int) ([]*Message, error) {
	if offset < 0 {
		offset = 0
	}
	q := `SELECT payload, offset, ts FROM messages WHERE topic=$1 AND partition=$2 AND offset >= $3 ORDER BY offset ASC`
	var rows *sql.Rows
	var err error
	if max > 0 {
		q = q + " LIMIT $4"
		rows, err = p.db.Query(q, topic, partition, offset, max)
	} else {
		rows, err = p.db.Query(q, topic, partition, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Message{}
	for rows.Next() {
		var raw []byte
		var off int64
		var ts time.Time
		if err := rows.Scan(&raw, &off, &ts); err != nil {
			continue
		}
		var m Message
		if err := json.Unmarshal(raw, &m); err != nil {
			// try fallback: treat payload as the message.Value only
			continue
		}
		// ensure offset & ts reflect stored values
		m.Offset = off
		if m.Ts.IsZero() {
			m.Ts = ts
		}
		out = append(out, &m)
	}
	return out, nil
}

// CommitOffset stores committed offset in commits table (upsert).
func (p *PostgresStore) CommitOffset(group, topic string, partition int, offset int64) error {
	if group == "" {
		return errors.New("group required")
	}
	_, err := p.db.Exec(`
		INSERT INTO commits(group_id, topic, partition, offset)
		VALUES($1,$2,$3,$4)
		ON CONFLICT (group_id, topic, partition) DO UPDATE SET offset = EXCLUDED.offset
	`, group, topic, partition, offset)
	return err
}

// GetCommittedOffset returns committed offset for group/topic/partition or -1 if none.
func (p *PostgresStore) GetCommittedOffset(group, topic string, partition int) (int64, error) {
	if group == "" {
		return -1, errors.New("group required")
	}
	var off sql.NullInt64
	err := p.db.QueryRow(`SELECT offset FROM commits WHERE group_id=$1 AND topic=$2 AND partition=$3`, group, topic, partition).Scan(&off)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil
		}
		return -1, err
	}
	if !off.Valid {
		return -1, nil
	}
	return off.Int64, nil
}

func (p *PostgresStore) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}
