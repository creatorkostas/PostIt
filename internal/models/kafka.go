package models

// KafkaConnection stores a saved Kafka connection configuration.
type KafkaConnection struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Config   KafkaConfig      `json:"config"`
}

// KafkaConfig is the full connection configuration for a Kafka producer.
type KafkaConfig struct {
	Brokers       []string     `json:"brokers"`
	ClientID      string       `json:"clientId"`
	TLS           TLSConfig    `json:"tls"`
	SASL          *SASLConfig  `json:"sasl,omitempty"`
	Compression   string       `json:"compression"` // none, gzip, snappy, lz4, zstd
	RequiredAcks  string       `json:"requiredAcks"` // 0, 1, all
	BatchSize     int          `json:"batchSize"`    // messages
	BatchTimeoutMs int         `json:"batchTimeoutMs"`
	TimeoutSec    int          `json:"timeoutSec"`
}

// TLSConfig holds TLS settings for Kafka connections.
type TLSConfig struct {
	Enabled           bool   `json:"enabled"`
	InsecureSkipVerify bool  `json:"insecureSkipVerify"`
}

// SASLConfig holds SASL authentication settings.
type SASLConfig struct {
	Mechanism string `json:"mechanism"` // plain, scram-sha-256, scram-sha-512
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
}

// KafkaMessage is a message to send to a Kafka topic.
type KafkaMessage struct {
	Topic     string            `json:"topic"`
	Key       string            `json:"key"`
	Value     string            `json:"value"`
	Headers   map[string]string `json:"headers"`
	Partition int               `json:"partition"` // -1 for automatic
}

// KafkaResult is the result of sending a message.
type KafkaResult struct {
	Topic     string `json:"topic"`
	Partition int    `json:"partition"`
	Offset    int64  `json:"offset"`
	Error     string `json:"error,omitempty"`
}

// TopicPartition holds partition metadata for a topic.
type TopicPartition struct {
	Topic      string `json:"topic"`
	Partition  int    `json:"partition"`
	Leader     int    `json:"leader"`
	Replicas   []int  `json:"replicas"`
	ISR        []int  `json:"isr"`
}
