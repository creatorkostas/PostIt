package processor

import (
	"fmt"
	"math/rand"
	"os"
	"postit/internal/models"
	"postit/internal/storage"
	"regexp"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

var (
	reVar       = regexp.MustCompile(`\{\{([^}]+)\}\}`)
	reSetVar    = regexp.MustCompile(`pm\.(collectionVariables|environment|globals)\.set\(['"](.+?)['"]\s*,\s*(.+?)\)`)
	reGetVar    = regexp.MustCompile(`pm\.(collectionVariables|environment|globals)\.get\(['"](.+?)['"]\)`)
	reJsonPath  = regexp.MustCompile(`pm\.response\.json\(\)\?*\.(.+?)(?:;|\n|$)`)
	reHeaderGet = regexp.MustCompile(`pm\.(response|request)\.headers\.get\(['"](.+?)['"]\)`)
	reAssign    = regexp.MustCompile(`(?:const|let|var|)\s*(\w+)\s*=\s*(.+?)(?:;|\n|$)`)
	reIf        = regexp.MustCompile(`if\s*\((.+?)\)`)
)

type ScriptProcessor struct {
	Storage       *storage.Manager
	EnablePrompts bool
}

func NewScriptProcessor(store *storage.Manager) *ScriptProcessor {
	return &ScriptProcessor{Storage: store, EnablePrompts: true}
}

func (sp *ScriptProcessor) ResolveVariables(text string) string {
	return reVar.ReplaceAllStringFunc(text, func(m string) string {
		varName := m[2 : len(m)-2]
		
		// Magic Variables
		if strings.HasPrefix(varName, "$") {
			switch varName {
			case "$guid":
				return uuid.New().String()
			case "$timestamp":
				return fmt.Sprintf("%d", time.Now().Unix())
			case "$isoTimestamp":
				return time.Now().Format(time.RFC3339)
			case "$randomInt":
				return fmt.Sprintf("%d", rand.Intn(1000))
			case "$randomPassword":
				const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
				b := make([]byte, 12)
				for i := range b {
					b[i] = charset[rand.Intn(len(charset))]
				}
				return string(b)
			}
		}

		// 1. Check active environment
		if sp.Storage.ActiveEnvID != "" {
			for _, env := range sp.Storage.Environments {
				if env.ID == sp.Storage.ActiveEnvID {
					if val, ok := env.Variables[varName]; ok && val != "" {
						return val
					}
					if val, ok := env.SecretVars[varName]; ok && val != "" {
						if decrypted, err := sp.Storage.Decrypt(val); err == nil {
							return decrypted
						}
						return " [LOCKED] "
					}
					break
				}
			}
		}

		// 2. Check globals
		if val, ok := sp.Storage.GetVariable(varName); ok && val != "" {
			return val
		}
		
		// 3. Check OS env
		if val := os.Getenv(varName); val != "" {
			sp.Storage.SetVariable(varName, val)
			return val
		}
		
		if !sp.EnablePrompts {
			return "{{" + varName + "}}" // Return as-is if no prompts allowed
		}

		var val string
		prompt := &survey.Input{Message: fmt.Sprintf("Variable '%s' required. Value:", varName)}
		survey.AskOne(prompt, &val)
		sp.Storage.SetVariable(varName, val)
		return val
	})
}

func (sp *ScriptProcessor) RunScripts(events []models.Event, typeFilter string, responseBody []byte, respHeaders map[string][]string, reqHeaders []models.Header) {
	localVars := make(map[string]string)
	for _, event := range events {
		if event.Listen != typeFilter {
			continue
		}
		sp.processLines(event.Script.Exec, responseBody, respHeaders, reqHeaders, localVars)
	}
}

func (sp *ScriptProcessor) processLines(lines []string, responseBody []byte, respHeaders map[string][]string, reqHeaders []models.Header, localVars map[string]string) {
	var bodyStr *string
	if responseBody != nil {
		s := string(responseBody)
		bodyStr = &s
	}

	skipping := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		if m := reIf.FindStringSubmatch(line); len(m) > 1 {
			cond := m[1]
			skipping = !sp.evaluateCondition(cond, localVars)
			continue
		}
		if strings.Contains(line, "}else{") || strings.Contains(line, "else {") {
			skipping = !skipping
			continue
		}
		if line == "}" {
			skipping = false
			continue
		}

		if skipping {
			continue
		}

		if m := reAssign.FindStringSubmatch(line); len(m) > 2 {
			varName := m[1]
			valRaw := strings.TrimSpace(m[2])
			val := sp.resolveValue(valRaw, bodyStr, respHeaders, reqHeaders, localVars)
			if val != "" {
				localVars[varName] = val
			}
		}

		if m := reSetVar.FindStringSubmatch(line); len(m) > 3 {
			key := strings.Trim(m[2], "'\"")
			valRaw := strings.TrimSpace(m[3])
			val := sp.resolveValue(valRaw, bodyStr, respHeaders, reqHeaders, localVars)
			if val != "" {
				sp.Storage.SetVariable(key, val)
				if responseBody != nil {
					fmt.Printf(" [Script] Variable Saved: %s = %s\n", key, val)
				}
			}
		}
	}
}

func (sp *ScriptProcessor) evaluateCondition(cond string, localVars map[string]string) bool {
	cond = strings.ReplaceAll(cond, "===", "==")
	parts := strings.Split(cond, "==")
	if len(parts) != 2 {
		return true
	}
	left := strings.TrimSpace(parts[0])
	right := strings.Trim(strings.TrimSpace(parts[1]), "'\"")
	leftVal := left
	if val, ok := localVars[left]; ok {
		leftVal = val
	} else if val, ok := sp.Storage.GetVariable(left); ok {
		leftVal = val
	}
	return leftVal == right
}

func (sp *ScriptProcessor) resolveValue(raw string, body *string, respHeaders map[string][]string, reqHeaders []models.Header, localVars map[string]string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "+") {
		parts := strings.Split(raw, "+")
		result := ""
		for _, p := range parts {
			result += sp.resolveValue(p, body, respHeaders, reqHeaders, localVars)
		}
		return result
	}
	if strings.HasPrefix(raw, "'") || strings.HasPrefix(raw, "\"") {
		return strings.Trim(raw, "'\"")
	}
	if m := reGetVar.FindStringSubmatch(raw); len(m) > 2 {
		key := strings.Trim(m[2], "'\"")
		val, ok := sp.Storage.GetVariable(key)
		if !ok || val == "" {
			return sp.GetOrPrompt(key)
		}
		return val
	}
	if m := reHeaderGet.FindStringSubmatch(raw); len(m) > 2 {
		source := m[1]
		hName := strings.Trim(m[2], "'\"")
		if source == "response" && respHeaders != nil {
			if vals, ok := respHeaders[httpHeaderKey(hName, respHeaders)]; ok && len(vals) > 0 {
				return vals[0]
			}
		} else if source == "request" && reqHeaders != nil {
			for _, h := range reqHeaders {
				if strings.EqualFold(h.Key, hName) {
					return h.Value
				}
			}
		}
	}
	if body != nil && strings.Contains(raw, "pm.response.json()") {
		if m := reJsonPath.FindStringSubmatch(raw); len(m) > 1 {
			jsPath := strings.ReplaceAll(m[1], "?.", ".")
			return gjson.Get(*body, jsPath).String()
		}
	}
	if val, ok := localVars[raw]; ok {
		return val
	}
	return ""
}

func (sp *ScriptProcessor) GetOrPrompt(name string) string {
	// 1. Check active environment
	if sp.Storage.ActiveEnvID != "" {
		for _, env := range sp.Storage.Environments {
			if env.ID == sp.Storage.ActiveEnvID {
				if val, ok := env.Variables[name]; ok && val != "" {
					return val
				}
				if val, ok := env.SecretVars[name]; ok && val != "" {
					if decrypted, err := sp.Storage.Decrypt(val); err == nil {
						return decrypted
					}
					return " [LOCKED] "
				}
				break
			}
		}
	}

	if val, ok := sp.Storage.GetVariable(name); ok && val != "" {
		return val
	}
	if val := os.Getenv(name); val != "" {
		sp.Storage.SetVariable(name, val)
		return val
	}

	if !sp.EnablePrompts {
		return "{{" + name + "}}"
	}

	var val string
	prompt := &survey.Input{Message: fmt.Sprintf("Variable '%s' required. Value:", name)}
	survey.AskOne(prompt, &val)
	sp.Storage.SetVariable(name, val)
	return val
}

func httpHeaderKey(key string, headers map[string][]string) string {
	for k := range headers {
		if strings.EqualFold(k, key) {
			return k
		}
	}
	return key
}
