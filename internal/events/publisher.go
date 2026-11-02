// Package events contains the Kafka publisher/consumer and event payloads.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/segmentio/kafka-go"
)

// Publisher publishes domain events. Implementations must be safe for concurrent use.
type Publisher interface {
	Publish(ctx context.Context, ev domain.Event) error
	Ping(ctx context.Context) error
	Close() error
}

// KafkaPublisher writes JSON events to a Kafka topic.
type KafkaPublisher struct {
	writer *kafka.Writer
	topic  string
}

// NewKafkaPublisher creates a synchronous, all-replica-acknowledged writer.
func NewKafkaPublisher(cfg config.KafkaConfig) (*KafkaPublisher, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("no kafka brokers configured")
	}
	w := &kafka.Writer{
		Addr:                   kafka.TCP(cfg.Brokers...),
		Topic:                  cfg.Topic,
		Balancer:               &kafka.LeastBytes{},
		RequiredAcks:           kafka.RequireAll,
		WriteTimeout:           10 * time.Second,
		ReadTimeout:            10 * time.Second,
		BatchTimeout:           10 * time.Millisecond,
		AllowAutoTopicCreation: true,
	}
	return &KafkaPublisher{writer: w, topic: cfg.Topic}, nil
}

// Publish serialises an event and writes it keyed by aggregate id.
func (p *KafkaPublisher) Publish(ctx context.Context, ev domain.Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		metrics.KafkaEvents.WithLabelValues("publish", p.topic, "error").Inc()
		return fmt.Errorf("marshal event: %w", err)
	}
	msg := kafka.Message{
		Key:   []byte(ev.AggregateID),
		Value: body,
		Time:  ev.OccurredAt,
		Headers: []kafka.Header{
			{Key: "event_id", Value: []byte(ev.ID.String())},
			{Key: "event_type", Value: []byte(ev.Type)},
			{Key: "content-type", Value: []byte("application/json")},
		},
	}
	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		metrics.KafkaEvents.WithLabelValues("publish", p.topic, "error").Inc()
		return fmt.Errorf("publish event: %w", err)
	}
	metrics.KafkaEvents.WithLabelValues("publish", p.topic, "ok").Inc()
	return nil
}

// Ping verifies that the first broker accepts TCP connections.
func (p *KafkaPublisher) Ping(ctx context.Context) error {
	addr := p.writer.Addr.String()
	conn, err := kafka.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// Close flushes buffered messages and closes the writer.
func (p *KafkaPublisher) Close() error { return p.writer.Close() }

// NoopPublisher is used when Kafka is disabled (local unit tests, minimal dev).
type NoopPublisher struct{}

// Publish drops the event.
func (NoopPublisher) Publish(context.Context, domain.Event) error { return nil }

// Ping always succeeds.
func (NoopPublisher) Ping(context.Context) error { return nil }

// Close does nothing.
func (NoopPublisher) Close() error { return nil }

// NewPublisher returns a Kafka publisher or a no-op publisher when disabled.
func NewPublisher(cfg config.KafkaConfig) (Publisher, error) {
	if !cfg.Enabled {
		return NoopPublisher{}, nil
	}
	return NewKafkaPublisher(cfg)
}
