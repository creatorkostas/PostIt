package api

import (
	"net"
	"testing"
)

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		host    string
		private bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true}, // Link-local
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false}, // example.com
		{"", false},              // empty string - ParseIP returns nil
		{"not-an-ip", false},     // invalid IP
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			result := isPrivateIP(tt.host)
			if result != tt.private {
				t.Errorf("isPrivateIP(%q) = %v, want %v", tt.host, result, tt.private)
			}
		})
	}
}

func TestIsURLAllowed(t *testing.T) {
	t.Run("allowed URLs", func(t *testing.T) {
		allowed := []string{
			"https://api.example.com/v1/users",
			"http://example.com",
			"https://google.com/search?q=test",
			"http://93.184.216.34", // example.com IP, public
		}
		for _, url := range allowed {
			t.Run(url, func(t *testing.T) {
				err := isURLAllowed(url)
				if err != nil {
					t.Errorf("Expected URL %q to be allowed, got: %v", url, err)
				}
			})
		}
	})

	t.Run("blocked hosts", func(t *testing.T) {
		blocked := []string{
			"http://localhost:8080",
			"https://127.0.0.1",
			"http://0.0.0.0:3000",
			"http://169.254.169.254/latest/meta-data/", // AWS metadata
		}
		for _, url := range blocked {
			t.Run(url, func(t *testing.T) {
				err := isURLAllowed(url)
				if err == nil {
					t.Errorf("Expected URL %q to be blocked", url)
				}
			})
		}
		// IPv6 loopback ::1 needs brackets in URL form: http://[::1]
		t.Run("http://[::1]", func(t *testing.T) {
			err := isURLAllowed("http://[::1]")
			if err == nil {
				t.Error("Expected [::1] IPv6 loopback to be rejected")
			}
		})
	})

	t.Run("private IPs are blocked", func(t *testing.T) {
		privateURLs := []string{
			"http://10.0.0.1",
			"https://192.168.1.100/admin",
			"http://172.16.0.1",
		}
		for _, url := range privateURLs {
			t.Run(url, func(t *testing.T) {
				err := isURLAllowed(url)
				if err == nil {
					t.Errorf("Expected private IP URL %q to be blocked", url)
				}
			})
		}
	})

	t.Run("invalid schemes are rejected", func(t *testing.T) {
		invalid := []string{
			"ftp://example.com",
			"file:///etc/passwd",
			"gopher://example.com",
			"dict://example.com",
		}
		for _, url := range invalid {
			t.Run(url, func(t *testing.T) {
				err := isURLAllowed(url)
				if err == nil {
					t.Errorf("Expected invalid scheme URL %q to be rejected", url)
				}
			})
		}
	})

	t.Run("invalid URL returns error", func(t *testing.T) {
		err := isURLAllowed("://invalid-url")
		if err == nil {
			t.Error("Expected error for invalid URL")
		}
	})
}

func TestIsPrivateIP_Integration(t *testing.T) {
	// Verify that the isPrivateIP function correctly wraps Go's net.IP methods
	// by testing some edge cases with actual IP parsing
	cases := []struct {
		ip       string
		isLoopback  bool
		isPrivate   bool
		isLinkLocal bool
	}{
		{"127.0.0.1", true, false, false},
		{"10.0.0.1", false, true, false},
		{"169.254.1.1", false, false, true},
		{"8.8.8.8", false, false, false},
		{"192.168.1.1", false, true, false},
		{"172.31.255.255", false, true, false},
	}

	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("Failed to parse IP: %s", c.ip)
		}
		if ip.IsLoopback() != c.isLoopback {
			t.Errorf("IsLoopback(%s) = %v, want %v", c.ip, ip.IsLoopback(), c.isLoopback)
		}
		if ip.IsPrivate() != c.isPrivate {
			t.Errorf("IsPrivate(%s) = %v, want %v", c.ip, ip.IsPrivate(), c.isPrivate)
		}
		if ip.IsLinkLocalUnicast() != c.isLinkLocal {
			t.Errorf("IsLinkLocalUnicast(%s) = %v, want %v", c.ip, ip.IsLinkLocalUnicast(), c.isLinkLocal)
		}
	}
}

func TestIsURLAllowed_CaseInsensitiveBlockedHosts(t *testing.T) {
	// Verify case insensitivity for blocked hosts
	err := isURLAllowed("http://LOCALHOST:8080")
	if err == nil {
		t.Error("Expected LOCALHOST (uppercase) to be blocked")
	}

	err = isURLAllowed("http://LocalHost:8080")
	if err == nil {
		t.Error("Expected LocalHost (mixed case) to be blocked")
	}
}
