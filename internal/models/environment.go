package models

type Environment struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Variables  map[string]string `json:"variables"`
	SecretVars map[string]string `json:"secret_vars,omitempty"` // Key -> Encrypted Value
}
