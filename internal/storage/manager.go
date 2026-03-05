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
	"io/ioutil"
	"os"
	"path/filepath"
	"postit/internal/models"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

type Manager struct {
	OutputDir        string
	VarsPath         string
	HeadersPath      string
	HistoryPath      string
	WorkflowsPath    string
	EnvironmentsPath string
	ActiveEnvPath    string
	CollectionPath   string
	VariableMap      map[string]string
	GlobalHeaders    []models.Header
	Environments     []models.Environment
	ActiveEnvID      string
	VaultKey         []byte // AES-256 Key derived from password
	Logger           *log.Logger
	
	// Thread Protection
	varMu            sync.RWMutex
	historyMu        sync.Mutex
	dataMu           sync.RWMutex // for GlobalHeaders and Environments

	// Vault Auto-Lock
	LastActivity     time.Time
	AutoLockDuration time.Duration
	vaultMu          sync.Mutex
}

func NewManager(outputDir string) *Manager {
	return &Manager{
		OutputDir:        outputDir,
		VarsPath:         filepath.Join(outputDir, "variables.json"),
		HeadersPath:      filepath.Join(outputDir, "global_headers.json"),
		HistoryPath:      filepath.Join(outputDir, "history.json"),
		WorkflowsPath:    filepath.Join(outputDir, "workflows.json"),
		EnvironmentsPath: filepath.Join(outputDir, "environments.json"),
		ActiveEnvPath:    filepath.Join(outputDir, "active_env.json"),
		CollectionPath:   filepath.Join(outputDir, "collection.json"),
		VariableMap:      make(map[string]string),
		GlobalHeaders:    []models.Header{},
		Environments:     []models.Environment{},
		AutoLockDuration: 15 * time.Minute, // Default auto-lock
		Logger:           log.Default(),
	}
}

func (m *Manager) StartVaultMonitor() {
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			m.vaultMu.Lock()
			if len(m.VaultKey) > 0 && !m.LastActivity.IsZero() {
				if time.Since(m.LastActivity) > m.AutoLockDuration {
					m.VaultKey = nil
					m.Logger.Info("Vault auto-locked due to inactivity")
				}
			}
			m.vaultMu.Unlock()
		}
	}()
}

func (m *Manager) ResetVaultActivity() {
	m.vaultMu.Lock()
	defer m.vaultMu.Unlock()
	m.LastActivity = time.Now()
}

func (m *Manager) LoadEnvironments() []models.Environment {
	m.dataMu.Lock()
	defer m.dataMu.Unlock()
	if data, err := ioutil.ReadFile(m.EnvironmentsPath); err == nil {
		if err := json.Unmarshal(data, &m.Environments); err != nil {
			m.Logger.Error("Failed to unmarshal environments", "error", err)
		}
	}
	return m.Environments
}

func (m *Manager) GetEnvironments() []models.Environment {
	m.dataMu.RLock()
	defer m.dataMu.RUnlock()
	return append([]models.Environment{}, m.Environments...)
}

func (m *Manager) SaveEnvironments(envs []models.Environment) {
	m.dataMu.Lock()
	m.Environments = envs
	m.dataMu.Unlock()

	data, err := json.MarshalIndent(envs, "", "  ")
	if err != nil {
		m.Logger.Error("Failed to marshal environments", "error", err)
		return
	}
	if err := ioutil.WriteFile(m.EnvironmentsPath, data, 0644); err != nil {
		m.Logger.Error("Failed to write environments file", "error", err)
	}
}

func (m *Manager) LoadActiveEnv() string {
	if data, err := ioutil.ReadFile(m.ActiveEnvPath); err == nil {
		m.ActiveEnvID = strings.TrimSpace(string(data))
	}
	return m.ActiveEnvID
}

func (m *Manager) SaveActiveEnv(id string) {
	m.ActiveEnvID = id
	if err := ioutil.WriteFile(m.ActiveEnvPath, []byte(id), 0644); err != nil {
		m.Logger.Error("Failed to save active env", "error", err)
	}
}

func (m *Manager) LoadWorkflows() []models.Workflow {
	var workflows []models.Workflow
	if data, err := ioutil.ReadFile(m.WorkflowsPath); err == nil {
		if err := json.Unmarshal(data, &workflows); err != nil {
			m.Logger.Error("Failed to unmarshal workflows", "error", err)
		}
	}
	return workflows
}

func (m *Manager) SaveWorkflows(workflows []models.Workflow) {
	data, err := json.MarshalIndent(workflows, "", "  ")
	if err != nil {
		m.Logger.Error("Failed to marshal workflows", "error", err)
		return
	}
	if err := ioutil.WriteFile(m.WorkflowsPath, data, 0644); err != nil {
		m.Logger.Error("Failed to write workflows file", "error", err)
	}
}

func (m *Manager) Init() error {
	if _, err := os.Stat(m.OutputDir); os.IsNotExist(err) {
		os.Mkdir(m.OutputDir, 0755)
	}
	m.LoadVariables()
	m.LoadGlobalHeaders()
	m.StartVaultMonitor()
	return nil
}

func (m *Manager) LoadHistory() []models.HistoryRecord {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	var history []models.HistoryRecord
	if data, err := ioutil.ReadFile(m.HistoryPath); err == nil {
		if err := json.Unmarshal(data, &history); err != nil {
			m.Logger.Error("Failed to unmarshal history", "error", err)
		}
	}
	return history
}

func (m *Manager) SaveHistory(history []models.HistoryRecord) {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	// Keep last 50
	if len(history) > 50 {
		history = history[len(history)-50:]
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		m.Logger.Error("Failed to marshal history", "error", err)
		return
	}
	if err := ioutil.WriteFile(m.HistoryPath, data, 0644); err != nil {
		m.Logger.Error("Failed to write history file", "error", err)
	}
}

func (m *Manager) AddHistoryRecord(record models.HistoryRecord) {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	
	var history []models.HistoryRecord
	if data, err := ioutil.ReadFile(m.HistoryPath); err == nil {
		json.Unmarshal(data, &history)
	}
	
	history = append(history, record)
	if len(history) > 50 {
		history = history[len(history)-50:]
	}
	
	data, _ := json.MarshalIndent(history, "", "  ")
	ioutil.WriteFile(m.HistoryPath, data, 0644)
}

func (m *Manager) LoadVariables() {
	m.varMu.Lock()
	defer m.varMu.Unlock()
	if data, err := ioutil.ReadFile(m.VarsPath); err == nil {
		if err := json.Unmarshal(data, &m.VariableMap); err != nil {
			m.Logger.Error("Failed to unmarshal variables", "error", err)
		}
	}
}

func (m *Manager) SaveVariables() {
	m.varMu.RLock()
	data, err := json.MarshalIndent(m.VariableMap, "", "  ")
	m.varMu.RUnlock()
	
	if err != nil {
		m.Logger.Error("Failed to marshal variables", "error", err)
		return
	}
	if err := ioutil.WriteFile(m.VarsPath, data, 0644); err != nil {
		m.Logger.Error("Failed to write variables file", "error", err)
	}
}

func (m *Manager) SetVariable(key, value string) {
	m.varMu.Lock()
	m.VariableMap[key] = value
	m.varMu.Unlock()
	m.SaveVariables()
}

func (m *Manager) GetVariable(key string) (string, bool) {
	m.varMu.RLock()
	defer m.varMu.RUnlock()
	val, ok := m.VariableMap[key]
	return val, ok
}

func (m *Manager) GetVariableMapCopy() map[string]string {
	m.varMu.RLock()
	defer m.varMu.RUnlock()
	copy := make(map[string]string)
	for k, v := range m.VariableMap {
		copy[k] = v
	}
	return copy
}

func (m *Manager) LoadGlobalHeaders() {
	m.dataMu.Lock()
	defer m.dataMu.Unlock()
	if data, err := ioutil.ReadFile(m.HeadersPath); err == nil {
		if err := json.Unmarshal(data, &m.GlobalHeaders); err != nil {
			m.Logger.Error("Failed to unmarshal global headers", "error", err)
		}
	}
}

func (m *Manager) GetGlobalHeaders() []models.Header {
	m.dataMu.RLock()
	defer m.dataMu.RUnlock()
	return append([]models.Header{}, m.GlobalHeaders...)
}

func (m *Manager) SaveGlobalHeaders(headers []models.Header) {
	m.dataMu.Lock()
	m.GlobalHeaders = headers
	m.dataMu.Unlock()

	data, err := json.MarshalIndent(headers, "", "  ")
	if err != nil {
		m.Logger.Error("Failed to marshal global headers", "error", err)
		return
	}
	if err := ioutil.WriteFile(m.HeadersPath, data, 0644); err != nil {
		m.Logger.Error("Failed to write global headers file", "error", err)
	}
}

func (m *Manager) LoadCollection() (models.Collection, error) {
	var col models.Collection
	data, err := ioutil.ReadFile(m.CollectionPath)
	if err != nil {
		return col, err
	}
	err = json.Unmarshal(data, &col)
	return col, err
}

func (m *Manager) SaveCollection(col models.Collection) error {
	data, err := json.MarshalIndent(col, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(m.CollectionPath, data, 0644)
}

func (m *Manager) LoadCache() map[string]models.RequestInfo {
	cache := make(map[string]models.RequestInfo)

	// Optimal solution for PERF-002: Load from centralized collection index
	if col, err := m.LoadCollection(); err == nil && len(col.Item) > 0 {
		var flatten func([]models.Item, string)
		flatten = func(items []models.Item, prefix string) {
			for _, item := range items {
				path := item.Name
				if prefix != "" {
					path = prefix + " > " + item.Name
				}
				if item.Request != nil {
					cache[path] = models.RequestInfo{
						Path:    path,
						Request: item.Request,
						Events:  item.Event,
						Order:   item.Order,
					}
				}
				if len(item.Item) > 0 {
					flatten(item.Item, path)
				}
			}
		}
		flatten(col.Item, "")
		if len(cache) > 0 {
			return cache
		}
	}

	// Fallback for migration: Scan directory if index is missing or empty
	files, err := ioutil.ReadDir(m.OutputDir)
	if err != nil {
		m.Logger.Error("Failed to read output directory", "error", err)
		return cache
	}
	for _, file := range files {
		name := file.Name()
		if !file.IsDir() && strings.HasSuffix(name, ".json") && 
		   name != "variables.json" && name != "global_headers.json" &&
		   name != "history.json" && name != "workflows.json" &&
		   name != "environments.json" && name != "active_env.json" &&
		   name != "collection.json" {
			data, err := ioutil.ReadFile(filepath.Join(m.OutputDir, name))
			if err != nil {
				continue
			}
			var req models.RequestInfo
			if err := json.Unmarshal(data, &req); err == nil {
				cache[req.Path] = req
			}
		}
	}
	return cache
}

func (m *Manager) SaveSingleRequest(req models.RequestInfo) {
	filename := getSafeFilename(req.Path)
	data, err := json.MarshalIndent(req, "", "    ")
	if err != nil {
		m.Logger.Error("Failed to marshal request", "error", err)
		return
	}
	if err := ioutil.WriteFile(filepath.Join(m.OutputDir, filename), data, 0644); err != nil {
		m.Logger.Error("Failed to write request file", "error", err)
	}
}

func (m *Manager) DeleteRequestFile(path string) {
	filename := getSafeFilename(path)
	if err := os.Remove(filepath.Join(m.OutputDir, filename)); err != nil && !os.IsNotExist(err) {
		m.Logger.Error("Failed to delete request file", "error", err)
	}
}

func (m *Manager) SetVaultPassword(password string) {
	hash := sha256.Sum256([]byte(password))
	m.vaultMu.Lock()
	m.VaultKey = hash[:]
	m.LastActivity = time.Now()
	m.vaultMu.Unlock()
}

func (m *Manager) Encrypt(plaintext string) (string, error) {
	m.ResetVaultActivity()
	m.vaultMu.Lock()
	defer m.vaultMu.Unlock()

	if len(m.VaultKey) == 0 {
		return "", fmt.Errorf("Vault is locked")
	}
	block, err := aes.NewCipher(m.VaultKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (m *Manager) Decrypt(ciphertextStr string) (string, error) {
	m.ResetVaultActivity()
	m.vaultMu.Lock()
	defer m.vaultMu.Unlock()

	if len(m.VaultKey) == 0 {
		return "", fmt.Errorf("Vault is locked")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextStr)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(m.VaultKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func getSafeFilename(path string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	safe := reg.ReplaceAllString(path, "_")
	regMulti := regexp.MustCompile(`_+`)
	safe = regMulti.ReplaceAllString(safe, "_")
	return strings.Trim(safe, "_") + ".json"
}
