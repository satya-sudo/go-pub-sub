package main

import (
	"fmt"
	"log"
	"net"

	"github.com/satya-sudo/go-pub-sub/api"
	"github.com/satya-sudo/go-pub-sub/internal/config"
	"github.com/satya-sudo/go-pub-sub/internal/middleware"
	"google.golang.org/grpc"
)

func main() {
	cfg := config.LoadConfig()
	br := config.InitBroker(cfg)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		log.Fatalf("[server] listen failed: %v", err)
	}

	// ---- Interceptors setup ----
	unary := middleware.ChainUnaryServer(
		middleware.UnaryRecoveryInterceptor,
		middleware.UnaryAuthInterceptor,
		middleware.UnaryLoggingInterceptor,
	)

	stream := middleware.ChainStreamServer(
		middleware.StreamRecoveryInterceptor,
		middleware.StreamAuthInterceptor,
		middleware.StreamLoggingInterceptor,
	)

	// ---- Create gRPC server with interceptors ----
	s := grpc.NewServer(
		grpc.UnaryInterceptor(unary),
		grpc.StreamInterceptor(stream),
	)

	api.RegisterPubSubServer(s, br)
	log.Printf("[server] gRPC broker listening on :%d", cfg.Port)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("[server] serve error: %v", err)
	}
}
