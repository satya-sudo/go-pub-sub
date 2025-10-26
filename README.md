# 📨 mini-pubsub

A lightweight **gRPC-based Publish/Subscribe system** written in Go.
Built for learning and experimentation — simple, modular, and easy to extend.

---

## 🚀 Overview

`mini-pubsub` is a minimal implementation of a **message broker** similar to Kafka or NATS, designed to understand how core Pub/Sub systems work internally.

It provides:

* gRPC APIs for **Publish**, **Subscribe**, and **Ack**
* **In-memory storage** (with pluggable backends)
* **Partitioned topics** and basic **offset tracking**
* A structure ready to evolve into a distributed system

---

## 🧩 Architecture

```
Producer ──► gRPC Server ──► Broker ──► Storage
                                   │
                                   ▼
                             Consumer (stream)
```

### Core Components

| Component           | Description                                                      |
| ------------------- | ---------------------------------------------------------------- |
| **Broker**          | Handles publish, subscribe, and notification logic.              |
| **Storage Engine**  | Abstracts message persistence (in-memory for MVP).               |
| **gRPC API**        | Defines how clients interact with the broker.                    |
| **Consumer Groups** | (Planned) Coordinate message consumption among multiple clients. |

---

## 🏗️ Project Structure

```
mini-pubsub/
├── api/                    # .proto files + generated Go gRPC code
│   └── pubsub.proto
│
├── cmd/                    # Executable entry points
│   └── server/
│       └── main.go
│
├── internal/               # Core logic (private to repo)
│   ├── broker/             # Broker orchestration (publish, subscribe, notify)
│   ├── storage/            # Persistence layer
│   ├── consumer/           # Consumer group management (future)
│   ├── middleware/         # Interceptors (future)
│   └── config/             # Config loader (YAML/env)
│
├── client/                 # gRPC client wrapper for apps/tests
├── config/                 # Configuration files
├── scripts/                # Helper scripts
├── Makefile                # Build + proto generation commands
├── go.mod                  # Go module definition
└── README.md               # Project overview and usage
```

---

## ⚙️ Setup

### Prerequisites

* Go 1.20+
* `protoc` (Protocol Buffers compiler)
* Go protobuf plugins:

  ```bash
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  ```

### Clone and initialize

```bash
git clone https://github.com/yourname/mini-pubsub.git
cd mini-pubsub
go mod tidy
```

### Generate gRPC code

```bash
chmod +x scripts/gen.sh
./scripts/gen.sh
```

### Run the server

```bash
go run ./cmd/server
```

Server listens by default on **`:50051`**.

---

## 🧪 Testing (once basic server is up)

You can use [`grpcurl`](https://github.com/fullstorydev/grpcurl) or [Evans CLI](https://github.com/ktr0731/evans) to test:

**Publish:**

```bash
grpcurl -plaintext -d '{"topic":"events","value":"aGVsbG8="}' localhost:50051 pubsub.PubSub/Publish
```

**Subscribe:**
Use Evans interactive CLI to open a stream:

```bash
evans -r repl -p 50051
> service pubsub.PubSub
> call Subscribe
```

---

## 📦 Roadmap

| Stage | Feature                                   | Status        |
| ----- | ----------------------------------------- | ------------- |
| 1     | gRPC server + in-memory publish/subscribe | ✅ In progress |
| 2     | Ack & consumer offset tracking            | ⏳ Planned     |
| 3     | File-based storage (WAL + segments)       | ⏳ Planned     |
| 4     | Consumer groups & partition assignment    | ⏳ Planned     |
| 5     | Metrics, logs, and config management      | ⏳ Planned     |
| 6     | Multi-node clustering with Raft           | 🔜 Future     |

---

## 🧠 Learning Goals

This project helps you understand:

* How message brokers manage topics and offsets.
* The internal flow of publish/subscribe mechanics.
* How gRPC streaming works in Go.
* Scaling patterns for distributed messaging systems.

---

## 🧑‍💻 Author

**Satyam Shree (@satya-sudo)**
Built to learn gRPC, concurrency, and distributed systems in Go.

---

## 🪄 License

MIT
