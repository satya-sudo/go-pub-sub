package client

import (
	"context"
	"flag"
	"io"
	"log"

	"google.golang.org/grpc"

	pb "github.com/satya-sudo/go-pub-sub/api"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "broker addr")
	topic := flag.String("topic", "events", "topic")
	group := flag.String("group", "g1", "consumer group")
	flag.Parse()

	conn, err := grpc.Dial(*addr, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewPubSubClient(conn)
	ctx := context.Background()

	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{
		Topic:     *topic,
		Group:     *group,
		Partition: -1,
		Offset:    -1, // -1 = latest as per your proto
		AutoAck:   false,
	})
	if err != nil {
		log.Fatalf("subscribe failed: %v", err)
	}
	log.Printf("subscribed to %s as %s", *topic, *group)

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			log.Println("stream closed")
			return
		}
		if err != nil {
			log.Fatalf("recv error: %v", err)
		}

		log.Printf("msg received topic=%s partition=%d offset=%d value=%s",
			msg.Topic, msg.Partition, msg.Offset, string(msg.Value))

		// manual ack
		_, err = client.Ack(ctx, &pb.AckRequest{
			Topic:     msg.Topic,
			Partition: msg.Partition,
			Offset:    msg.Offset,
			Group:     *group,
		})
		if err != nil {
			log.Printf("ack error: %v", err)
		} else {
			log.Printf("ack sent for offset=%d", msg.Offset)
		}
	}
}
