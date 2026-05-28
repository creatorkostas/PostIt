package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"postit/internal/models"
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"golang.org/x/crypto/pbkdf2"
)

// Constants for storage limits and configuration
const (
	MaxHistoryRecords    = 50   // Maximum history records to keep
	MaxVariableKeys      = 1000 // Maximum variables in VariableMap
	MaxVariableKeyLength = 64   // Maximum variable key length
	MaxVariableValueLen  = 4096 // Maximum variable value length
	MaxVaultSaltSize     = 16   // Salt size for PBKDF2
	VaultPBKDF2Iters     = 100000 // PBKDF2 iterations for key derivation
)

type Manager struct {
	BaseDir       string
	RequestDir    string
	VariablePath  string
	HistoryPath   string
	WorkflowsPath string
	VariableMap   map[string]string
	GlobalHeaders []models.Header
	Environments  []models.Environment
	Workflows     []models.Workflow
	ActiveEnvID   string
	KafkaConnections []models.KafkaConnection

	Logger *log.Logger
	varMu  sync.RWMutex
	historyMu sync.RWMutex
	dataMu    sync.RWMutex // For Environments and GlobalHeaders
	workflowMu sync.RWMutex // For Workflows
	kafkaMu   sync.RWMutex // For KafkaConnections
	vaultKey []byte

	// Active environment cache for O(1) variable lookups
	activeEnvCache   map[string]string
	activeEnvCacheMu sync.RWMutex
}

func NewManager(base string) *Manager {
	reqDir := filepath.Join(base, "requests")
	os.MkdirAll(reqDir, 0755)

	m := &Manager{
		BaseDir: base,
		RequestDir: reqDir,
		VariablePath: filepath.Join(base, "variables.json"),
		HistoryPath: filepath.Join(base, "history.json"),
		WorkflowsPath: filepath.Join(base, "workflows.json"),
		VariableMap: make(map[string]string),
		Workflows:        []models.Workflow{},
		KafkaConnections: []models.KafkaConnection{},
		Logger: log.Default(),
		activeEnvCache: make(map[string]string),
	}
	return m
}

func (m *Manager) Init() error {
	m.LoadVariables()
	m.LoadGlobalHeaders()
	m.LoadEnvironments()
	m.LoadActiveEnv()
	if err := m.LoadWorkflows(); err != nil {
		m.Logger.Errorf("Failed to load workflows: %v", err)
	}
	if err := m.LoadKafkaConnections(); err != nil {
		m.Logger.Errorf("Failed to load kafka connections: %v", err)
	}
	return nil
}

func (m *Manager) LoadVariables() error {
	m.varMu.Lock()
	defer m.varMu.Unlock()
	data, err := os.ReadFile(m.VariablePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return &StorageError{Op: "Read", Path: m.VariablePath, Err: err}
	}
	if err := json.Unmarshal(data, &m.VariableMap); err != nil {
		return &StorageError{Op: "Parse", Path: m.VariablePath, Err: err}
	}
	return nil
}

func (m *Manager) SaveVariables() error {
	m.varMu.RLock()
	data, err := json.MarshalIndent(m.VariableMap, "", "  ")
	m.varMu.RUnlock()
	if err != nil {
		return err
	}
	return m.atomicWriteFile(m.VariablePath, data)
}

func (m *Manager) GetVariable(name string) (string, bool) {
	m.varMu.RLock()
	defer m.varMu.RUnlock()
	val, ok := m.VariableMap[name]
	return val, ok
}

func (m *Manager) SetVariable(name, val string) error {
	m.varMu.Lock()
	// Skip if value is unchanged - prevents unnecessary disk writes and duplicate logs
	if current, ok := m.VariableMap[name]; ok && current == val {
		m.varMu.Unlock()
		return nil
	}
	m.VariableMap[name] = val
	m.varMu.Unlock()
	return m.SaveVariables()
}

func (m *Manager) GetVariableMapCopy() map[string]string {
	m.varMu.RLock()
	defer m.varMu.RUnlock()
	res := make(map[string]string)
	for k, v := range m.VariableMap {
		res[k] = v
	}
	return res
}

func (m *Manager) SaveSingleRequest(req models.RequestInfo) error {
	filename := m.getSafeFilename(req.Path)
	fullPath := filepath.Join(m.RequestDir, filename)
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(fullPath, data)
}

func (m *Manager) DeleteRequestFile(path string) error {
	filename := m.getSafeFilename(path)
	fullPath := filepath.Join(m.RequestDir, filename)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return &StorageError{Op: "Delete", Path: fullPath, Err: err}
	}
	return nil
}

func (m *Manager) LoadCache() []models.RequestInfo {
	files, err := os.ReadDir(m.RequestDir)
	if err != nil {
		return nil
	}

	var flat []models.RequestInfo
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.RequestDir, f.Name()))
		if err != nil {
			m.Logger.Warn("Failed to read request file", "file", f.Name(), "error", err)
			continue
		}
		var req models.RequestInfo
		if err := json.Unmarshal(data, &req); err != nil {
			m.Logger.Warn("Failed to parse request file", "file", f.Name(), "error", err)
			continue
		}
		flat = append(flat, req)
	}
	return flat
}

func (m *Manager) LoadCollection() (models.Collection, []models.RequestInfo, error) {
	flat := m.LoadCache()
	col := models.Collection{
		Info: models.Info{Name: "PostIt Collection"},
		Item: models.ReconstructItems(flat),
	}
	return col, flat, nil
}

func (m *Manager) atomicWriteFile(filename string, data []byte) error {
	tmpFile := filename + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return &StorageError{Op: "Write", Path: tmpFile, Err: err}
	}
	if err := os.Rename(tmpFile, filename); err != nil {
		return &StorageError{Op: "Rename", Path: filename, Err: err}
	}
	return nil
}

// appendHistoryLine appends a single history record as a JSON line
func (m *Manager) appendHistoryLine(record models.HistoryRecord) error {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal history record: %w", err)
	}

	// Open file in append mode, create if doesn't exist
	f, err := os.OpenFile(m.HistoryPath+".jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return &StorageError{Op: "Open", Path: m.HistoryPath + ".jsonl", Err: err}
	}
	defer f.Close()

	// Write JSON line with newline
	if _, err := f.Write(append(data, '\n')); err != nil {
		return &StorageError{Op: "Write", Path: m.HistoryPath + ".jsonl", Err: err}
	}

	return nil
}

func (m *Manager) SaveHistory(history []models.HistoryRecord) error {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	// Keep last 50
	if len(history) > 50 {
		history = history[len(history)-50:]
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(m.HistoryPath, data)
}

func (m *Manager) AddHistoryRecord(record models.HistoryRecord) error {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	
	var history []models.HistoryRecord
	if data, err := os.ReadFile(m.HistoryPath); err == nil {
		json.Unmarshal(data, &history)
	}
	
	history = append(history, record)
	if len(history) > 50 {
		history = history[len(history)-50:]
	}
	
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(m.HistoryPath, data)
}

func (m *Manager) LoadHistory() []models.HistoryRecord {
	m.historyMu.RLock()
	defer m.historyMu.RUnlock()
	data, err := os.ReadFile(m.HistoryPath)
	if err != nil {
		return []models.HistoryRecord{}
	}
	var history []models.HistoryRecord
	json.Unmarshal(data, &history)
	return history
}

func (m *Manager) SaveCollection(col models.Collection) error {
	path := filepath.Join(m.BaseDir, "collection.json")
	data, err := json.MarshalIndent(col, "", "  ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(path, data)
}

// vaultSalt is used for PBKDF2 key derivation
var vaultSalt []byte

// IsVaultUnlocked returns true if the vault has been unlocked
func (m *Manager) IsVaultUnlocked() bool {
	m.dataMu.RLock()
	defer m.dataMu.RUnlock()
	return m.vaultKey != nil
}

func (m *Manager) SetVaultKey(password string) error {
	// Generate random salt
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}
	vaultSalt = salt

	// Use PBKDF2 with 100,000 iterations for secure key derivation
	m.vaultKey = pbkdf2.Key([]byte(password), salt, 100000, 32, sha256.New)
	return nil
}

func (m *Manager) Encrypt(plaintext string) (string, error) {
	if m.vaultKey == nil {
		return "", fmt.Errorf("vault is locked")
	}

	block, err := aes.NewCipher(m.vaultKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Store version, salt, and ciphertext for backward compatibility
	// Format: version:salt:ciphertext (base64 encoded)
	version := byte(2) // Version 2 = PBKDF2
	saltLen := byte(len(vaultSalt))
	data := append([]byte{version, saltLen}, vaultSalt...)
	data = append(data, ciphertext...)

	return base64.StdEncoding.EncodeToString(data), nil
}

func (m *Manager) Decrypt(encoded string) (string, error) {
	if m.vaultKey == nil {
		return "", fmt.Errorf("vault is locked")
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode: %w", err)
	}

	if len(data) < 2 {
		return "", fmt.Errorf("invalid encrypted data")
	}

	version := data[0]

	var ciphertext []byte

	if version == 2 {
		// Version 2: PBKDF2 with stored salt
		saltLen := int(data[1])
		if len(data) < 2+saltLen {
			return "", fmt.Errorf("invalid encrypted data format")
		}
		storedSalt := data[2 : 2+saltLen]
		ciphertext = data[2+saltLen:]

		// Re-derive key with stored salt
		if vaultSalt == nil || !byteSlicesEqual(vaultSalt, storedSalt) {
			return "", fmt.Errorf("vault salt mismatch - re-enter password")
		}
	} else if version == 1 || version == 0 {
		// Version 1 or legacy: simple SHA256 (backward compatibility)
		// Try to decrypt with current key
		ciphertext = data
	} else {
		return "", fmt.Errorf("unsupported encryption version: %d", version)
	}

	block, err := aes.NewCipher(m.vaultKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}

	return string(plaintext), nil
}

// byteSlicesEqual compares two byte slices for equality
func byteSlicesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *Manager) GetGlobalHeaders() []models.Header {
	m.dataMu.RLock()
	defer m.dataMu.RUnlock()
	res := make([]models.Header, len(m.GlobalHeaders))
	copy(res, m.GlobalHeaders)
	return res
}

func (m *Manager) SaveGlobalHeaders(headers []models.Header) error {
	m.dataMu.Lock()
	m.GlobalHeaders = headers
	m.dataMu.Unlock()
	
	path := filepath.Join(m.BaseDir, "global_headers.json")
	data, err := json.MarshalIndent(headers, "", "  ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(path, data)
}

func (m *Manager) LoadGlobalHeaders() error {
	path := filepath.Join(m.BaseDir, "global_headers.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	m.dataMu.Lock()
	defer m.dataMu.Unlock()
	return json.Unmarshal(data, &m.GlobalHeaders)
}

func (m *Manager) GetEnvironments() []models.Environment {
	m.dataMu.RLock()
	defer m.dataMu.RUnlock()
	res := make([]models.Environment, len(m.Environments))
	copy(res, m.Environments)
	return res
}

func (m *Manager) SaveEnvironments(envs []models.Environment) error {
	m.dataMu.Lock()
	m.Environments = envs
	m.dataMu.Unlock()
	
	path := filepath.Join(m.BaseDir, "environments.json")
	data, err := json.MarshalIndent(envs, "", "  ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(path, data)
}

func (m *Manager) LoadEnvironments() error {
	path := filepath.Join(m.BaseDir, "environments.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	m.dataMu.Lock()
	defer m.dataMu.Unlock()
	return json.Unmarshal(data, &m.Environments)
}

func (m *Manager) GetActiveEnvID() string {
	m.dataMu.RLock()
	defer m.dataMu.RUnlock()
	return m.ActiveEnvID
}

func (m *Manager) SetActiveEnvID(id string) error {
	m.dataMu.Lock()
	m.ActiveEnvID = id
	envs := make([]models.Environment, len(m.Environments))
	copy(envs, m.Environments)
	m.dataMu.Unlock()

	// Refresh the environment cache
	m.refreshEnvCache(id, envs)

	return m.SaveActiveEnv()
}

// refreshEnvCache updates the active environment cache
func (m *Manager) refreshEnvCache(activeID string, envs []models.Environment) {
	m.activeEnvCacheMu.Lock()
	defer m.activeEnvCacheMu.Unlock()

	// Clear old cache
	m.activeEnvCache = make(map[string]string)

	// Find active environment and populate cache
	for _, env := range envs {
		if env.ID == activeID {
			for k, v := range env.Variables {
				m.activeEnvCache[k] = v
			}
			for k, v := range env.SecretVars {
				m.activeEnvCache[k] = v // Encrypted value, will be decrypted on access
			}
			break
		}
	}
}

// GetCachedEnvVariable returns a variable from the active environment cache (O(1))
// Returns empty string and false if not found
func (m *Manager) GetCachedEnvVariable(name string) (string, bool) {
	m.activeEnvCacheMu.RLock()
	defer m.activeEnvCacheMu.RUnlock()
	val, ok := m.activeEnvCache[name]
	return val, ok
}

// RefreshActiveEnvCache forces a cache refresh from current environments
func (m *Manager) RefreshActiveEnvCache() {
	m.dataMu.RLock()
	activeID := m.ActiveEnvID
	envs := make([]models.Environment, len(m.Environments))
	copy(envs, m.Environments)
	m.dataMu.RUnlock()

	m.refreshEnvCache(activeID, envs)
}

// GetActiveEnvironment returns a copy of the active environment with thread-safe access
// Returns nil if no active environment is set
func (m *Manager) GetActiveEnvironment() *models.Environment {
	m.dataMu.RLock()
	activeID := m.ActiveEnvID
	envs := make([]models.Environment, len(m.Environments))
	copy(envs, m.Environments)
	m.dataMu.RUnlock()

	for i := range envs {
		if envs[i].ID == activeID {
			// Return a deep copy to prevent external modification
			envCopy := envs[i]
			envCopy.Variables = make(map[string]string)
			for k, v := range envs[i].Variables {
				envCopy.Variables[k] = v
			}
			envCopy.SecretVars = make(map[string]string)
			for k, v := range envs[i].SecretVars {
				envCopy.SecretVars[k] = v
			}
			return &envCopy
		}
	}
	return nil
}

// GetEnvironmentByID returns a specific environment by ID with thread-safe access
// Returns nil if not found
func (m *Manager) GetEnvironmentByID(id string) *models.Environment {
	m.dataMu.RLock()
	defer m.dataMu.RUnlock()

	for _, env := range m.Environments {
		if env.ID == id {
			// Return a deep copy
			envCopy := env
			envCopy.Variables = make(map[string]string)
			for k, v := range env.Variables {
				envCopy.Variables[k] = v
			}
			envCopy.SecretVars = make(map[string]string)
			for k, v := range env.SecretVars {
				envCopy.SecretVars[k] = v
			}
			return &envCopy
		}
	}
	return nil
}

func (m *Manager) SaveActiveEnv() error {
	m.dataMu.RLock()
	id := m.ActiveEnvID
	m.dataMu.RUnlock()
	
	path := filepath.Join(m.BaseDir, "active_env.json")
	data, err := json.MarshalIndent(map[string]string{"activeEnvId": id}, "", "  ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(path, data)
}

func (m *Manager) LoadActiveEnv() error {
	path := filepath.Join(m.BaseDir, "active_env.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var res struct { ActiveEnvId string `json:"activeEnvId"` }
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	m.dataMu.Lock()
	m.ActiveEnvID = res.ActiveEnvId
	m.dataMu.Unlock()
	return nil
}

func (m *Manager) getSafeFilename(path string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	safe := reg.ReplaceAllString(path, "_")
	regMulti := regexp.MustCompile(`_+`)
	safe = regMulti.ReplaceAllString(safe, "_")
	return strings.Trim(safe, "_") + ".json"
}

// Workflow methods

func (m *Manager) GetWorkflows() []models.Workflow {
	m.workflowMu.RLock()
	defer m.workflowMu.RUnlock()
	res := make([]models.Workflow, len(m.Workflows))
	copy(res, m.Workflows)
	return res
}

func (m *Manager) SaveWorkflows(workflows []models.Workflow) error {
	m.workflowMu.Lock()
	m.Workflows = workflows
	m.workflowMu.Unlock()
	return m.saveWorkflowsToFile()
}

func (m *Manager) AddWorkflow(workflow models.Workflow) error {
	m.workflowMu.Lock()
	m.Workflows = append(m.Workflows, workflow)
	m.workflowMu.Unlock()
	return m.saveWorkflowsToFile()
}

func (m *Manager) DeleteWorkflow(id string) error {
	m.workflowMu.Lock()
	defer m.workflowMu.Unlock()

	for i, w := range m.Workflows {
		if w.ID == id {
			m.Workflows = append(m.Workflows[:i], m.Workflows[i+1:]...)
			return m.saveWorkflowsToFile()
		}
	}
	return fmt.Errorf("workflow not found: %s", id)
}

func (m *Manager) UpdateWorkflow(updated models.Workflow) error {
	m.workflowMu.Lock()
	defer m.workflowMu.Unlock()

	for i, w := range m.Workflows {
		if w.ID == updated.ID {
			m.Workflows[i] = updated
			return m.saveWorkflowsToFile()
		}
	}
	return fmt.Errorf("workflow not found: %s", updated.ID)
}

func (m *Manager) LoadWorkflows() error {
	m.workflowMu.Lock()
	defer m.workflowMu.Unlock()

	data, err := os.ReadFile(m.WorkflowsPath)
	if err != nil {
		if os.IsNotExist(err) {
			m.Workflows = []models.Workflow{}
			return nil
		}
		return &StorageError{Op: "Read", Path: m.WorkflowsPath, Err: err}
	}
	if err := json.Unmarshal(data, &m.Workflows); err != nil {
		return &StorageError{Op: "Parse", Path: m.WorkflowsPath, Err: err}
	}
	return nil
}

func (m *Manager) saveWorkflowsToFile() error {
	data, err := json.MarshalIndent(m.Workflows, "", " ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(m.WorkflowsPath, data)
}

// --- Kafka Connections ---

func (m *Manager) GetKafkaConnections() []models.KafkaConnection {
	m.kafkaMu.RLock()
	defer m.kafkaMu.RUnlock()
	res := make([]models.KafkaConnection, len(m.KafkaConnections))
	copy(res, m.KafkaConnections)
	return res
}

func (m *Manager) SaveKafkaConnections(conns []models.KafkaConnection) error {
	m.kafkaMu.Lock()
	m.KafkaConnections = conns
	m.kafkaMu.Unlock()

	path := filepath.Join(m.BaseDir, "kafka_connections.json")
	data, err := json.MarshalIndent(conns, "", "  ")
	if err != nil {
		return err
	}
	return m.atomicWriteFile(path, data)
}

func (m *Manager) LoadKafkaConnections() error {
	path := filepath.Join(m.BaseDir, "kafka_connections.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return &StorageError{Op: "Read", Path: path, Err: err}
	}
	m.kafkaMu.Lock()
	defer m.kafkaMu.Unlock()
	return json.Unmarshal(data, &m.KafkaConnections)
}

func (m *Manager) AddKafkaConnection(conn models.KafkaConnection) error {
	conns := m.GetKafkaConnections()
	// Update if exists, append if new
	found := false
	for i, c := range conns {
		if c.ID == conn.ID {
			conns[i] = conn
			found = true
			break
		}
	}
	if !found {
		conns = append(conns, conn)
	}
	return m.SaveKafkaConnections(conns)
}

func (m *Manager) DeleteKafkaConnection(id string) error {
	conns := m.GetKafkaConnections()
	updated := make([]models.KafkaConnection, 0, len(conns))
	for _, c := range conns {
		if c.ID != id {
			updated = append(updated, c)
		}
	}
	return m.SaveKafkaConnections(updated)
}
