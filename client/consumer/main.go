package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"

	pb "github.com/satya-sudo/go-pub-sub/api"
)

// SimpleLogger logs to both stdout and a file, with leveled output
type SimpleLogger struct {
	logger *log.Logger
	file   *os.File
	debug  bool
}

func NewSimpleLogger(name string, debug bool) *SimpleLogger {
	// ensure file path relative to binary location
	exeDir, _ := os.Getwd()
	logFile := filepath.Join(exeDir, fmt.Sprintf("%s.log", name))

	// open or create file for append
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}

	// multiwriter (stdout + file)
	mw := io.MultiWriter(os.Stdout, f)

	return &SimpleLogger{
		logger: log.New(mw, "", log.LstdFlags|log.Lmicroseconds),
		file:   f,
		debug:  debug,
	}
}

func (l *SimpleLogger) Close() {
	if l.file != nil {
		_ = l.file.Close()
	}
}

func (l *SimpleLogger) Infof(format string, v ...any) {
	l.logger.Printf("INFO  "+format, v...)
}
func (l *SimpleLogger) Errorf(format string, v ...any) {
	l.logger.Printf("ERROR "+format, v...)
}
func (l *SimpleLogger) Debugf(format string, v ...any) {
	if l.debug {
		l.logger.Printf("DEBUG "+format, v...)
	}
}
func (l *SimpleLogger) Fatalf(format string, v ...any) {
	l.logger.Fatalf("FATAL "+format, v...)
}

func main() {
	addr := flag.String("addr", "localhost:50051", "broker addr")
	topic := flag.String("topic", "events", "topic")
	group := flag.String("group", "g1", "consumer group")
	flag.Parse()

	debugOn := os.Getenv("PUBSUB_DEBUG") == "1"
	logger := NewSimpleLogger("consumer", debugOn)
	defer logger.Close()

	logger.Debugf("starting consumer; addr=%s topic=%s group=%s debug=%v", *addr, *topic, *group, debugOn)

	conn, err := grpc.Dial(*addr, grpc.WithInsecure())
	if err != nil {
		logger.Fatalf("dial: %v", err)
	}
	defer func() {
		_ = conn.Close()
		logger.Debugf("connection closed")
	}()

	client := pb.NewPubSubClient(conn)
	ctx := context.Background()

	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{
		Topic:     *topic,
		Group:     *group,
		Partition: -1,
		Offset:    -1,
		AutoAck:   false,
	})
	if err != nil {
		logger.Fatalf("subscribe failed: %v", err)
	}
	logger.Infof("🧩 subscribed to topic=%s group=%s", *topic, *group)

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			logger.Infof("stream closed by server")
			return
		}
		if err != nil {
			logger.Errorf("recv error: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		logger.Infof("➡ received topic=%s partition=%d offset=%d value=%s",
			msg.Topic, msg.Partition, msg.Offset, string(msg.Value))

		_, err = client.Ack(ctx, &pb.AckRequest{
			Topic:     msg.Topic,
			Partition: msg.Partition,
			Offset:    msg.Offset,
			Group:     *group,
		})
		if err != nil {
			logger.Errorf("ack error: %v", err)
		} else {
			logger.Infof("✅ acked offset=%d", msg.Offset)
		}
	}
}
