# go-pub-sub — Lightweight gRPC Publish–Subscribe Broker

> A distributed, gRPC-based Publish–Subscribe system written entirely in Go — designed for learning and small-scale production workloads.

---

## 📘 Overview

`go-pub-sub` is a compact, extensible broker implementing classic pub/sub mechanics:
- **Publish** messages via gRPC API
- **Subscribe** over bidirectional gRPC streams
- **Ack** and offset tracking
- **Consumer groups & partitioning**
- **Pluggable storage backends** (in-memory, Redis, Postgres)
- **Topic admin APIs**

Built to learn distributed systems, gRPC streams, and storage backends — while remaining deployable as a real broker.

---

## ⚙️ Features

✅ gRPC-based Publish, Subscribe, and Ack APIs  
✅ Consumer groups with dynamic partition assignment  
✅ Multiple storage backends (in-memory, Redis, Postgres)  
✅ Admin APIs: CreateTopic, ListTopics, GetTopicInfo  
✅ Configurable via YAML (no flags/env by default)  
✅ Structured logging (to console + files)  
✅ Dockerfile & Compose for easy deployment  
✅ Smoke test pipeline using real containers  
✅ Extensible for chat/messaging apps or internal pipelines

---

## 🧩 Architecture

```text
Publisher(s) → gRPC (Publish)
Consumer(s)  ← gRPC Stream (Subscribe)
↳ Ack → gRPC (Ack)
↳ Admin APIs → CreateTopic, ListTopics, TopicInfo
```

### Key Components

| Layer | Description |
|--------|-------------|
| **cmd/server** | Broker entrypoint |
| **client/producer** | Simple message publisher |
| **client/consumer** | gRPC stream consumer with ack |
| **internal/broker** | Implements core pub/sub behavior |
| **internal/storage** | Pluggable backends (memory, redis, postgres) |
| **internal/config** | YAML loader + broker initialization |
| **internal/middleware** | gRPC interceptors (logging, recovery, auth) |
| **api/pubsub.proto** | gRPC definitions |
| **scripts/** | smoke test scripts for Docker-based runs |

---

## 🚀 Quick Start

### 1️⃣ Run Locally (In-memory mode)
```bash
go run ./cmd/server --config=config/config.yaml
```

Then use the CLI clients:
```bash
go run ./client/consumer --addr=localhost:50051 --topic=events --group=g1
go run ./client/producer --addr=localhost:50051 --topic=events --msg="hello world"
```

### 2️⃣ Docker Build
```bash
make build-image
docker run -p 50051:50051 go-pub-sub:local
```

### 3️⃣ Smoke Test (container broker + local clients)
```bash
make smoke-test
```

Runs the broker inside Docker, spawns a local consumer/producer pair, and validates end-to-end delivery.

---

## 🗄️ Configuration

### `config/config.yaml`
```yaml
server:
  port: 50051
  grpc_listen: "0.0.0.0:50051"
storage:
  type: memory     # memory | redis | postgres
  postgres:
    dsn: "postgres://gopub:password@postgres:5432/gopub?sslmode=disable"
  redis:
    addr: "localhost:6379"
logging:
  level: info
  file: ""
topics:
  default: "events"
retention:
  default_ms: 86400000
```

---

## 🧱 gRPC Services

### PubSub Service
| Method | Description |
|--------|-------------|
| `Publish(PublishRequest)` | Publish a message |
| `Subscribe(SubscribeRequest)` | Stream messages to a consumer |
| `Ack(AckRequest)` | Commit offset for a consumer group |

### Admin Service
| Method | Description |
|--------|-------------|
| `CreateTopic(CreateTopicRequest)` | Create new topic |
| `GetTopicInfo(TopicInfoRequest)` | Fetch topic metadata |
| `ListTopics(ListTopicsRequest)` | List all topics |

---

## 🧰 Smoke Testing

The script [`scripts/smoke_with_docker.sh`](scripts/smoke_with_docker.sh) automates:
1. Spinning up the broker container
2. Waiting for gRPC readiness via logs
3. Running a local consumer + multiple producers
4. Validating all messages were received + acked

```bash
./scripts/smoke_with_docker.sh -n 100 -i go-pub-sub:local -p 50051
```

Logs for broker, consumer, and producer are saved under `/tmp/` and in their respective directories.

---

## 🧩 Storage Backends

| Backend | File | Description |
|----------|------|-------------|
| In-Memory | `memory_store.go` | Fast ephemeral (default) |
| Redis | `redis_store.go` | In-memory + persistence hybrid |
| Postgres | `pg_store.go` | Durable, production-ready backend (recommended) |

**Upcoming:** automatic topic retention and message compaction.

---

## 🩺 Health, Metrics & Observability

- `HealthCheck` gRPC (upcoming)
- `/metrics` Prometheus endpoint (planned)
- Logging to both stdout & rotating log files
- JSON log format optional (future flag)

---

## 🧠 Learning Goal

The broker is designed as a minimal **Kafka-like system** in Go:
- Topics, partitions, offsets
- Consumer groups & rebalancing
- Acks and offset tracking
- Extendable with real storage backends

You can easily build on top of it — e.g.:
- a chat/messaging system (Topics = rooms, Groups = users)
- a real-time analytics pipeline
- an IoT event router

---

## 🧱 Directory Structure

```
go-pub-sub/
├── api/                 # protobuf definitions
├── cmd/
│   └── server/          # broker entrypoint
├── client/
│   ├── consumer/        # gRPC stream consumer
│   └── producer/        # message publisher
├── internal/
│   ├── broker/          # core logic
│   ├── config/          # config loader (YAML)
│   ├── middleware/      # interceptors
│   └── storage/         # in-memory, redis, postgres
├── config/              # YAML configs
├── scripts/             # smoke tests
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── README.md
```

---

## 🧩 Next Steps

- [ ] Implement `pg_store.go` for durable message persistence
- [ ] Add health and metrics endpoints
- [ ] Add topic retention cleanup job
- [ ] Improve producer batching
- [ ] Add JSON log option & Prometheus metrics
- [ ] Add integration tests for Postgres backend
- [ ] Extend for WebSocket/gRPC-Web support

---

## 💬 License

MIT License © 2025 [Satyam Shree](https://github.com/satya-sudo)

---
