package config

import (
	"flag"
	"log"
	"os"

	"github.com/satya-sudo/go-pub-sub/internal/broker"
	"github.com/satya-sudo/go-pub-sub/internal/storage"
	"gopkg.in/yaml.v3"
)

type RedisConfig struct {
	Addr string `yaml:"addr"`
	Pass string `yaml:"pass"`
	DB   int    `yaml:"db"`
}

type PostgresConfig struct {
	DSN string `yaml:"dsn"`
}

type StoreConfig struct {
	Type     string         `yaml:"type"`
	DataDir  string         `yaml:"data_dir"`
	Redis    RedisConfig    `yaml:"redis"`
	Postgres PostgresConfig `yaml:"postgres"`
}

type Config struct {
	Port         int         `yaml:"port"`
	Store        StoreConfig `yaml:"store"`
	DefaultTopic string      `yaml:"default_topic"`
	RetentionMs  int64       `yaml:"retention_ms"`
}

// MustLoad loads YAML config (only YAML, no env/flags except --config path).
func LoadConfig() *Config {
	path := flag.String("config", "config.yaml", "path to YAML config")
	flag.Parse()

	data, err := os.ReadFile(*path)
	if err != nil {
		log.Fatalf("[config] ❌ failed to read config file %s: %v", *path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("[config] ❌ failed to parse yaml: %v", err)
	}

	// --- Verbose startup log ---
	log.Println("────────────────────────────────────────────────────")
	log.Printf("[config] ✅ configuration loaded from: %s", *path)
	log.Println("----------------------------------------------------")
	log.Printf(" • gRPC Port        : %d", cfg.Port)
	log.Printf(" • Default Topic    : %s", cfg.DefaultTopic)
	log.Printf(" • Retention (ms)   : %d", cfg.RetentionMs)
	log.Printf(" • Store Type       : %s", cfg.Store.Type)
	log.Printf(" • Data Directory   : %s", cfg.Store.DataDir)
	switch cfg.Store.Type {
	case "memory":
		log.Printf(" • Memory Store     : (in-memory, ephemeral)")
	case "redis":
		log.Printf(" • Redis Addr       : %s", cfg.Store.Redis.Addr)
		log.Printf(" • Redis DB         : %d", cfg.Store.Redis.DB)
		log.Printf(" • Redis Prefix     : gopub:")
	case "postgres":
		log.Printf(" • Postgres DSN     : %s", cfg.Store.Postgres.DSN)
	case "file":
		log.Printf(" • File Store Dir   : %s", cfg.Store.DataDir)
	default:
		log.Printf(" • ⚠️  Unknown Store : %s (fallback: memory)", cfg.Store.Type)
	}
	log.Println("────────────────────────────────────────────────────")

	return &cfg
}

// InitBroker wires broker + storage backend.
func InitBroker(cfg *Config) *broker.Broker {
	var st storage.LogStorage
	var err error

	switch cfg.Store.Type {
	case "memory":
		st, _ = storage.NewInMemoryStore()
	case "file":
		st, _ = storage.NewInMemoryStore() // stub until implemented
		log.Println("[config] ⚙️ file store not implemented, using in-memory fallback")
	case "redis":
		r := cfg.Store.Redis
		st, err = storage.NewRedisStore(r.Addr, r.Pass, r.DB, "gopub:")
		if err != nil {
			log.Fatalf("[config] ❌ redis init failed: %v", err)
		}
		log.Printf("[config] 🔗 connected to Redis backend @ %s (db=%d)", r.Addr, r.DB)
	case "postgres":
		p := cfg.Store.Postgres
		st, err = storage.NewPostgresStore(p.DSN)
		if err != nil {
			log.Fatalf("[config] ❌ postgres init failed: %v", err)
		}
		log.Printf("[config] 🔗 connected to Postgres backend: %s", p.DSN)
	default:
		st, _ = storage.NewInMemoryStore()
		log.Printf("[config] ⚠️ unknown store type '%s'; using in-memory fallback", cfg.Store.Type)
	}

	br := broker.NewBroker(st)

	if !br.TM().TopicExists(cfg.DefaultTopic) {
		if err := br.TM().CreateTopic(cfg.DefaultTopic, 1, cfg.RetentionMs); err != nil {
			log.Printf("[config] ⚠️ failed to create default topic: %v", err)
		} else {
			log.Printf("[config] 🧩 default topic created: %s", cfg.DefaultTopic)
		}
	}

	log.Println("[config] ✅ broker initialization complete")
	return br
}
