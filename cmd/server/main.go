package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/satya-sudo/go-pub-sub/api"
	"github.com/satya-sudo/go-pub-sub/internal/broker"
	"github.com/satya-sudo/go-pub-sub/internal/storage"

	"google.golang.org/grpc"
)

var (
	addr     = flag.String("addr", ":50051", "gRPC listen address")
	storeTyp = flag.String("store", "memory", "storage backend to use: memory|file (file not implemented yet)")
)

func main() {
	flag.Parse()
	fmt.Printf("go-pub-sub starting — addr=%s store=%s\n", *addr, *storeTyp)
	var st storage.LogStorage
	switch *storeTyp {
	case "memory":
		st = storage.NewInMemoryStore()
	default:
		log.Fatalf("unsupported store type: %s", *storeTyp)
	}

	br := broker.NewBroker(st)

	// Start gRPC server
	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", *addr, err)
	}

	grpcSrv := grpc.NewServer()
	api.RegisterPubSubServer(grpcSrv, br)

	// Graceful shutdown setup
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	go func() {
		fmt.Printf("gRPC server listening on %s\n", *addr)
		if err := grpcSrv.Serve(lis); err != nil {
			// Serve returns non-nil on Listen/Serve failure or after Stop/GracefulStop
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-sigs
	fmt.Println("\nshutting down gRPC server...")

	// Graceful stop
	grpcSrv.GracefulStop()

	// Close storage if it supports Close (optional)
	if closer, ok := st.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			log.Printf("warning: error closing storage: %v", err)
		}
	}

	fmt.Println("server stopped")
}
