package broker

import (
	"context"
	"fmt"
	"sync"

	"github.com/satya-sudo/go-pub-sub/api"
	"github.com/satya-sudo/go-pub-sub/internal/storage"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ api.PubSubServer = (*Broker)(nil)

// Broker coordinates storage + subscriptions.
type Broker struct {
	api.UnimplementedPubSubServer

	store storage.LogStorage

	mu   sync.RWMutex
	subs map[string][]chan *api.Message // key = topic:partition
}

// NewBroker creates a broker with given storage implementation.
func NewBroker(st storage.LogStorage) *Broker {
	return &Broker{
		store: st,
		subs:  make(map[string][]chan *api.Message),
	}
}

func tpKey(topic string, partition int32) string {
	return fmt.Sprintf("%s:%d", topic, partition)
}

// Publish implements unary publish:
// 1) append to storage
// 2) notify subscribers of that topic/partition
func (b *Broker) Publish(ctx context.Context, req *api.PublishRequest) (*api.PublishResponse, error) {
	if req == nil || req.Topic == "" {
		return nil, fmt.Errorf("invalid publish request: missing topic")
	}

	// For prototype, choose partition 0 when not specified.
	part := int(req.Partition)
	if part < 0 {
		part = 0
	}

	// Build storage message
	msg := &storage.Message{
		Topic:     req.Topic,
		Partition: int32(part),
		Key:       req.Key,
		Value:     req.Value,
		Headers:   req.Headers,
	}

	off, err := b.store.Append(req.Topic, part, msg)
	if err != nil {
		return nil, err
	}

	// Prepare proto Message to notify subscribers
	protoMsg := &api.Message{
		Topic:     msg.Topic,
		Partition: int32(part),
		Offset:    off,
		Key:       msg.Key,
		Value:     msg.Value,
		Headers:   msg.Headers,
		Timestamp: timestamppb.New(msg.Ts),
	}

	// Notify subscribers (non-blocking)
	key := tpKey(req.Topic, int32(part))
	b.mu.RLock()
	subs := b.subs[key]
	for _, ch := range subs {
		select {
		case ch <- protoMsg:
		default:

		}
	}
	b.mu.RUnlock()

	// Return publish response
	resp := &api.PublishResponse{
		Topic:     req.Topic,
		Partition: int32(part),
		Offset:    off,
		Timestamp: timestamppb.New(msg.Ts),
	}
	return resp, nil
}

// Subscribe implements server-streaming subscribe.
// It will:
// - stream historical messages from req.Offset (if >= 0)
// - register a channel for live notifications and stream new messages until client disconnects
func (b *Broker) Subscribe(req *api.SubscribeRequest, stream api.PubSub_SubscribeServer) error {
	if req == nil || req.Topic == "" {
		return fmt.Errorf("invalid subscribe request: missing topic")
	}

	part := int(req.Partition)
	if part < 0 {
		part = 0
	}

	startOffset := req.Offset
	if startOffset >= 0 {
		msgs, err := b.store.Read(req.Topic, part, startOffset, 0)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			pm := &api.Message{
				Topic:     m.Topic,
				Partition: m.Partition,
				Offset:    m.Offset,
				Key:       m.Key,
				Value:     m.Value,
				Headers:   m.Headers,
				Timestamp: timestamppb.New(m.Ts),
			}
			if err := stream.Send(pm); err != nil {
				return err
			}
		}
	}

	// Register for live updates
	ch := make(chan *api.Message, 64)
	key := tpKey(req.Topic, int32(part))
	b.mu.Lock()
	b.subs[key] = append(b.subs[key], ch)
	b.mu.Unlock()

	// Unregister on exit
	defer func() {
		b.mu.Lock()
		arr := b.subs[key]
		for i := range arr {
			if arr[i] == ch {
				b.subs[key] = append(arr[:i], arr[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
		close(ch)
	}()

	// Stream loop
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case m, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(m); err != nil {
				return err
			}
		}
	}
}

// Ack records the committed offset for a group via storage.
func (b *Broker) Ack(ctx context.Context, req *api.AckRequest) (*emptypb.Empty, error) {
	if req == nil || req.Topic == "" || req.Group == "" {
		return nil, fmt.Errorf("invalid ack request")
	}
	if err := b.store.CommitOffset(req.Group, req.Topic, int(req.Partition), req.Offset); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}
