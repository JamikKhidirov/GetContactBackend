package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/segmentio/kafka-go"
)

// Handler processes one event; returning an error routes it to the DLQ.
type Handler func(ctx context.Context, ev domain.Event) error

// Consumer is a Kafka consumer-group reader with manual commits and a DLQ.
type Consumer struct {
	reader    *kafka.Reader
	publisher Publisher
	dlqTopic  string
	log       *slog.Logger
	topic     string
}

// NewConsumer builds a consumer group reader.
func NewConsumer(cfg config.KafkaConfig, publisher Publisher, log *slog.Logger) (*Consumer, error) {
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("no kafka brokers configured")
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        cfg.Brokers,
		GroupID:        cfg.GroupID,
		Topic:          cfg.Topic,
		MinBytes:       1,
		MaxBytes:       10 << 20,
		MaxWait:        500 * time.Millisecond,
		CommitInterval: 0, // manual commits only
		StartOffset:    kafka.FirstOffset,
	})
	return &Consumer{reader: r, publisher: publisher, dlqTopic: cfg.DLQ, log: log, topic: cfg.Topic}, nil
}

// Run blocks until the context is cancelled, processing events sequentially.
func (c *Consumer) Run(ctx context.Context, handle Handler) error {
	c.log.Info("kafka consumer started", "topic", c.topic, "group", c.reader.Config().GroupID)
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("fetch message: %w", err)
		}

		var ev domain.Event
		if err := json.Unmarshal(msg.Value, &ev); err != nil {
			c.log.Error("malformed event", "error", err, "offset", msg.Offset)
			c.deadLetter(ctx, msg, err)
			c.commit(ctx, msg)
			continue
		}
		if ev.ID.String() == "00000000-0000-0000-0000-000000000000" || ev.Type == "" {
			err := errors.New("invalid event envelope")
			c.log.Error("invalid event envelope", "error", err, "offset", msg.Offset)
			c.deadLetter(ctx, msg, err)
			c.commit(ctx, msg)
			continue
		}

		start := time.Now()
		if err := handle(ctx, ev); err != nil {
			metrics.KafkaEvents.WithLabelValues("consume", c.topic, "error").Inc()
			c.log.Error("event handling failed", "event_id", ev.ID, "type", ev.Type, "error", err)
			c.deadLetter(ctx, msg, err)
		} else {
			metrics.KafkaEvents.WithLabelValues("consume", c.topic, "ok").Inc()
			c.log.Debug("event handled", "event_id", ev.ID, "type", ev.Type,
				"duration_ms", time.Since(start).Milliseconds())
		}
		c.commit(ctx, msg)
	}
}

func (c *Consumer) commit(ctx context.Context, msg kafka.Message) {
	if err := c.reader.CommitMessages(ctx, msg); err != nil {
		c.log.Error("commit offset failed", "offset", msg.Offset, "error", err)
	}
}

// deadLetter publishes the raw failed message to the DLQ, best-effort.
func (c *Consumer) deadLetter(ctx context.Context, msg kafka.Message, cause error) {
	if c.dlqTopic == "" || c.publisher == nil {
		return
	}
	header := func(key string) string {
		for _, h := range msg.Headers {
			if h.Key == key {
				return string(h.Value)
			}
		}
		return ""
	}
	envelope := map[string]any{
		"original_topic": c.topic,
		"error":          cause.Error(),
		"event_id":       header("event_id"),
		"event_type":     header("event_type"),
		"key":            string(msg.Key),
		"payload":        json.RawMessage(msg.Value),
		"failed_at":      time.Now().UTC(),
	}
	body, _ := json.Marshal(envelope)
	w := &kafka.Writer{Addr: kafka.TCP(c.reader.Config().Brokers...), Topic: c.dlqTopic,
		RequiredAcks: kafka.RequireAll, WriteTimeout: 5 * time.Second, AllowAutoTopicCreation: true}
	defer w.Close()
	if err := w.WriteMessages(ctx, kafka.Message{Value: body}); err != nil {
		c.log.Error("failed to publish to DLQ", "error", err)
		metrics.KafkaEvents.WithLabelValues("publish", c.dlqTopic, "error").Inc()
		return
	}
	metrics.KafkaEvents.WithLabelValues("publish", c.dlqTopic, "ok").Inc()
}

// Close stops the consumer.
func (c *Consumer) Close() error { return c.reader.Close() }

// Stats exposes reader statistics for logs.
func (c *Consumer) Stats() kafka.ReaderStats { return c.reader.Stats() }
