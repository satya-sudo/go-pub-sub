package broker

import (
	"context"
	"fmt"
	"sync"

	"github.com/satya-sudo/go-pub-sub/api"
	"github.com/satya-sudo/go-pub-sub/internal/consumer"
	"github.com/satya-sudo/go-pub-sub/internal/storage"
	"github.com/satya-sudo/go-pub-sub/internal/utils"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Ensure Broker implements generated interface
var _ api.PubSubServer = (*Broker)(nil)

type Broker struct {
	api.UnimplementedPubSubServer

	store storage.LogStorage
	pm    *PartitionManager
	tm    *TopicManager
	gm    *consumer.GroupManager

	mu   sync.RWMutex
	subs map[string][]chan *api.Message // key = topic:partition
}

func NewBroker(st storage.LogStorage) *Broker {
	pm := NewPartitionManager(st)
	tm := NewTopicManager(pm)
	gm := consumer.NewGroupManager(st)
	return &Broker{
		store: st,
		pm:    pm,
		tm:    tm,
		gm:    gm,
		subs:  make(map[string][]chan *api.Message),
	}
}

func (b *Broker) TM() *TopicManager {
	return b.tm
}

func tpKey(topic string, partition int32) string {
	return fmt.Sprintf("%s:%d", topic, partition)
}

// Publish unchanged (delegates to PartitionManager)
func (b *Broker) Publish(ctx context.Context, req *api.PublishRequest) (*api.PublishResponse, error) {
	if req == nil || req.Topic == "" {
		return nil, fmt.Errorf("invalid publish request: missing topic")
	}
	if !b.tm.TopicExists(req.Topic) {
		return nil, fmt.Errorf("unknown topic: %s", req.Topic)
	}
	msg := &storage.Message{
		Topic:   req.Topic,
		Key:     req.Key,
		Value:   req.Value,
		Headers: req.Headers,
	}
	off, part, err := b.pm.Append(req.Topic, int(req.Partition), req.Key, msg)
	if err != nil {
		return nil, err
	}
	protoMsg := &api.Message{
		Topic:     msg.Topic,
		Partition: part,
		Offset:    off,
		Key:       msg.Key,
		Value:     msg.Value,
		Headers:   msg.Headers,
		Timestamp: timestamppb.New(msg.Ts),
	}
	key := tpKey(req.Topic, part)
	b.mu.RLock()
	subs := b.subs[key]
	for _, ch := range subs {
		select {
		case ch <- protoMsg:
		default:
			// drop on slow subscriber for prototype
		}
	}
	b.mu.RUnlock()
	return &api.PublishResponse{
		Topic:     req.Topic,
		Partition: part,
		Offset:    off,
		Timestamp: timestamppb.New(msg.Ts),
	}, nil
}

// Subscribe integrates GroupManager and multiplexes partitions
func (b *Broker) Subscribe(req *api.SubscribeRequest, stream api.PubSub_SubscribeServer) error {
	if req == nil || req.Topic == "" {
		return fmt.Errorf("invalid subscribe request: missing topic")
	}
	if !b.tm.TopicExists(req.Topic) {
		return fmt.Errorf("unknown topic: %s", req.Topic)
	}

	// If no group -> previous fan-out behavior (single partition default)
	if req.Group == "" {
		// existing single-partition subscribe logic (default partition 0)
		part := int(req.Partition)
		if part < 0 {
			part = 0
		}
		if err := b.tm.ValidatePartition(req.Topic, part); err != nil {
			return err
		}
		// historical
		if req.Offset >= 0 {
			msgs, err := b.pm.Read(req.Topic, part, req.Offset, 0)
			if err != nil {
				return err
			}
			for _, m := range msgs {
				if err := stream.Send(&api.Message{
					Topic:     m.Topic,
					Partition: m.Partition,
					Offset:    m.Offset,
					Key:       m.Key,
					Value:     m.Value,
					Headers:   m.Headers,
					Timestamp: timestamppb.New(m.Ts),
				}); err != nil {
					return err
				}
			}
		}
		// register live updates
		ch := make(chan *api.Message, 64)
		key := tpKey(req.Topic, int32(part))
		b.mu.Lock()
		b.subs[key] = append(b.subs[key], ch)
		b.mu.Unlock()
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

	// -------- With group: use GroupManager assignments and multiplex --------
	partitions := b.pm.GetPartitionCount(req.Topic)
	if partitions == 0 {
		return fmt.Errorf("topic has no partitions: %s", req.Topic)
	}

	// generate a memberID for this subscription
	memberID := utils.NewMemberID()

	// join group and receive assigned partitions
	assignments, err := b.gm.Join(req.Group, memberID, req.Topic, partitions)
	if err != nil {
		return err
	}
	// ensure we leave on exit
	defer b.gm.Leave(req.Group, memberID, req.Topic, partitions)

	// central channel to multiplex all assigned partition messages
	central := make(chan *api.Message, 256)
	var forwardWg sync.WaitGroup

	// track registered channels to unregister later
	type reg struct {
		key string
		ch  chan *api.Message
	}
	var regs []reg

	// For each assigned partition:
	for _, p := range assignments {
		// validate partition
		if err := b.tm.ValidatePartition(req.Topic, p); err != nil {
			// ignore invalid, continue
			continue
		}

		// determine start offset for historical read
		var startOffset int64
		if req.Offset >= 0 {
			startOffset = req.Offset
		} else {
			comm, _ := b.gm.GetCommittedOffset(req.Group, req.Topic, p)
			if comm >= 0 {
				startOffset = comm + 1
			} else {
				startOffset = 0
			}
		}

		// send historical messages for this partition
		msgs, err := b.pm.Read(req.Topic, p, startOffset, 0)
		if err != nil {
			// fail fast on read errors
			// cleanups deferred below will run
			return err
		}
		for _, m := range msgs {
			select {
			case <-stream.Context().Done():
				// client disconnected
				return nil
			default:
			}
			if err := stream.Send(&api.Message{
				Topic:     m.Topic,
				Partition: m.Partition,
				Offset:    m.Offset,
				Key:       m.Key,
				Value:     m.Value,
				Headers:   m.Headers,
				Timestamp: timestamppb.New(m.Ts),
			}); err != nil {
				return err
			}
		}

		// register for live updates on this partition
		ch := make(chan *api.Message, 64)
		key := tpKey(req.Topic, int32(p))
		b.mu.Lock()
		b.subs[key] = append(b.subs[key], ch)
		b.mu.Unlock()
		regs = append(regs, reg{key: key, ch: ch})

		// forwarder: move from per-partition ch -> central chan
		forwardWg.Add(1)
		go func(c chan *api.Message) {
			defer forwardWg.Done()
			for m := range c {
				select {
				case central <- m:
				case <-stream.Context().Done():
					return
				}
			}
		}(ch)
	}

	// cleanup: unregister channels, close them, wait forwarders
	defer func() {
		// unregister and close chans
		b.mu.Lock()
		for _, r := range regs {
			arr := b.subs[r.key]
			for i := range arr {
				if arr[i] == r.ch {
					b.subs[r.key] = append(arr[:i], arr[i+1:]...)
					break
				}
			}
			// close channel to stop forwarder
			close(r.ch)
		}
		b.mu.Unlock()
		forwardWg.Wait()
		close(central)
	}()

	// stream loop: multiplex central channel into gRPC stream
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case m, ok := <-central:
			if !ok {
				return nil
			}
			if err := stream.Send(m); err != nil {
				return err
			}
		}
	}
}

// Ack delegates to partition manager / storage commit
func (b *Broker) Ack(ctx context.Context, req *api.AckRequest) (*emptypb.Empty, error) {
	if req == nil || req.Topic == "" || req.Group == "" {
		return nil, fmt.Errorf("invalid ack request")
	}
	if err := b.pm.CommitOffset(req.Group, req.Topic, int(req.Partition), req.Offset); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// --- Admin API ---

func (b *Broker) CreateTopic(ctx context.Context, req *api.CreateTopicRequest) (*api.CreateTopicResponse, error) {
	if req == nil || req.Topic == "" {
		return &api.CreateTopicResponse{Ok: false, Reason: "topic name required"}, nil
	}
	if req.Partitions <= 0 {
		return &api.CreateTopicResponse{Ok: false, Reason: "partitions must be > 0"}, nil
	}
	if req.RetentionMs <= 0 {
		req.RetentionMs = b.tm.defaultRetentionMs
	}
	if b.tm.TopicExists(req.Topic) {
		return &api.CreateTopicResponse{Ok: false, Reason: "topic already exists"}, nil
	}
	if err := b.tm.CreateTopic(req.Topic, int(req.Partitions), req.RetentionMs); err != nil {
		return &api.CreateTopicResponse{Ok: false, Reason: err.Error()}, nil
	}
	return &api.CreateTopicResponse{Ok: true}, nil
}

func (b *Broker) GetTopicInfo(ctx context.Context, req *api.TopicInfoRequest) (*api.TopicInfoResponse, error) {
	if req == nil || req.Topic == "" {
		return &api.TopicInfoResponse{Found: false, Reason: "topic required"}, nil
	}
	ti, ok := b.tm.GetTopicInfo(req.Topic)
	if !ok {
		return &api.TopicInfoResponse{Found: false, Reason: "not found"}, nil
	}
	info := &api.TopicInfo{
		Topic:       ti.Name,
		Partitions:  int32(ti.Partitions),
		RetentionMs: ti.RetentionMs,
		CreatedAt:   timestamppb.New(ti.CreatedAt),
	}
	return &api.TopicInfoResponse{Info: info, Found: true}, nil
}

func (b *Broker) ListTopics(ctx context.Context, req *api.ListTopicsRequest) (*api.ListTopicsResponse, error) {
	out := &api.ListTopicsResponse{}
	tlist := b.tm.ListTopics()
	for _, t := range tlist {
		out.Topics = append(out.Topics, &api.TopicInfo{
			Topic:       t.Name,
			Partitions:  int32(t.Partitions),
			RetentionMs: t.RetentionMs,
			CreatedAt:   timestamppb.New(t.CreatedAt),
		})
	}
	return out, nil
}
