package api

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func (c *Client) ExecuteSQL(dbPath, query string) ([]string, [][]string, error) {
	if dbPath == "" || query == "" {
		return nil, nil, fmt.Errorf("DB path and query are required")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	rows, err := db.Query(query)
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
