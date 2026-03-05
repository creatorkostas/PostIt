package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

func (c *Client) getDB(driver, connStr string) (*sql.DB, error) {
	c.dbMu.Lock()
	defer c.dbMu.Unlock()

	if db, ok := c.dbPool[connStr]; ok {
		if err := db.Ping(); err == nil {
			return db, nil
		}
		db.Close()
		delete(c.dbPool, connStr)
	}

	db, err := sql.Open(driver, connStr)
	if err != nil {
		return nil, err
	}
	c.dbPool[connStr] = db
	return db, nil
}

func (c *Client) ExecuteSQL(ctx context.Context, connStr, query string) ([]string, [][]string, error) {
	if connStr == "" || query == "" {
		return nil, nil, fmt.Errorf("Connection string and query are required")
	}

	driver := "sqlite"
	if strings.Contains(connStr, "postgres://") || strings.Contains(connStr, "sslmode=") {
		driver = "postgres"
	} else if strings.Contains(connStr, "@tcp(") || strings.Contains(connStr, "mysql") {
		driver = "mysql"
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
