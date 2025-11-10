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

// SimpleLogger logs to both stdout and a local file (e.g., producer.log)
type SimpleLogger struct {
	logger *log.Logger
	file   *os.File
	debug  bool
}

func NewSimpleLogger(name string, debug bool) *SimpleLogger {
	exeDir, _ := os.Getwd()
	logFile := filepath.Join(exeDir, fmt.Sprintf("%s.log", name))

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}

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
	msg := flag.String("msg", "hello world", "message text")
	partition := flag.Int("partition", -1, "partition (-1 for auto)")
	flag.Parse()

	debugOn := os.Getenv("PUBSUB_DEBUG") == "1"
	logger := NewSimpleLogger("producer", debugOn)
	defer logger.Close()

	logger.Debugf("starting producer; addr=%s topic=%s partition=%d msg=%q", *addr, *topic, *partition, *msg)

	conn, err := grpc.Dial(*addr, grpc.WithInsecure())
	if err != nil {
		logger.Fatalf("dial: %v", err)
	}
	defer func() {
		_ = conn.Close()
		logger.Debugf("connection closed")
	}()

	client := pb.NewPubSubClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &pb.PublishRequest{
		Topic:     *topic,
		Value:     []byte(*msg),
		Partition: int32(*partition),
	}

	resp, err := client.Publish(ctx, req)
	if err != nil {
		logger.Errorf("publish error: %v", err)
		os.Exit(1)
	}

	logger.Infof("📤 published topic=%s partition=%d offset=%d", resp.Topic, resp.Partition, resp.Offset)
}
