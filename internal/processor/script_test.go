package processor

import (
	"os"
	"postit/internal/models"
	"postit/internal/storage"
	"strings"
	"testing"
)

func setupScriptProcessor(t *testing.T) *ScriptProcessor {
	t.Helper()
	tmpDir := t.TempDir()
	store := storage.NewManager(tmpDir)
	store.Init()
	sp := NewScriptProcessor(store)
	sp.EnablePrompts = false // Disable interactive prompts for testing
	return sp
}

func TestEvaluateAtomicCondition(t *testing.T) {
	sp := setupScriptProcessor(t)

	tests := []struct {
		name     string
		cond     string
		localVars map[string]string
		want     bool
	}{
		// Equality
		{"exact match ==", "status == 200", map[string]string{"status": "200"}, true},
		{"exact match ===", "status === 200", map[string]string{"status": "200"}, true},
		{"exact match != fail", "status != 200", map[string]string{"status": "200"}, false},
		{"exact match != success", "status != 404", map[string]string{"status": "200"}, true},
		{"string equality", "name === John", map[string]string{"name": "John"}, true},
		{"string inequality", "name === John", map[string]string{"name": "Jane"}, false},

		// Null/undefined handling
		// Note: when a variable is undefined, the raw variable name string is used as leftVal,
		// so "value == null" compares the literal string "value" against "null" → false
		{"null check == null (empty)", "value == null", map[string]string{}, false},
		{"null check == null (explicit)", "value == null", map[string]string{"value": "null"}, true},
		{"null check == null (defined)", "value == null", map[string]string{"value": "something"}, false},
		{"undefined check", "value == undefined", map[string]string{}, false},
		{"not null check", "value != null", map[string]string{"value": "something"}, true},

		// Truthiness
		{"truthy check", "authToken", map[string]string{"authToken": "abc123"}, true},
		{"falsy empty", "authToken", map[string]string{}, false},
		{"falsy empty string", "authToken", map[string]string{"authToken": ""}, false},
		{"falsy false", "authToken", map[string]string{"authToken": "false"}, false},
		{"falsy zero", "authToken", map[string]string{"authToken": "0"}, false},
		{"falsy null", "authToken", map[string]string{"authToken": "null"}, false},
		{"falsy undefined", "authToken", map[string]string{"authToken": "undefined"}, false},

		// Negation
		{"negation true", "!missing", map[string]string{}, true},
		{"negation false", "!present", map[string]string{"present": "exists"}, false},
		{"not equal false", "!status === 200", map[string]string{"status": "200"}, false},
		{"not equal true", "!status === 404", map[string]string{"status": "200"}, true},

		// Malformed (should not panic)
		{"empty condition", "", map[string]string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sp.evaluateAtomicCondition(tt.cond, tt.localVars)
			if got != tt.want {
				t.Errorf("evaluateAtomicCondition(%q) = %v, want %v", tt.cond, got, tt.want)
			}
		})
	}
}

func TestEvaluateCondition(t *testing.T) {
	sp := setupScriptProcessor(t)

	tests := []struct {
		name     string
		cond     string
		localVars map[string]string
		want     bool
	}{
		// OR
		{"or true left", "status == 200 || status == 404", map[string]string{"status": "200"}, true},
		{"or true right", "status == 200 || status == 404", map[string]string{"status": "404"}, true},
		{"or false", "status == 200 || status == 404", map[string]string{"status": "500"}, false},

		// AND
		{"and true", "status == 200 && name == John", map[string]string{"status": "200", "name": "John"}, true},
		{"and false left", "status == 200 && name == John", map[string]string{"status": "404", "name": "John"}, false},
		{"and false right", "status == 200 && name == John", map[string]string{"status": "200", "name": "Jane"}, false},

		// Note: complex parenthesized conditions aren't supported by the simple evaluator
		// So we test simpler combined conditions
		{"simple or", "a == 1 || b == 2", map[string]string{"a": "1", "b": "0"}, true},
		{"simple and", "a == 1 && c == 3", map[string]string{"a": "1", "c": "3"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sp.evaluateCondition(tt.cond, tt.localVars)
			if got != tt.want {
				t.Errorf("evaluateCondition(%q) = %v, want %v", tt.cond, got, tt.want)
			}
		})
	}
}

func TestResolveValue(t *testing.T) {
	sp := setupScriptProcessor(t)

	body := `{"user":{"name":"Alice","age":30}}`
	respHeaders := map[string][]string{
		"Content-Type": {"application/json"},
		"X-Request-Id": {"abc-123"},
	}
	reqHeaders := []models.Header{
		{Key: "Authorization", Value: "Bearer test-token"},
	}

	tests := []struct {
		name       string
		raw        string
		body       *string
		respHeaders map[string][]string
		reqHeaders []models.Header
		localVars  map[string]string
		want       string
	}{
		// String literals
		{"string literal single quotes", "'hello'", nil, nil, nil, nil, "hello"},
		{"string literal double quotes", "\"hello\"", nil, nil, nil, nil, "hello"},

		// Concatenation
		{"concatenation", "'hello' + ' ' + 'world'", nil, nil, nil, nil, "hello world"},

		// Local variable resolution
		{"local variable", "myVar", nil, nil, nil, map[string]string{"myVar": "myValue"}, "myValue"},
		{"missing local variable", "missingVar", nil, nil, nil, nil, ""},

		// JSON path
		{"json path user name", "pm.response.json().user.name", &body, nil, nil, nil, "Alice"},
		{"json path user age", "pm.response.json().user.age", &body, nil, nil, nil, "30"},
		{"json path with optional chaining", "pm.response.json()?.user?.name", &body, nil, nil, nil, "Alice"},

		// Response headers
		{"response header", "pm.response.headers.get('Content-Type')", nil, respHeaders, nil, nil, "application/json"},
		{"response header not found", "pm.response.headers.get('X-Missing')", nil, respHeaders, nil, nil, ""},

		// Request headers
		{"request header", "pm.request.headers.get('Authorization')", nil, nil, reqHeaders, nil, "Bearer test-token"},
		{"request header not found", "pm.request.headers.get('X-Missing')", nil, nil, reqHeaders, nil, ""},

		// Empty raw
		{"empty raw", "", nil, nil, nil, nil, ""},
		{"whitespace raw", "  ", nil, nil, nil, nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sp.resolveValue(tt.raw, tt.body, tt.respHeaders, tt.reqHeaders, tt.localVars)
			if got != tt.want {
				t.Errorf("resolveValue(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolveValue_GetVarPattern(t *testing.T) {
	sp := setupScriptProcessor(t)

	// Test pm.collectionVariables.get, pm.environment.get, pm.globals.get
	sp.Storage.SetVariable("storedKey", "storedValue")

	tests := []struct {
		name      string
		raw       string
		localVars map[string]string
		want      string
	}{
		{"pm.collectionVariables.get", "pm.collectionVariables.get('storedKey')", nil, "storedValue"},
		{"pm.environment.get", "pm.environment.get('storedKey')", nil, "storedValue"},
		{"pm.globals.get", "pm.globals.get('storedKey')", nil, "storedValue"},
		{"collection var with local override", "pm.collectionVariables.get('storedKey')", map[string]string{"storedKey": "localOverride"}, "localOverride"},
		{"get non-existent returns unresolved", "pm.collectionVariables.get('noSuchKey')", nil, "{{noSuchKey}}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sp.resolveValue(tt.raw, nil, nil, nil, tt.localVars)
			if got != tt.want {
				t.Errorf("resolveValue(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestProcessLines_BasicExecution(t *testing.T) {
	sp := setupScriptProcessor(t)

	t.Run("variable assignment", func(t *testing.T) {
		localVars := map[string]string{}
		lines := []string{
			"const result = 'success'",
			"let count = '42'",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		if localVars["result"] != "success" {
			t.Errorf("Expected result='success', got '%s'", localVars["result"])
		}
		if localVars["count"] != "42" {
			t.Errorf("Expected count='42', got '%s'", localVars["count"])
		}
	})

	t.Run("pm.set variable", func(t *testing.T) {
		localVars := map[string]string{}
		lines := []string{
			"pm.collectionVariables.set('key', 'value')",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		val, ok := sp.Storage.GetVariable("key")
		if !ok || val != "value" {
			t.Errorf("Expected key='value' in storage, got '%s', ok=%v", val, ok)
		}
	})

	t.Run("empty lines and comments are skipped", func(t *testing.T) {
		localVars := map[string]string{}
		lines := []string{
			"",
			"  ",
			"// This is a comment",
			"const x = 'y'",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		if localVars["x"] != "y" {
			t.Errorf("Expected x='y', got '%s'", localVars["x"])
		}
	})
}

func TestProcessLines_Conditionals(t *testing.T) {
	sp := setupScriptProcessor(t)

	t.Run("if true executes body", func(t *testing.T) {
		localVars := map[string]string{"status": "200"}
		lines := []string{
			"if (status == 200)",
			"const result = 'OK'",
			"}",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		if localVars["result"] != "OK" {
			t.Errorf("Expected result='OK', got '%s'", localVars["result"])
		}
	})

	t.Run("if false skips body", func(t *testing.T) {
		localVars := map[string]string{"status": "404"}
		lines := []string{
			"if (status == 200)",
			"const result = 'OK'",
			"}",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		if _, ok := localVars["result"]; ok {
			t.Errorf("Expected result to not be set when condition is false")
		}
	})

	t.Run("if else true branch", func(t *testing.T) {
		localVars := map[string]string{"status": "200"}
		lines := []string{
			"if (status == 200)",
			"const branch = 'if'",
			"}else{",
			"const branch = 'else'",
			"}",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		if localVars["branch"] != "if" {
			t.Errorf("Expected branch='if', got '%s'", localVars["branch"])
		}
	})

	t.Run("if else false branch", func(t *testing.T) {
		localVars := map[string]string{"status": "500"}
		lines := []string{
			"if (status == 200)",
			"const branch = 'if'",
			"}else{",
			"const branch = 'else'",
			"}",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		if localVars["branch"] != "else" {
			t.Errorf("Expected branch='else', got '%s'", localVars["branch"])
		}
	})

	t.Run("nested if conditions", func(t *testing.T) {
		localVars := map[string]string{"a": "1", "b": "2"}
		lines := []string{
			"if (a == 1)",
			"const outer = 'yes'",
			"if (b == 2)",
			"const inner = 'yes'",
			"}",
			"}",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		if localVars["outer"] != "yes" {
			t.Errorf("Expected outer='yes', got '%s'", localVars["outer"])
		}
		if localVars["inner"] != "yes" {
			t.Errorf("Expected inner='yes', got '%s'", localVars["inner"])
		}
	})

	t.Run("nested if with outer false", func(t *testing.T) {
		localVars := map[string]string{"a": "0", "b": "2"}
		lines := []string{
			"if (a == 1)",
			"const outer = 'yes'",
			"if (b == 2)",
			"const inner = 'yes'",
			"}",
			"}",
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		if _, ok := localVars["outer"]; ok {
			t.Errorf("Expected 'outer' to not be set when outer condition is false")
		}
		if _, ok := localVars["inner"]; ok {
			t.Errorf("Expected 'inner' to not be set when outer condition is false")
		}
	})
}

func TestProcessLines_ResponseDataAccess(t *testing.T) {
	sp := setupScriptProcessor(t)

	t.Run("extract json path from body", func(t *testing.T) {
		localVars := map[string]string{}
		body := []byte(`{"data":{"id":123,"name":"Test"}}`)
		lines := []string{
			"const id = pm.response.json().data.id",
			"const name = pm.response.json().data.name",
		}
		sp.processLines(lines, body, nil, nil, localVars)

		if localVars["id"] != "123" {
			t.Errorf("Expected id='123', got '%s'", localVars["id"])
		}
		if localVars["name"] != "Test" {
			t.Errorf("Expected name='Test', got '%s'", localVars["name"])
		}
	})
}

func TestHttpHeaderKey(t *testing.T) {
	headers := map[string][]string{
		"Content-Type": {"application/json"},
		"X-Custom-Header": {"value1"},
	}

	tests := []struct {
		key      string
		expected string
	}{
		{"content-type", "Content-Type"},
		{"CONTENT-TYPE", "Content-Type"},
		{"Content-Type", "Content-Type"},
		{"x-custom-header", "X-Custom-Header"},
		{"x-custom-header", "X-Custom-Header"},
		{"missing-key", "missing-key"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := httpHeaderKey(tt.key, headers)
			if result != tt.expected {
				t.Errorf("httpHeaderKey(%q) = %q, want %q", tt.key, result, tt.expected)
			}
		})
	}
}

func TestResolveVariablesWithLocal(t *testing.T) {
	sp := setupScriptProcessor(t)
	sp.Storage.SetVariable("globalVar", "globalValue")

	t.Run("resolves local variables", func(t *testing.T) {
		result := sp.ResolveVariablesWithLocal("Hello {{name}}", map[string]string{"name": "World"})
		if result != "Hello World" {
			t.Errorf("Expected 'Hello World', got '%s'", result)
		}
	})

	t.Run("resolves global variables as fallback", func(t *testing.T) {
		result := sp.ResolveVariablesWithLocal("Global: {{globalVar}}", nil)
		if result != "Global: globalValue" {
			t.Errorf("Expected 'Global: globalValue', got '%s'", result)
		}
	})

	t.Run("local overrides global", func(t *testing.T) {
		result := sp.ResolveVariablesWithLocal("{{globalVar}}", map[string]string{"globalVar": "localValue"})
		if result != "localValue" {
			t.Errorf("Expected 'localValue', got '%s'", result)
		}
	})

	t.Run("no match with prompts disabled", func(t *testing.T) {
		result := sp.ResolveVariablesWithLocal("{{missingVar}}", nil)
		// Since EnablePrompts is false, it should return as-is
		expected := "{{missingVar}}"
		if result != expected {
			t.Errorf("Expected '%s', got '%s'", expected, result)
		}
	})

	t.Run("no variable placeholders returns original", func(t *testing.T) {
		result := sp.ResolveVariablesWithLocal("plain text", nil)
		if result != "plain text" {
			t.Errorf("Expected 'plain text', got '%s'", result)
		}
	})

	t.Run("empty text", func(t *testing.T) {
		result := sp.ResolveVariablesWithLocal("", nil)
		if result != "" {
			t.Errorf("Expected '', got '%s'", result)
		}
	})
}

func TestResolveVariables_MagicVariables(t *testing.T) {
	sp := setupScriptProcessor(t)

	t.Run("$guid generates uuid", func(t *testing.T) {
		result := sp.ResolveVariables("{{$guid}}")
		if result == "" || len(result) < 20 {
			t.Errorf("Expected a UUID string, got '%s'", result)
		}
	})

	t.Run("$timestamp generates number", func(t *testing.T) {
		result := sp.ResolveVariables("{{$timestamp}}")
		if result == "" {
			t.Errorf("Expected a timestamp string, got empty")
		}
	})

	t.Run("$randomInt generates a number", func(t *testing.T) {
		result := sp.ResolveVariables("{{$randomInt}}")
		if result == "" {
			t.Errorf("Expected a random int string, got empty")
		}
	})

	t.Run("$randomPassword generates a password", func(t *testing.T) {
		result := sp.ResolveVariables("{{$randomPassword}}")
		if len(result) != 12 {
			t.Errorf("Expected 12-char password, got %d: '%s'", len(result), result)
		}
	})
}

func TestRunScriptsWithLocal(t *testing.T) {
	sp := setupScriptProcessor(t)

	t.Run("filters by event type", func(t *testing.T) {
		events := []models.Event{
			{Listen: "prerequest", Script: models.Script{Exec: []string{"const x = 'pre'"}}},
			{Listen: "test", Script: models.Script{Exec: []string{"const y = 'test'"}}},
		}

		localVars := map[string]string{}

		// Only run "test" events
		sp.RunScriptsWithLocal(events, "test", nil, nil, nil, localVars)

		if _, ok := localVars["x"]; ok {
			t.Error("'prerequest' event should not have been executed")
		}
		if localVars["y"] != "test" {
			t.Errorf("Expected y='test', got '%s'", localVars["y"])
		}
	})

	t.Run("no matching events", func(t *testing.T) {
		events := []models.Event{
			{Listen: "prerequest", Script: models.Script{Exec: []string{"const x = '1'"}}},
		}

		localVars := map[string]string{}
		sp.RunScriptsWithLocal(events, "test", nil, nil, nil, localVars)

		if _, ok := localVars["x"]; ok {
			t.Error("No 'test' events should have been executed")
		}
	})

	t.Run("empty events list", func(t *testing.T) {
		localVars := map[string]string{}
		sp.RunScriptsWithLocal([]models.Event{}, "test", nil, nil, nil, localVars)
		// Should not panic
	})
}

func TestGetOrPrompt_PromptsDisabled(t *testing.T) {
	sp := setupScriptProcessor(t)
	sp.EnablePrompts = false

	t.Run("returns unresolved for missing var", func(t *testing.T) {
		result := sp.GetOrPrompt("nonExistentVar")
		if result != "{{nonExistentVar}}" {
			t.Errorf("Expected '{{nonExistentVar}}', got '%s'", result)
		}
	})

	t.Run("checks storage first", func(t *testing.T) {
		sp.Storage.SetVariable("existingVar", "existingValue")
		result := sp.GetOrPrompt("existingVar")
		if result != "existingValue" {
			t.Errorf("Expected 'existingValue', got '%s'", result)
		}
	})

	t.Run("checks os env", func(t *testing.T) {
		os.Setenv("TEST_POSTIT_VAR", "envValue")
		defer os.Unsetenv("TEST_POSTIT_VAR")

		result := sp.GetOrPrompt("TEST_POSTIT_VAR")
		if result != "envValue" {
			t.Errorf("Expected 'envValue', got '%s'", result)
		}
	})
}

func TestProcessLines_PmSetWithJsonPath(t *testing.T) {
	sp := setupScriptProcessor(t)

	t.Run("pm.set with string literal value", func(t *testing.T) {
		localVars := map[string]string{}
		lines := []string{
			`pm.collectionVariables.set('token', 'abc123')`,
		}
		sp.processLines(lines, nil, nil, nil, localVars)

		val, ok := sp.Storage.GetVariable("token")
		if !ok || val != "abc123" {
			t.Errorf("Expected token='abc123', got '%s'", val)
		}
	})
}

func TestScriptErrorHandling(t *testing.T) {
	sp := setupScriptProcessor(t)

	t.Run("malformed pm.set doesn't crash", func(t *testing.T) {
		localVars := map[string]string{}
		lines := []string{
			`pm.collectionVariables.set(`,
		}
		sp.processLines(lines, nil, nil, nil, localVars)
		// Should not panic
	})

	t.Run("malformed json path doesn't crash", func(t *testing.T) {
		localVars := map[string]string{}
		body := []byte(`{"a":1}`)
		lines := []string{
			`pm.response.json()...invalid`,
		}
		sp.processLines(lines, body, nil, nil, localVars)
		// Should not panic
	})
}

func TestResolveVariablesWithLocal_EdgeCases(t *testing.T) {
	sp := setupScriptProcessor(t)

	t.Run("multiple variables in one string", func(t *testing.T) {
		result := sp.ResolveVariablesWithLocal("{{a}}-{{b}}", map[string]string{"a": "1", "b": "2"})
		if result != "1-2" {
			t.Errorf("Expected '1-2', got '%s'", result)
		}
	})

	t.Run("nested braces are not variables", func(t *testing.T) {
		result := sp.ResolveVariablesWithLocal("{{a{b}c}}", nil)
		if !strings.Contains(result, "{{") {
			t.Errorf("Expected unresolved variable, got '%s'", result)
		}
	})
}
