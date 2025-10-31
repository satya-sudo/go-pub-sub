package middleware

import (
	"context"
	"fmt"
	"io"
	"log"
	"runtime/debug"
	"time"

	"github.com/satya-sudo/go-pub-sub/internal/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// --------------------
// Unary Interceptors
// --------------------

// UnaryLoggingInterceptor logs request start/finish and latency.
func UnaryLoggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// add a request id
	reqID := utils.NewMemberID()
	ctx = context.WithValue(ctx, ctxRequestIDKey{}, reqID)

	start := time.Now()
	log.Printf("[req %s] unary start: %s", reqID, info.FullMethod)

	resp, err := handler(ctx, req)

	lat := time.Since(start)
	if err != nil {
		log.Printf("[req %s] unary error: %s latency=%s err=%v", reqID, info.FullMethod, lat, err)
	} else {
		log.Printf("[req %s] unary done: %s latency=%s", reqID, info.FullMethod, lat)
	}
	return resp, err
}

// UnaryAuthInterceptor checks for an 'authorization' metadata header (example).
// For prototype: accepts a token "dev-token" or empty (if you want to disable).
func UnaryAuthInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	toks := md.Get("authorization")
	if len(toks) == 0 {
		// not authenticated - for prototype maybe allow; otherwise reject:
		// return nil, status.Errorf(codes.Unauthenticated, "missing authorization")
		// We'll allow but log
		log.Printf("[auth] missing authorization for %s", info.FullMethod)
	} else {
		// simple check
		if toks[0] != "dev-token" {
			// reject for prototype
			log.Printf("[auth] invalid token for %s", info.FullMethod)
			// return nil, status.Errorf(codes.PermissionDenied, "invalid token")
		}
	}
	return handler(ctx, req)
}

// UnaryRecoveryInterceptor recovers from panics and returns an error instead of crashing.
func UnaryRecoveryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[panic] recovered in unary %s: %#v\n%s", info.FullMethod, r, debug.Stack())
			// you can return a gRPC status error here, but for prototype we wrap as fmt.Errorf
			err = fmt.Errorf("internal server error")
		}
	}()
	return handler(ctx, req)
}

// --------------------
// Stream Interceptors
// --------------------

// streamServerWrapper wraps grpc.ServerStream to allow modifying Context()
type streamServerWrapper struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *streamServerWrapper) Context() context.Context { return w.ctx }

// StreamLoggingInterceptor logs stream start/close and can inject request ID.
func StreamLoggingInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	reqID := utils.NewMemberID()
	ctx := context.WithValue(ss.Context(), ctxRequestIDKey{}, reqID)
	log.Printf("[stream %s] start: %s", reqID, info.FullMethod)
	wrapped := &streamServerWrapper{ServerStream: ss, ctx: ctx}

	start := time.Now()
	err := handler(srv, wrapped)
	lat := time.Since(start)
	if err != nil && err != io.EOF {
		log.Printf("[stream %s] error: %s latency=%s err=%v", reqID, info.FullMethod, lat, err)
	} else {
		log.Printf("[stream %s] done: %s latency=%s", reqID, info.FullMethod, lat)
	}
	return err
}

// StreamAuthInterceptor checks metadata at stream start.
func StreamAuthInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	md, _ := metadata.FromIncomingContext(ss.Context())
	toks := md.Get("authorization")
	if len(toks) == 0 {
		log.Printf("[auth-stream] missing authorization for %s", info.FullMethod)
		// either reject or allow for prototype
		// return status.Errorf(codes.Unauthenticated, "missing token")
	}
	return handler(srv, ss)
}

// StreamRecoveryInterceptor recovers from panics inside stream handlers.
func StreamRecoveryInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[panic] recovered in stream %s: %#v\n%s", info.FullMethod, r, debug.Stack())
			err = fmt.Errorf("internal server error")
		}
	}()
	return handler(srv, ss)
}

// --------------------
// Chaining helpers
// --------------------

// ctxRequestIDKey is a small private type for context key
type ctxRequestIDKey struct{}

// ChainUnaryServer chains multiple unary interceptors into one.
func ChainUnaryServer(interceptors ...grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	// return identity if none
	if len(interceptors) == 0 {
		return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			return handler(ctx, req)
		}
	}
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// build nested handler chain
		curr := handler
		// apply in reverse so interceptors run in given order
		for i := len(interceptors) - 1; i >= 0; i-- {
			ic := interceptors[i]
			next := curr
			curr = func(c context.Context, r interface{}) (interface{}, error) {
				return ic(c, r, info, next)
			}
		}
		return curr(ctx, req)
	}
}

// ChainStreamServer chains multiple stream interceptors into one.
func ChainStreamServer(interceptors ...grpc.StreamServerInterceptor) grpc.StreamServerInterceptor {
	if len(interceptors) == 0 {
		return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			return handler(srv, ss)
		}
	}
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		curr := handler
		for i := len(interceptors) - 1; i >= 0; i-- {
			ic := interceptors[i]
			next := curr
			curr = func(srv interface{}, stream grpc.ServerStream) error {
				return ic(srv, stream, info, next)
			}
		}
		return curr(srv, ss)
	}
}
