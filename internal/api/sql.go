package api

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// dangerousKeywords are SQL commands that could be harmful if misused
var dangerousKeywords = []string{
	"DROP", "DELETE", "TRUNCATE", "ALTER TABLE",
	"EXEC", "EXECUTE", "UNION", "INSERT", "UPDATE",
}

// normalizeQuery applies Unicode normalization to prevent bypasses
func normalizeQuery(query string) string {
	// NFKC normalization handles compatibility characters
	normalized := strings.Map(func(r rune) rune {
		// Replace common homoglyphs
		switch r {
		case '\u0430': // Cyrillic 'a' -> Latin 'a'
			return 'a'
		case '\u0435': // Cyrillic 'e' -> Latin 'e'
			return 'e'
		case '\u043E': // Cyrillic 'o' -> Latin 'o'
			return 'o'
		case '\u0440': // Cyrillic 'p' -> Latin 'p'
			return 'p'
		case '\u0441': // Cyrillic 'c' -> Latin 'c'
			return 'c'
		case '\u0445': // Cyrillic 'x' -> Latin 'x'
			return 'x'
		case '\u0456': // Cyrillic 'i' -> Latin 'i'
			return 'i'
		}
		// Filter out non-printable and special control characters
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return unicode.ToUpper(r)
	}, query)
	return normalized
}

// validateSQLQuery checks if the query contains potentially dangerous keywords
// Returns an error if the query is not allowed
func validateSQLQuery(query string) error {
	// Normalize the query to prevent Unicode bypasses
	normalized := normalizeQuery(query)

	// Check for dangerous keywords
	for _, keyword := range dangerousKeywords {
		// Use regex to match whole words only (case-insensitive already handled by normalization)
		pattern := `\b` + regexp.QuoteMeta(strings.ToUpper(keyword)) + `\b`
		matched, err := regexp.MatchString(pattern, normalized)
		if err != nil {
			return fmt.Errorf("error validating query: %w", err)
		}
		if matched {
			return fmt.Errorf("query contains forbidden keyword: %s", keyword)
		}
	}

	// Check for obfuscated keywords using comment injection
	// Remove common comment patterns and re-check
	deobfuscated := regexp.MustCompile(`(?i)/\*.*?\*/`).ReplaceAllString(normalized, "")
	deobfuscated = regexp.MustCompile(`(?i)--[^\n]*`).ReplaceAllString(deobfuscated, "")
	deobfuscated = regexp.MustCompile(`(?i)#`).ReplaceAllString(deobfuscated, "")

	for _, keyword := range dangerousKeywords {
		pattern := `\b` + regexp.QuoteMeta(strings.ToUpper(keyword)) + `\b`
		matched, _ := regexp.MatchString(pattern, deobfuscated)
		if matched {
			return fmt.Errorf("query contains forbidden keyword (possibly obfuscated): %s", keyword)
		}
	}

	// Block multiple statements (potential for injection)
	if strings.Contains(normalized, ";") && !strings.HasSuffix(strings.TrimSpace(normalized), ";") {
		// Allow single trailing semicolon, but block mid-query semicolons
		return fmt.Errorf("multiple statements not allowed")
	}

	// Block SQL comments which can be used to bypass filters
	if strings.Contains(normalized, "--") || strings.Contains(normalized, "/*") || strings.Contains(normalized, "*/") {
		return fmt.Errorf("SQL comments not allowed")
	}

	// Block NULL byte injection
	if strings.Contains(query, "\x00") {
		return fmt.Errorf("null bytes not allowed")
	}

	return nil
}

func (c *Client) getDB(driver, connStr string) (*sql.DB, error) {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()

	if entry, ok := c.dbPool[connStr]; ok {
		// Ping with timeout to prevent hanging
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := entry.db.PingContext(pingCtx)
		cancel()
		if err == nil {
			// Update last used time and move to front of LRU
			entry.lastUsed = time.Now()
			// Move to front of LRU list
			for e := c.dbOrder.Front(); e != nil; e = e.Next() {
				if e.Value == connStr {
					c.dbOrder.MoveToFront(e)
					break
				}
			}
			return entry.db, nil
		}
		// Connection is dead, close and remove
		entry.db.Close()
		delete(c.dbPool, connStr)
		// Remove from LRU list
		for e := c.dbOrder.Front(); e != nil; e = e.Next() {
			if e.Value == connStr {
				c.dbOrder.Remove(e)
				break
			}
		}
	}

	// Check if we need to evict an old connection (pool full)
	if len(c.dbPool) >= dbMaxPoolSize {
		// Evict least recently used
		oldest := c.dbOrder.Back()
		if oldest != nil {
			oldConnStr := oldest.Value.(string)
			if oldEntry, ok := c.dbPool[oldConnStr]; ok {
				oldEntry.db.Close()
				delete(c.dbPool, oldConnStr)
			}
			c.dbOrder.Remove(oldest)
		}
	}

	db, err := sql.Open(driver, connStr)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	c.dbPool[connStr] = &dbConnEntry{
		db:        db,
		lastUsed:  now,
		createdAt: now,
	}
	c.dbOrder.PushFront(connStr)
	return db, nil
}

func (c *Client) ExecuteSQL(ctx context.Context, driver, connStr, query string) ([]string, [][]string, error) {
	if connStr == "" || query == "" {
		return nil, nil, fmt.Errorf("Connection string and query are required")
	}

	// Validate query for security
	if err := validateSQLQuery(query); err != nil {
		return nil, nil, fmt.Errorf("query validation failed: %w", err)
	}

	if driver == "" {
		driver = "sqlite"
		// Use HasPrefix for scheme detection to avoid substring matching issues
		if strings.HasPrefix(connStr, "postgres://") || strings.HasPrefix(connStr, "postgresql://") {
			driver = "postgres"
		} else if strings.Contains(connStr, "sslmode=") {
			// sslmode is postgres-specific parameter
			driver = "postgres"
		} else if strings.HasPrefix(connStr, "mysql://") || strings.HasPrefix(connStr, "mysql+") {
			// Explicit mysql protocol
			driver = "mysql"
		} else if strings.Contains(connStr, "@tcp(") || strings.Contains(connStr, "@unix(") {
			// MySQL specific connection format
			driver = "mysql"
		}
		// Default remains "sqlite" for file paths and other cases
	}

	db, err := c.getDB(driver, connStr)
	if err != nil {
		return nil, nil, err
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	var results [][]string
	for rows.Next() {
		row := make([]interface{}, len(columns))
		rowPtrs := make([]interface{}, len(columns))
		for i := range row {
			rowPtrs[i] = &row[i]
		}

		if err := rows.Scan(rowPtrs...); err != nil {
			return nil, nil, err
		}

		var rowStrs []string
		for _, val := range row {
			if val == nil {
				rowStrs = append(rowStrs, "NULL")
			} else {
				rowStrs = append(rowStrs, fmt.Sprintf("%v", val))
			}
		}
		results = append(results, rowStrs)
	}

	return columns, results, nil
}
