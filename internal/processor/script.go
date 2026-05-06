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
	"github.com/charmbracelet/log"
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
	Logger        *log.Logger
}

func NewScriptProcessor(store *storage.Manager) *ScriptProcessor {
	return &ScriptProcessor{
		Storage:       store,
		EnablePrompts: true,
		Logger:        log.Default(),
	}
}

func (sp *ScriptProcessor) ResolveVariables(text string) string {
	return sp.ResolveVariablesWithLocal(text, nil)
}

func (sp *ScriptProcessor) ResolveVariablesWithLocal(text string, localVars map[string]string) string {
	return reVar.ReplaceAllStringFunc(text, func(m string) string {
		varName := m[2 : len(m)-2]
		
		// 0. Check local variables
		if localVars != nil {
			if val, ok := localVars[varName]; ok && val != "" {
				return val
			}
		}

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

	// 1. Check active environment (fast O(1) cache lookup first)
	if val, ok := sp.Storage.GetCachedEnvVariable(varName); ok && val != "" {
		// Check if it's an encrypted value (from SecretVars)
		// Encrypted values in cache are stored as-is, so try to decrypt
		if decrypted, err := sp.Storage.Decrypt(val); err == nil {
			return decrypted
		}
		return val
	}

	// Fallback to full environment lookup (thread-safe, O(N))
	if env := sp.Storage.GetActiveEnvironment(); env != nil {
		if val, ok := env.Variables[varName]; ok && val != "" {
			return val
		}
		if val, ok := env.SecretVars[varName]; ok && val != "" {
			if decrypted, err := sp.Storage.Decrypt(val); err == nil {
				return decrypted
			}
			return " [LOCKED] "
		}
	}

		// 2. Check globals
		if val, ok := sp.Storage.GetVariable(varName); ok && val != "" {
			return val
		}
		
		// 3. Check OS env
		if val := os.Getenv(varName); val != "" {
			// Don't auto-save env vars to VariableMap here to avoid leaks in loops
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
	sp.RunScriptsWithLocal(events, typeFilter, responseBody, respHeaders, reqHeaders, nil)
}

func (sp *ScriptProcessor) RunScriptsWithLocal(events []models.Event, typeFilter string, responseBody []byte, respHeaders map[string][]string, reqHeaders []models.Header, localVars map[string]string) {
	if localVars == nil {
		localVars = make(map[string]string)
	}
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

	skipStack := []bool{}
	// Track variables already logged to prevent duplicate messages
	loggedVars := make(map[string]bool)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		if m := reIf.FindStringSubmatch(line); len(m) > 1 {
			cond := m[1]
			isTrue := sp.evaluateCondition(cond, localVars)
			
			parentSkipping := false
			if len(skipStack) > 0 {
				parentSkipping = skipStack[len(skipStack)-1]
			}
			
			skipStack = append(skipStack, parentSkipping || !isTrue)
			continue
		}
		
		if strings.Contains(line, "}else{") || strings.Contains(line, "else {") {
			if len(skipStack) > 0 {
				parentSkipping := false
				if len(skipStack) > 1 {
					parentSkipping = skipStack[len(skipStack)-2]
				}
				if !parentSkipping {
					skipStack[len(skipStack)-1] = !skipStack[len(skipStack)-1]
				}
			}
			continue
		}
		
		if line == "}" {
			if len(skipStack) > 0 {
				skipStack = skipStack[:len(skipStack)-1]
			}
			continue
		}

		if len(skipStack) > 0 && skipStack[len(skipStack)-1] {
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
					// Only log once per key per script execution
					if responseBody != nil && !loggedVars[key] {
						sp.Logger.Info("Variable Saved", "key", key, "value", val)
						loggedVars[key] = true
					}
				}
			}
	}
}

func (sp *ScriptProcessor) evaluateCondition(cond string, localVars map[string]string) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return false
	}

	// Handle OR (lower precedence than AND, but here we evaluate simply)
	if strings.Contains(cond, "||") {
		parts := strings.Split(cond, "||")
		for _, p := range parts {
			if sp.evaluateCondition(p, localVars) {
				return true
			}
		}
		return false
	}

	// Handle AND
	if strings.Contains(cond, "&&") {
		parts := strings.Split(cond, "&&")
		for _, p := range parts {
			if !sp.evaluateCondition(p, localVars) {
				return false
			}
		}
		return true
	}

	return sp.evaluateAtomicCondition(cond, localVars)
}

func (sp *ScriptProcessor) evaluateAtomicCondition(cond string, localVars map[string]string) bool {
	cond = strings.TrimSpace(cond)

	// Check for negation prefix
	negate := false
	if strings.HasPrefix(cond, "!") {
		negate = true
		cond = strings.TrimSpace(cond[1:])
	}

	op := ""
	if strings.Contains(cond, "===") {
		op = "==="
	} else if strings.Contains(cond, "==") {
		op = "=="
	} else if strings.Contains(cond, "!=") {
		op = "!="
	}

	if op == "" {
		// Support truthiness (e.g., if (authToken))
		val := ""
		if v, ok := localVars[cond]; ok {
			val = v
		} else if v, ok := sp.Storage.GetVariable(cond); ok {
			val = v
		}
		isTrue := val != "" && val != "false" && val != "0" && val != "null" && val != "undefined"
		if negate {
			return !isTrue
		}
		return isTrue
	}

	parts := strings.Split(cond, op)
	if len(parts) != 2 {
		sp.Logger.Warn("Malformed atomic condition", "cond", cond, "op", op)
		return false
	}

	left := strings.TrimSpace(parts[0])
	right := strings.Trim(strings.TrimSpace(parts[1]), "'\"")

	leftVal := left
	if val, ok := localVars[left]; ok {
		leftVal = val
	} else if val, ok := sp.Storage.GetVariable(left); ok {
		leftVal = val
	}

	result := false
	if right == "null" || right == "undefined" {
		// Handle null/undefined keywords
		if op == "==" || op == "===" {
			result = leftVal == "" || leftVal == "null" || leftVal == "undefined"
		} else if op == "!=" {
			result = leftVal != "" && leftVal != "null" && leftVal != "undefined"
		}
	} else {
		if op == "==" || op == "===" {
			result = leftVal == right
		} else if op == "!=" {
			result = leftVal != right
		}
	}

	if negate {
		return !result
	}
	return result
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
		// Check localVars first for consistency
		if val, ok := localVars[key]; ok {
			return val
		}
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
		} else {
			sp.Logger.Warn("Malformed JSON path", "raw", raw)
		}
	}
	if val, ok := localVars[raw]; ok {
		return val
	}
	return ""
}

func (sp *ScriptProcessor) GetOrPrompt(name string) string {
	// 1. Check active environment (thread-safe access)
	if env := sp.Storage.GetActiveEnvironment(); env != nil {
		if val, ok := env.Variables[name]; ok && val != "" {
			return val
		}
		if val, ok := env.SecretVars[name]; ok && val != "" {
			if decrypted, err := sp.Storage.Decrypt(val); err == nil {
				return decrypted
			}
			return " [LOCKED] "
		}
	}

	if val, ok := sp.Storage.GetVariable(name); ok && val != "" {
		return val
	}
	if val := os.Getenv(name); val != "" {
		// No auto-save here to prevent loop leaks
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
