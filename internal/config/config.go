// Package config loads typed configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the full runtime configuration of both the API and the worker.
type Config struct {
	Env         string
	ServiceName string
	Version     string

	HTTP  HTTPConfig
	DB    DBConfig
	Redis RedisConfig
	Kafka KafkaConfig
	Auth  AuthConfig
	CORS  CORSConfig
	Rate  RateConfig
	Admin AdminConfig
}

type HTTPConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	RequestTimeout  time.Duration
	MetricsAddr     string
}

type DBConfig struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	CacheTTL time.Duration
}

type KafkaConfig struct {
	Brokers []string
	Topic   string
	DLQ     string
	GroupID string
	Enabled bool
}

type AuthConfig struct {
	JWTSecret       string
	Issuer          string
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	ArgonMemory     uint32
	ArgonIterations uint32
	ArgonThreads    uint8
	ArgonKeyLen     uint32
}

type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
}

type RateConfig struct {
	Enabled   bool
	PublicRPM int
	AuthRPM   int
	Window    time.Duration
}

type AdminConfig struct {
	Phone    string
	Password string
	Name     string
}

// Load reads configuration from the process environment and applies defaults.
func Load() (*Config, error) {
	c := &Config{
		Env:         env("APP_ENV", "development"),
		ServiceName: env("SERVICE_NAME", "getcontact-api"),
		Version:     env("APP_VERSION", "1.0.0"),
		HTTP: HTTPConfig{
			Addr:            env("HTTP_ADDR", ":8080"),
			ReadTimeout:     envDuration("HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:    envDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:     envDuration("HTTP_IDLE_TIMEOUT", 90*time.Second),
			ShutdownTimeout: envDuration("HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
			RequestTimeout:  envDuration("HTTP_REQUEST_TIMEOUT", 20*time.Second),
			MetricsAddr:     env("METRICS_ADDR", ":8081"),
		},
		DB: DBConfig{
			DSN:             env("DATABASE_URL", "postgres://getcontact:getcontact@localhost:5432/getcontact?sslmode=disable"),
			MaxConns:        int32(envInt("DB_MAX_CONNS", 20)),
			MinConns:        int32(envInt("DB_MIN_CONNS", 2)),
			MaxConnLifetime: envDuration("DB_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime: envDuration("DB_MAX_CONN_IDLE_TIME", 15*time.Minute),
			ConnectTimeout:  envDuration("DB_CONNECT_TIMEOUT", 10*time.Second),
		},
		Redis: RedisConfig{
			Addr:     env("REDIS_ADDR", "localhost:6379"),
			Password: env("REDIS_PASSWORD", ""),
			DB:       envInt("REDIS_DB", 0),
			CacheTTL: envDuration("REDIS_CACHE_TTL", 60*time.Second),
		},
		Kafka: KafkaConfig{
			Brokers: split(env("KAFKA_BROKERS", "localhost:9092")),
			Topic:   env("KAFKA_TOPIC", "getcontact.events"),
			DLQ:     env("KAFKA_DLQ_TOPIC", "getcontact.events.dlq"),
			GroupID: env("KAFKA_GROUP_ID", "getcontact-workers"),
			Enabled: envBool("KAFKA_ENABLED", true),
		},
		Auth: AuthConfig{
			JWTSecret:       env("JWT_SECRET", "change-me-in-production-at-least-32-chars"),
			Issuer:          env("JWT_ISSUER", "getcontact"),
			AccessTTL:       envDuration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL:      envDuration("JWT_REFRESH_TTL", 30*24*time.Hour),
			ArgonMemory:     uint32(envInt("ARGON2_MEMORY_KB", 64*1024)),
			ArgonIterations: uint32(envInt("ARGON2_ITERATIONS", 3)),
			ArgonThreads:    uint8(envInt("ARGON2_THREADS", 2)),
			ArgonKeyLen:     uint32(envInt("ARGON2_KEY_LEN", 32)),
		},
		CORS: CORSConfig{
			AllowedOrigins: split(env("CORS_ALLOWED_ORIGINS", "*")),
			AllowedMethods: split(env("CORS_ALLOWED_METHODS", "GET,POST,PATCH,PUT,DELETE,OPTIONS")),
			AllowedHeaders: split(env("CORS_ALLOWED_HEADERS", "Accept,Authorization,Content-Type,X-Request-Id")),
		},
		Rate: RateConfig{
			Enabled:   envBool("RATE_LIMIT_ENABLED", true),
			PublicRPM: envInt("RATE_LIMIT_PUBLIC_RPM", 120),
			AuthRPM:   envInt("RATE_LIMIT_AUTH_RPM", 20),
			Window:    envDuration("RATE_LIMIT_WINDOW", time.Minute),
		},
		Admin: AdminConfig{
			Phone:    env("ADMIN_PHONE", ""),
			Password: env("ADMIN_PASSWORD", ""),
			Name:     env("ADMIN_NAME", "Platform Admin"),
		},
	}

	if len(c.Auth.JWTSecret) < 32 && c.Env == "production" {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters in production")
	}
	if c.DB.MaxConns < c.DB.MinConns {
		return nil, fmt.Errorf("DB_MAX_CONNS must be >= DB_MIN_CONNS")
	}
	return c, nil
}

// IsProduction reports whether the service runs with production hardening.
func (c *Config) IsProduction() bool { return strings.EqualFold(c.Env, "production") }

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil {
		return fallback
	}
	return v
}

func envBool(key string, fallback bool) bool {
	v, err := strconv.ParseBool(env(key, strconv.FormatBool(fallback)))
	if err != nil {
		return fallback
	}
	return v
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v, err := time.ParseDuration(env(key, fallback.String()))
	if err != nil {
		return fallback
	}
	return v
}

func split(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
