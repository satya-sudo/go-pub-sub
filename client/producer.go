package client

import (
	"context"
	"flag"
	"log"
	"time"

	"google.golang.org/grpc"

	pb "github.com/satya-sudo/go-pub-sub/api"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "broker addr")
	topic := flag.String("topic", "events", "topic")
	msg := flag.String("msg", "hello world", "message text")
	partition := flag.Int("partition", -1, "partition (-1 for auto)")
	flag.Parse()

	conn, err := grpc.Dial(*addr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewPubSubClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &pb.PublishRequest{
		Topic:     *topic,
		Value:     []byte(*msg), // proto bytes -> raw bytes here
		Partition: int32(*partition),
	}

	resp, err := client.Publish(ctx, req)
	if err != nil {
		log.Fatalf("publish error: %v", err)
	}
	log.Printf("published: topic=%s partition=%d offset=%d", resp.Topic, resp.Partition, resp.Offset)
}
