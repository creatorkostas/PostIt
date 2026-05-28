package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"postit/internal/models"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
)

// compressionMap maps config strings to kafka-go compression values.
// "none" is the default (zero value), so it's omitted from the map.
var compressionMap = map[string]kafka.Compression{
	"gzip":   kafka.Gzip,
	"snappy": kafka.Snappy,
	"lz4":    kafka.Lz4,
	"zstd":   kafka.Zstd,
}

// KafkaConfigDefaults returns a KafkaConfig with sensible defaults.
func KafkaConfigDefaults() models.KafkaConfig {
	return models.KafkaConfig{
		ClientID:       "postit",
		Compression:    "none",
		RequiredAcks:   "1",
		BatchSize:      1,
		BatchTimeoutMs: 10,
		TimeoutSec:     10,
	}
}

// KafkaProducer manages a connection to a Kafka cluster for producing messages.
// It follows the same lifecycle pattern as WSClient in websocket.go.
type KafkaProducer struct {
	writer    *kafka.Writer
	transport *kafka.Transport
	connected bool
	mu        sync.Mutex
}

// NewKafkaProducer creates a new unconnected KafkaProducer.
func NewKafkaProducer() *KafkaProducer {
	return &KafkaProducer{}
}

// Connect establishes a connection to the Kafka cluster and creates a Writer.
// The writer is configured with per-message topic support so different topics
// can be targeted without reconnecting.
func (kp *KafkaProducer) Connect(cfg models.KafkaConfig) error {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	if kp.connected {
		kp.closeWriter()
	}

	transport, err := buildTransport(cfg)
	if err != nil {
		return fmt.Errorf("failed to build transport: %w", err)
	}
	kp.transport = transport

	acks, err := parseRequiredAcks(cfg.RequiredAcks)
	if err != nil {
		return fmt.Errorf("invalid requiredAcks: %w", err)
	}

	// Default compression is None (zero value) if not recognized
	compression := compressionMap[cfg.Compression]

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 1
	}

	batchTimeout := time.Duration(cfg.BatchTimeoutMs) * time.Millisecond
	if batchTimeout <= 0 {
		batchTimeout = 10 * time.Millisecond
	}

	clientID := cfg.ClientID
	if clientID == "" {
		clientID = "postit"
	}

	// Build broker addresses using kafka.TCP helper
	addr := kafka.TCP(cfg.Brokers...)

	kp.writer = &kafka.Writer{
		Addr:          addr,
		Transport:     kp.transport,
		Compression:   compression,
		RequiredAcks:  acks,
		BatchSize:     batchSize,
		BatchTimeout:  batchTimeout,
		WriteTimeout:  timeout,
		ReadTimeout:   timeout,
		Balancer:      &kafka.Hash{}, // consistent partition by key
		Async:         false,          // synchronous by default for clear results
	}

	kp.connected = true
	return nil
}

// SendMessage sends a single message to a Kafka topic.
// If partition is -1, the balancer (Hash) determines the partition based on the key.
func (kp *KafkaProducer) SendMessage(ctx context.Context, msg models.KafkaMessage) (*models.KafkaResult, error) {
	kp.mu.Lock()
	writer := kp.writer
	connected := kp.connected
	kp.mu.Unlock()

	if !connected || writer == nil {
		return nil, fmt.Errorf("kafka not connected, call Connect first")
	}

	kmsg := kafka.Message{
		Topic: msg.Topic,
		Key:   []byte(msg.Key),
		Value: []byte(msg.Value),
	}

	// Add headers
	for k, v := range msg.Headers {
		kmsg.Headers = append(kmsg.Headers, kafka.Header{
			Key:   k,
			Value: []byte(v),
		})
	}

	// Set explicit partition if provided (non-negative)
	if msg.Partition >= 0 {
		kmsg.Partition = msg.Partition
	}

	err := writer.WriteMessages(ctx, kmsg)
	if err != nil {
		return nil, fmt.Errorf("failed to write message: %w", err)
	}

	return &models.KafkaResult{
		Topic:     msg.Topic,
		Partition: kmsg.Partition,
		Offset:    kmsg.Offset,
	}, nil
}

// TestConnection verifies that the brokers are reachable with the given config.
// It does not store the connection; use Connect for that.
func (kp *KafkaProducer) TestConnection(ctx context.Context, cfg models.KafkaConfig) error {
	if len(cfg.Brokers) == 0 {
		return fmt.Errorf("at least one broker is required")
	}

	dialer, err := buildDialer(cfg)
	if err != nil {
		return fmt.Errorf("failed to build dialer: %w", err)
	}

	// Try dialing the first broker
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Brokers[0])
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", cfg.Brokers[0], err)
	}
	conn.Close()
	return nil
}

// GetTopics lists all available topics from the Kafka cluster.
// Uses a fresh connection since the writer may be topic-scoped.
func (kp *KafkaProducer) GetTopics(ctx context.Context, cfg models.KafkaConfig) ([]string, error) {
	dialer, err := buildDialer(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build dialer: %w", err)
	}

	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("at least one broker is required")
	}

	conn, err := dialer.DialContext(ctx, "tcp", cfg.Brokers[0])
	if err != nil {
		return nil, fmt.Errorf("failed to dial broker: %w", err)
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions()
	if err != nil {
		return nil, fmt.Errorf("failed to read partitions: %w", err)
	}

	topicSet := make(map[string]struct{})
	for _, p := range partitions {
		topicSet[p.Topic] = struct{}{}
	}

	topics := make([]string, 0, len(topicSet))
	for t := range topicSet {
		topics = append(topics, t)
	}
	return topics, nil
}

// GetTopicMetadata returns partition metadata for a specific topic.
func (kp *KafkaProducer) GetTopicMetadata(ctx context.Context, cfg models.KafkaConfig, topic string) ([]models.TopicPartition, error) {
	dialer, err := buildDialer(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build dialer: %w", err)
	}

	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("at least one broker is required")
	}

	conn, err := dialer.DialContext(ctx, "tcp", cfg.Brokers[0])
	if err != nil {
		return nil, fmt.Errorf("failed to dial broker: %w", err)
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions()
	if err != nil {
		return nil, fmt.Errorf("failed to read partitions: %w", err)
	}

	var result []models.TopicPartition
	for _, p := range partitions {
		if p.Topic == topic {
			replicas := make([]int, len(p.Replicas))
		for i, b := range p.Replicas {
			replicas[i] = b.ID
		}
		isr := make([]int, len(p.Isr))
		for i, b := range p.Isr {
			isr[i] = b.ID
		}
		result = append(result, models.TopicPartition{
			Topic:     p.Topic,
			Partition: p.ID,
			Leader:    p.Leader.ID,
			Replicas:  replicas,
			ISR:       isr,
		})
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("topic %q not found", topic)
	}
	return result, nil
}

// IsConnected returns whether the producer is currently connected.
func (kp *KafkaProducer) IsConnected() bool {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	return kp.connected
}

// Close gracefully shuts down the Kafka writer and releases resources.
func (kp *KafkaProducer) Close() {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.closeWriter()
}

// closeWriter is the internal, lock-free close.
func (kp *KafkaProducer) closeWriter() {
	if kp.writer != nil {
		_ = kp.writer.Close()
		kp.writer = nil
	}
	if kp.transport != nil {
		kp.transport = nil
	}
	kp.connected = false
}

// --- Transport / Dialer helpers ---

// buildTransport creates a kafka.Transport configured with TLS and/or SASL.
// Transport is the modern kafka-go way to configure shared auth for Writer.
func buildTransport(cfg models.KafkaConfig) (*kafka.Transport, error) {
	t := &kafka.Transport{
		ClientID: cfg.ClientID,
	}

	if cfg.TLS.Enabled {
		t.TLS = &tls.Config{
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		}
	}

	if cfg.SASL != nil {
		mechanism, err := buildSASLMechanism(cfg.SASL)
		if err != nil {
			return nil, err
		}
		t.SASL = mechanism
	}

	return t, nil
}

// buildDialer creates a kafka.Dialer configured with TLS and/or SASL.
// Dialer is used for one-off connections (topic listing, metadata, test).
func buildDialer(cfg models.KafkaConfig) (*kafka.Dialer, error) {
	d := &kafka.Dialer{
		Timeout:   time.Duration(cfg.TimeoutSec) * time.Second,
		DualStack: true,
		ClientID:  cfg.ClientID,
	}

	if cfg.TLS.Enabled {
		d.TLS = &tls.Config{
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		}
	}

	if cfg.SASL != nil {
		mechanism, err := buildSASLMechanism(cfg.SASL)
		if err != nil {
			return nil, err
		}
		d.SASLMechanism = mechanism
	}

	return d, nil
}

// buildSASLMechanism returns the appropriate SASL mechanism based on config.
func buildSASLMechanism(saslCfg *models.SASLConfig) (sasl.Mechanism, error) {
	switch saslCfg.Mechanism {
	case "plain":
		return plain.Mechanism{
			Username: saslCfg.Username,
			Password: saslCfg.Password,
		}, nil

	case "scram-sha-256":
		return scram.Mechanism(scram.SHA256, saslCfg.Username, saslCfg.Password)

	case "scram-sha-512":
		return scram.Mechanism(scram.SHA512, saslCfg.Username, saslCfg.Password)

	default:
		return nil, fmt.Errorf("unsupported SASL mechanism: %q (supported: plain, scram-sha-256, scram-sha-512)", saslCfg.Mechanism)
	}
}

// parseRequiredAcks converts a string like "0", "1", "all" to kafka.RequiredAcks.
func parseRequiredAcks(s string) (kafka.RequiredAcks, error) {
	switch s {
	case "0":
		return kafka.RequireNone, nil
	case "1":
		return kafka.RequireOne, nil
	case "all", "-1":
		return kafka.RequireAll, nil
	default:
		return 0, fmt.Errorf("must be one of: 0, 1, all")
	}
}
