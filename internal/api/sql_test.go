package api

import (
	"testing"
)

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "basic uppercase",
			input:    "select * from users",
			expected: "SELECT * FROM USERS",
		},
		{
			name:     "cyrillic homoglyph bypass",
			input:    "selеct", // Uses Cyrillic lowercase е (U+0435)
			// switch returns uppercase 'E', consistent with unicode.ToUpper on other chars
			expected: "SELECT",
		},
		{
			name:     "mixed homoglyphs lowercase",
			input:    "drор", // Uses Cyrillic lowercase о (U+043E) and р (U+0440)
			// switch returns uppercase 'O','P', consistent with unicode.ToUpper on other chars
			expected: "DROP",
		},
		{
			name:     "control characters filtered",
			input:    "SELECT\x00\x01\x02 * FROM users",
			expected: "SELECT * FROM USERS",
		},
		{
			name:     "newlines preserved",
			input:    "SELECT *\nFROM users",
			expected: "SELECT *\nFROM USERS",
		},
		{
			name:     "tabs preserved",
			input:    "SELECT\t* FROM users",
			expected: "SELECT\t* FROM USERS",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "all cyrillic lowercase homoglyphs",
			input:    "аеорсхі", // All supported Cyrillic homoglyphs (lowercase)
			// all switch returns uppercase, matching unicode.ToUpper consistent behavior
			expected: "AEOPCXI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeQuery(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeQuery(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestValidateSQLQuery(t *testing.T) {
	t.Run("safe SELECT queries", func(t *testing.T) {
		safe := []string{
			"SELECT * FROM users",
			"select name, email from users where id = 1",
			"SELECT COUNT(*) FROM orders",
			"SELECT u.name, o.total FROM users u JOIN orders o ON u.id = o.user_id",
		}
		for _, q := range safe {
			t.Run(q, func(t *testing.T) {
				err := validateSQLQuery(q)
				if err != nil {
					t.Errorf("Expected no error for safe query %q, got: %v", q, err)
				}
			})
		}
	})

	t.Run("unsafe DROP queries", func(t *testing.T) {
		unsafe := []string{
			"DROP TABLE users",
			"drop table users",
		}
		for _, q := range unsafe {
			t.Run(q, func(t *testing.T) {
				err := validateSQLQuery(q)
				if err == nil {
					t.Errorf("Expected error for unsafe query %q", q)
				}
			})
		}
	})

	t.Run("unsafe DELETE queries", func(t *testing.T) {
		err := validateSQLQuery("DELETE FROM users")
		if err == nil {
			t.Error("Expected error for DELETE query")
		}
	})

	t.Run("unsafe INSERT queries", func(t *testing.T) {
		err := validateSQLQuery("INSERT INTO users VALUES (1)")
		if err == nil {
			t.Error("Expected error for INSERT query")
		}
	})

	t.Run("unsafe UPDATE queries", func(t *testing.T) {
		err := validateSQLQuery("UPDATE users SET name = 'test'")
		if err == nil {
			t.Error("Expected error for UPDATE query")
		}
	})

	t.Run("unsafe TRUNCATE queries", func(t *testing.T) {
		err := validateSQLQuery("TRUNCATE TABLE users")
		if err == nil {
			t.Error("Expected error for TRUNCATE query")
		}
	})

	t.Run("unsafe ALTER TABLE queries", func(t *testing.T) {
		err := validateSQLQuery("ALTER TABLE users DROP COLUMN email")
		if err == nil {
			t.Error("Expected error for ALTER TABLE query")
		}
	})

	t.Run("unsafe EXECUTE queries", func(t *testing.T) {
		err := validateSQLQuery("EXECUTE sp_delete_users")
		if err == nil {
			t.Error("Expected error for EXECUTE query")
		}
	})

	t.Run("query with obfuscated keyword via comments", func(t *testing.T) {
		queries := []string{
			"SELECT * FROM users; DR/**/OP TABLE users",
			"SELECT * FROM users -- DROP TABLE users",
			"SELECT * FROM users # DROP TABLE users",
		}
		for _, q := range queries {
			t.Run(q, func(t *testing.T) {
				err := validateSQLQuery(q)
				if err == nil {
					t.Errorf("Expected error for obfuscated query %q", q)
				}
			})
		}
	})

	t.Run("multiple statements blocked", func(t *testing.T) {
		queries := []string{
			"SELECT * FROM users; SELECT * FROM orders",
			"SELECT 1; SELECT 2; SELECT 3",
		}
		for _, q := range queries {
			t.Run(q, func(t *testing.T) {
				err := validateSQLQuery(q)
				if err == nil {
					t.Errorf("Expected error for multi-statement query %q", q)
				}
			})
		}
	})

	t.Run("trailing semicolon allowed", func(t *testing.T) {
		err := validateSQLQuery("SELECT * FROM users;")
		if err != nil {
			t.Errorf("Expected trailing semicolon to be allowed, got: %v", err)
		}
	})

	t.Run("SQL comments blocked", func(t *testing.T) {
		queries := []string{
			"SELECT * FROM users -- comment",
			"SELECT * FROM users /* comment */",
		}
		for _, q := range queries {
			t.Run(q, func(t *testing.T) {
				err := validateSQLQuery(q)
				if err == nil {
					t.Errorf("Expected error for query with comments %q", q)
				}
			})
		}
	})

	t.Run("null byte blocked", func(t *testing.T) {
		err := validateSQLQuery("SELECT * FROM users\x00")
		if err == nil {
			t.Error("Expected error for null byte")
		}
	})

	t.Run("blocked keyword in middle of word is allowed", func(t *testing.T) {
		// 'UPDATE' is a keyword, but 'UPDATER' is not
		err := validateSQLQuery("SELECT * FROM UPDATER_LOG")
		if err != nil {
			t.Errorf("Expected 'UPDATER_LOG' to be allowed (whole word match), got: %v", err)
		}
	})

	t.Run("cyrillic homoglyph DROP is detected", func(t *testing.T) {
		// normalizeQuery now converts Cyrillic о/р to uppercase 'O'/'P',
		// making the result "DROP" which is caught by the regex.
		err := validateSQLQuery("drор TABLE users") // Cyrillic lowercase о (U+043E) and р (U+0440)
		if err == nil {
			t.Error("Homoglyph DROP should now be detected by normalizeQuery")
		}
	})

	t.Run("empty query is allowed", func(t *testing.T) {
		err := validateSQLQuery("")
		if err != nil {
			t.Errorf("Expected empty query to be allowed, got: %v", err)
		}
	})
}
