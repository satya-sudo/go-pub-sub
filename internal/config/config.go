package config

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/satya-sudo/go-pub-sub/internal/broker"
	"github.com/satya-sudo/go-pub-sub/internal/storage"
)

// Config represents runtime configuration for the broker server.
type Config struct {
	Port         int
	StoreType    string
	DataDir      string
	DefaultTopic string
}

// MustLoad parses CLI flags and env vars into a Config.
func MustLoad() *Config {
	cfg := &Config{}

	flag.IntVar(&cfg.Port, "port", getEnvInt("BROKER_PORT", 50051), "gRPC server port")
	flag.StringVar(&cfg.StoreType, "store", getEnv("BROKER_STORE", "memory"), "storage backend (memory, redis, file)")
	flag.StringVar(&cfg.DataDir, "data", getEnv("BROKER_DATA_DIR", "./data"), "data directory for file store")
	flag.StringVar(&cfg.DefaultTopic, "topic", getEnv("BROKER_DEFAULT_TOPIC", "events"), "default topic name")
	flag.Parse()

	log.Printf("[config] loaded: port=%d store=%s topic=%s", cfg.Port, cfg.StoreType, cfg.DefaultTopic)
	return cfg
}

// InitBroker wires up the broker and its dependencies based on config.
func InitBroker(cfg *Config) *broker.Broker {
	var st storage.LogStorage
	err := error(nil)
	switch cfg.StoreType {
	case "memory":
		st, _ = storage.NewInMemoryStore()
	case "file":
		log.Println("[config] file store not implemented yet; using memory")
		st, _ = storage.NewInMemoryStore()
	case "redis":
		// example: REDIS_ADDR=localhost:6379
		st, err = storage.NewRedisStore(getEnv("REDIS_ADDR", "localhost:6379"), getEnv("REDIS_PASS", ""), 0, "gopub:")
		if err != nil {
			log.Fatalf("redis init: %v", err)
		}
	case "postgres":
		conn := getEnv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/gopub?sslmode=disable")
		st, err = storage.NewPostgresStore(conn)
		if err != nil {
			log.Fatalf("postgres init: %v", err)
		}

	default:
		log.Printf("[config] unknown store type %s; fallback to memory", cfg.StoreType)
		st, _ = storage.NewInMemoryStore()
	}

	br := broker.NewBroker(st)

	// bootstrap default topic
	if !br.TM().TopicExists(cfg.DefaultTopic) {
		if err := br.TM().CreateTopic(cfg.DefaultTopic, 1, 24*3600*1000); err != nil {
			log.Printf("[config] failed to create default topic: %v", err)
		} else {
			log.Printf("[config] default topic created: %s", cfg.DefaultTopic)
		}
	}

	return br
}

// Helpers ----------------------------------------------------------

func getEnv(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}

func getEnvInt(key string, def int) int {
	if val := os.Getenv(key); val != "" {
		var n int
		fmt.Sscanf(val, "%d", &n)
		return n
	}
	return def
}
