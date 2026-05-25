// Package sqlite provides database schema introspection for SQLite databases.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// Inspector implements schema introspection for SQLite.
type Inspector struct {
	DB *sql.DB
}

// New returns a new SQLite Inspector.
func New(db *sql.DB) *Inspector {
	return &Inspector{DB: db}
}

func (i *Inspector) Tables(ctx context.Context) ([]string, error) {
	rows, err := i.DB.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, nil
}

func (i *Inspector) Columns(ctx context.Context, tableName string) ([]string, error) {
	rows, err := i.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			continue
		}
		isPK := "0"
		if pk > 0 {
			isPK = "1"
		}
		if typ == "" {
			typ = "TEXT"
		}
		cols = append(cols, fmt.Sprintf("%s|%s|%s", name, typ, isPK))
	}
	return cols, nil
}

func (i *Inspector) Relations(ctx context.Context, tableName string) ([]string, error) {
	query := fmt.Sprintf("PRAGMA foreign_key_list(%q)", tableName)
	rows, err := i.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []string
	for rows.Next() {
		var id, seq int
		var refTable, fromCol, toCol, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &refTable, &fromCol, &toCol, &onUpdate, &onDelete, &match); err != nil {
			continue // on ignore en cas d'erreur de parsing
		}
		relations = append(relations, fmt.Sprintf("%s -> %s.%s", fromCol, refTable, toCol))
	}
	return relations, nil
}
