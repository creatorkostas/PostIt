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
)

type Manager struct {
	OutputDir        string
	VarsPath         string
	HeadersPath      string
	HistoryPath      string
	WorkflowsPath    string
	EnvironmentsPath string
	ActiveEnvPath    string
	VariableMap      map[string]string
	GlobalHeaders    []models.Header
	Environments     []models.Environment
	ActiveEnvID      string
	VaultKey         []byte // AES-256 Key derived from password
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
		VariableMap:      make(map[string]string),
		GlobalHeaders:    []models.Header{},
		Environments:     []models.Environment{},
	}
}

func (m *Manager) LoadEnvironments() []models.Environment {
	if data, err := ioutil.ReadFile(m.EnvironmentsPath); err == nil {
		json.Unmarshal(data, &m.Environments)
	}
	return m.Environments
}

func (m *Manager) SaveEnvironments(envs []models.Environment) {
	m.Environments = envs
	data, _ := json.MarshalIndent(envs, "", "  ")
	ioutil.WriteFile(m.EnvironmentsPath, data, 0644)
}

func (m *Manager) LoadActiveEnv() string {
	if data, err := ioutil.ReadFile(m.ActiveEnvPath); err == nil {
		m.ActiveEnvID = strings.TrimSpace(string(data))
	}
	return m.ActiveEnvID
}

func (m *Manager) SaveActiveEnv(id string) {
	m.ActiveEnvID = id
	ioutil.WriteFile(m.ActiveEnvPath, []byte(id), 0644)
}

func (m *Manager) LoadWorkflows() []models.Workflow {
	var workflows []models.Workflow
	if data, err := ioutil.ReadFile(m.WorkflowsPath); err == nil {
		json.Unmarshal(data, &workflows)
	}
	return workflows
}

func (m *Manager) SaveWorkflows(workflows []models.Workflow) {
	data, _ := json.MarshalIndent(workflows, "", "  ")
	ioutil.WriteFile(m.WorkflowsPath, data, 0644)
}

func (m *Manager) Init() error {
	if _, err := os.Stat(m.OutputDir); os.IsNotExist(err) {
		os.Mkdir(m.OutputDir, 0755)
	}
	m.LoadVariables()
	m.LoadGlobalHeaders()
	return nil
}

func (m *Manager) LoadHistory() []models.HistoryRecord {
	var history []models.HistoryRecord
	if data, err := ioutil.ReadFile(m.HistoryPath); err == nil {
		json.Unmarshal(data, &history)
	}
	return history
}

func (m *Manager) SaveHistory(history []models.HistoryRecord) {
	// Keep last 50
	if len(history) > 50 {
		history = history[len(history)-50:]
	}
	data, _ := json.MarshalIndent(history, "", "  ")
	ioutil.WriteFile(m.HistoryPath, data, 0644)
}

func (m *Manager) LoadVariables() {
	if data, err := ioutil.ReadFile(m.VarsPath); err == nil {
		json.Unmarshal(data, &m.VariableMap)
	}
}

func (m *Manager) SaveVariables() {
	data, _ := json.MarshalIndent(m.VariableMap, "", "  ")
	ioutil.WriteFile(m.VarsPath, data, 0644)
}

func (m *Manager) SetVariable(key, value string) {
	m.VariableMap[key] = value
	m.SaveVariables()
}

func (m *Manager) LoadGlobalHeaders() {
	if data, err := ioutil.ReadFile(m.HeadersPath); err == nil {
		json.Unmarshal(data, &m.GlobalHeaders)
	}
}

func (m *Manager) SaveGlobalHeaders() {
	data, _ := json.MarshalIndent(m.GlobalHeaders, "", "  ")
	ioutil.WriteFile(m.HeadersPath, data, 0644)
}

func (m *Manager) LoadCache() map[string]models.RequestInfo {
	cache := make(map[string]models.RequestInfo)
	files, _ := ioutil.ReadDir(m.OutputDir)
	for _, file := range files {
		name := file.Name()
		if !file.IsDir() && strings.HasSuffix(name, ".json") && 
		   name != "variables.json" && name != "global_headers.json" {
			data, _ := ioutil.ReadFile(filepath.Join(m.OutputDir, name))
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
	data, _ := json.MarshalIndent(req, "", "    ")
	ioutil.WriteFile(filepath.Join(m.OutputDir, filename), data, 0644)
}

func (m *Manager) DeleteRequestFile(path string) {
	filename := getSafeFilename(path)
	os.Remove(filepath.Join(m.OutputDir, filename))
}

func (m *Manager) SetVaultPassword(password string) {
	hash := sha256.Sum256([]byte(password))
	m.VaultKey = hash[:]
}

func (m *Manager) Encrypt(plaintext string) (string, error) {
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
