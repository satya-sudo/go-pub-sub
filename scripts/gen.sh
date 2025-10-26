#!/usr/bin/env bash
set -e
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       api/pubsub.proto
echo "gRPC code generated"
