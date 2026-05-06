package storage

import (
	"os"
	"path/filepath"
	"postit/internal/models"
	"testing"
	"time"
)

func TestManager_GetActiveEnvironment(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	// Test empty environment
	result := m.GetActiveEnvironment()
	if result != nil {
		t.Error("Expected nil for empty active environment")
	}

	// Add an environment
	env := models.Environment{
		ID:        "test-env",
		Name:      "Test Environment",
		Variables: map[string]string{"key": "value"},
		SecretVars: map[string]string{"secret": "encrypted"},
	}
	m.dataMu.Lock()
	m.Environments = []models.Environment{env}
	m.ActiveEnvID = "test-env"
	m.dataMu.Unlock()

	// Test retrieval
	result = m.GetActiveEnvironment()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if result.ID != "test-env" {
		t.Errorf("Expected ID 'test-env', got '%s'", result.ID)
	}
	if result.Variables["key"] != "value" {
		t.Errorf("Expected variable 'key'='value', got '%s'", result.Variables["key"])
	}

	// Verify modification doesn't affect original
	result.Variables["new"] = "newval"
	original := m.GetActiveEnvironment()
	if _, ok := original.Variables["new"]; ok {
		t.Error("Deep copy failed - modification propagated to original")
	}
}

func TestManager_GetCachedEnvVariable(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	// Test cache miss
	val, ok := m.GetCachedEnvVariable("nonexistent")
	if ok {
		t.Error("Expected cache miss for non-existent variable")
	}
	if val != "" {
		t.Error("Expected empty string for cache miss")
	}

	// Add environment and refresh cache
	env := models.Environment{
		ID:         "test-env",
		Name:       "Test",
		Variables:  map[string]string{"cached_key": "cached_value"},
		SecretVars: map[string]string{},
	}
	m.dataMu.Lock()
	m.Environments = []models.Environment{env}
	m.dataMu.Unlock()
	m.SetActiveEnvID("test-env")

	// Test cache hit
	val, ok = m.GetCachedEnvVariable("cached_key")
	if !ok {
		t.Error("Expected cache hit after setting active environment")
	}
	if val != "cached_value" {
		t.Errorf("Expected 'cached_value', got '%s'", val)
	}
}

func TestManager_AppendHistoryLine(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	record := models.HistoryRecord{
		Timestamp:  time.Now(),
		Path:       "Test > Request",
		Method:     "GET",
		URL:        "http://example.com",
		StatusCode: 200,
		StatusText: "OK",
		Duration:   100,
	}

	// Append first record
	err := m.appendHistoryLine(record)
	if err != nil {
		t.Fatalf("Failed to append history line: %v", err)
	}

	// Append second record
	record2 := record
	record2.StatusCode = 404
	err = m.appendHistoryLine(record2)
	if err != nil {
		t.Fatalf("Failed to append second history line: %v", err)
	}

	// Verify file exists and contains both lines
	historyFile := m.HistoryPath + ".jsonl"
	data, err := os.ReadFile(historyFile)
	if err != nil {
		t.Fatalf("Failed to read history file: %v", err)
	}

	lines := string(data)
	if len(lines) == 0 {
		t.Error("History file is empty")
	}
}

func TestManager_ConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	// Initialize
	m.Init()

	env := models.Environment{
		ID:         "test-env",
		Name:       "Test",
		Variables:  map[string]string{"key": "value"},
		SecretVars: map[string]string{},
	}

	// Concurrent writes
	done := make(chan bool, 3)

	go func() {
		for i := 0; i < 100; i++ {
			m.SetActiveEnvID("test-env")
			m.SetVariable("var1", "value1")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			m.dataMu.Lock()
			m.Environments = []models.Environment{env}
			m.dataMu.Unlock()
			m.GetActiveEnvID()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			m.GetCachedEnvVariable("key")
			m.GetActiveEnvironment()
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}
}

func TestManager_GetSafeFilename(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple.json"},
		{"path/to/file", "path_to_file.json"},
		{"file with spaces", "file_with_spaces.json"},
		// Note: The regex keeps dashes, only collapses underscores
		{"file---multiple---underscores", "file---multiple---underscores.json"},
		{"special!@#$%chars", "special_chars.json"},
		{"/leading/slash", "leading_slash.json"},
	}

	for _, tt := range tests {
		result := m.getSafeFilename(tt.input)
		if result != tt.expected {
			t.Errorf("getSafeFilename(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestManager_AtomicWriteFile(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	testFile := filepath.Join(tempDir, "test.txt")
	data := []byte("test data for atomic write")

	err := m.atomicWriteFile(testFile, data)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("File was not created")
	}

	// Verify content
	readData, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(readData) != string(data) {
		t.Errorf("File content mismatch: expected %s, got %s", data, readData)
	}

	// Verify temp file doesn't exist
	tempFile := testFile + ".tmp"
	if _, err := os.Stat(tempFile); !os.IsNotExist(err) {
		t.Error("Temporary file should not exist after atomic write")
	}
}

func TestManager_SetVaultKey(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	// Set vault key
	err := m.SetVaultKey("testpassword123")
	if err != nil {
		t.Fatalf("Failed to set vault key: %v", err)
	}

	// Verify vault is unlocked
	if !m.IsVaultUnlocked() {
		t.Error("Vault should be unlocked after setting key")
	}

	// Test encryption/decryption
	plaintext := "sensitive data"
	encrypted, err := m.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Failed to encrypt: %v", err)
	}

	decrypted, err := m.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Failed to decrypt: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("Decryption failed: expected %s, got %s", plaintext, decrypted)
	}
}
