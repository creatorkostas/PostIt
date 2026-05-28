package api

import (
	"context"
	"postit/internal/models"
	"testing"
)

func TestKafkaConfigDefaults(t *testing.T) {
	cfg := KafkaConfigDefaults()

	tests := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"ClientID", cfg.ClientID, "postit"},
		{"Compression", cfg.Compression, "none"},
		{"RequiredAcks", cfg.RequiredAcks, "1"},
		{"BatchSize", cfg.BatchSize, 1},
		{"BatchTimeoutMs", cfg.BatchTimeoutMs, 10},
		{"TimeoutSec", cfg.TimeoutSec, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestKafkaProducer_Lifecycle(t *testing.T) {
	kp := NewKafkaProducer()

	// Initial state: not connected
	if kp.IsConnected() {
		t.Error("expected producer to be not connected initially")
	}

	// Close on unconnected producer should not panic
	kp.Close()

	if kp.IsConnected() {
		t.Error("expected producer to be not connected after close")
	}

	// SendMessage without connecting
	_, err := kp.SendMessage(context.Background(), models.KafkaMessage{
		Topic: "test",
		Value: "hello",
	})
	if err == nil {
		t.Error("expected error when sending without connecting")
	}
}

func TestParseRequiredAcks(t *testing.T) {
	tests := []struct {
		input    string
		wantAcks int
		wantErr  bool
	}{
		{"0", 0, false},
		{"1", 1, false},
		{"all", -1, false},
		{"-1", -1, false},
		{"invalid", 0, true},
		{"2", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			acks, err := parseRequiredAcks(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if int(acks) != tt.wantAcks {
				t.Errorf("got %d, want %d", acks, tt.wantAcks)
			}
		})
	}
}

func TestBuildSASLMechanism(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *models.SASLConfig
		wantErr bool
	}{
		{
			name: "plain",
			cfg: &models.SASLConfig{
				Mechanism: "plain",
				Username:  "user",
				Password:  "pass",
			},
			wantErr: false,
		},
		{
			name: "scram-sha-256",
			cfg: &models.SASLConfig{
				Mechanism: "scram-sha-256",
				Username:  "user",
				Password:  "pass",
			},
			wantErr: false,
		},
		{
			name: "scram-sha-512",
			cfg: &models.SASLConfig{
				Mechanism: "scram-sha-512",
				Username:  "user",
				Password:  "pass",
			},
			wantErr: false,
		},
		{
			name: "unsupported",
			cfg: &models.SASLConfig{
				Mechanism: "gssapi",
				Username:  "user",
				Password:  "pass",
			},
			wantErr: true,
		},
		{
			name: "empty mechanism",
			cfg: &models.SASLConfig{
				Mechanism: "",
				Username:  "user",
				Password:  "pass",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := buildSASLMechanism(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if m == nil {
				t.Error("expected mechanism, got nil")
			}
		})
	}
}

func TestBuildDialer_NoAuth(t *testing.T) {
	cfg := KafkaConfigDefaults()
	cfg.Brokers = []string{"localhost:9092"}

	dialer, err := buildDialer(cfg)
	if err != nil {
		t.Fatalf("unexpected error building dialer: %v", err)
	}
	if dialer == nil {
		t.Fatal("expected dialer, got nil")
	}
	if dialer.SASLMechanism != nil {
		t.Error("expected nil SASL mechanism for unauthenticated dialer")
	}
	if dialer.TLS != nil {
		t.Error("expected nil TLS config when TLS not enabled")
	}
}

func TestBuildDialer_WithTLS(t *testing.T) {
	cfg := KafkaConfigDefaults()
	cfg.Brokers = []string{"localhost:9093"}
	cfg.TLS = models.TLSConfig{
		Enabled:           true,
		InsecureSkipVerify: true,
	}

	dialer, err := buildDialer(cfg)
	if err != nil {
		t.Fatalf("unexpected error building dialer: %v", err)
	}
	if dialer.TLS == nil {
		t.Fatal("expected TLS config")
	}
	if !dialer.TLS.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify to be true")
	}
}

func TestBuildDialer_WithSASL(t *testing.T) {
	cfg := KafkaConfigDefaults()
	cfg.Brokers = []string{"localhost:9092"}
	cfg.SASL = &models.SASLConfig{
		Mechanism: "plain",
		Username:  "testuser",
		Password:  "testpass",
	}

	dialer, err := buildDialer(cfg)
	if err != nil {
		t.Fatalf("unexpected error building dialer: %v", err)
	}
	if dialer.SASLMechanism == nil {
		t.Error("expected SASL mechanism")
	}
}

func TestBuildTransport(t *testing.T) {
	t.Run("no auth", func(t *testing.T) {
		cfg := KafkaConfigDefaults()
		tr, err := buildTransport(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tr == nil {
			t.Fatal("expected transport")
		}
		if tr.SASL != nil {
			t.Error("expected nil SASL for no auth")
		}
	})

	t.Run("with SASL", func(t *testing.T) {
		cfg := KafkaConfigDefaults()
		cfg.SASL = &models.SASLConfig{
			Mechanism: "scram-sha-512",
			Username:  "user",
			Password:  "pass",
		}
		tr, err := buildTransport(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tr.SASL == nil {
			t.Error("expected SASL mechanism on transport")
		}
	})

	t.Run("with TLS", func(t *testing.T) {
		cfg := KafkaConfigDefaults()
		cfg.TLS = models.TLSConfig{Enabled: true}
		tr, err := buildTransport(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tr.TLS == nil {
			t.Error("expected TLS config on transport")
		}
	})
}

func TestTestConnection_NoBrokers(t *testing.T) {
	kp := NewKafkaProducer()
	cfg := KafkaConfigDefaults()
	cfg.Brokers = []string{} // empty

	err := kp.TestConnection(context.Background(), cfg)
	if err == nil {
		t.Error("expected error for empty brokers")
	}
}

func TestGetTopics_NoBrokers(t *testing.T) {
	kp := NewKafkaProducer()
	cfg := KafkaConfigDefaults()

	_, err := kp.GetTopics(context.Background(), cfg)
	if err == nil {
		t.Error("expected error for empty brokers")
	}
}

func TestGetTopicMetadata_NoBrokers(t *testing.T) {
	kp := NewKafkaProducer()
	cfg := KafkaConfigDefaults()

	_, err := kp.GetTopicMetadata(context.Background(), cfg, "test-topic")
	if err == nil {
		t.Error("expected error for empty brokers")
	}
}

func TestSendMessage_EmptyTopic(t *testing.T) {
	// Can't test actual sending without a broker,
	// but we can verify that the validation in the web handler works.
	// The Connect state check is tested separately above.
}
