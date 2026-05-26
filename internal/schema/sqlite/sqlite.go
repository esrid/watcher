// Package sqlite provides database schema introspection for SQLite databases.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/esrid/watcher/internal/schema/model"
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

func (i *Inspector) Indexes(ctx context.Context, tableName string) ([]model.Index, error) {
	rows, err := i.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%q)", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []model.Index
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, err
		}

		if origin == "pk" {
			continue // skip primary keys
		}

		infoRows, err := i.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%q)", name))
		if err != nil {
			return nil, err
		}
		
		var columns []string
		for infoRows.Next() {
			var seqno, cid int
			var colName string
			if err := infoRows.Scan(&seqno, &cid, &colName); err == nil {
				columns = append(columns, colName)
			}
		}
		infoRows.Close()

		indexes = append(indexes, model.Index{
			Name:    name,
			Unique:  unique != 0,
			Columns: columns,
			Partial: partial != 0,
		})
	}
	return indexes, nil
}

func (i *Inspector) ColumnMeta(ctx context.Context, tableName string) ([]model.ColumnMeta, error) {
	rows, err := i.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	uniqueCols, err := i.uniqueColumns(ctx, tableName)
	if err != nil {
		return nil, err
	}

	var cols []model.ColumnMeta
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}

		isPK := pk > 0
		if typ == "" {
			typ = "TEXT"
		}

		defaultVal := ""
		if dflt.Valid {
			defaultVal = dflt.String
		}

		cols = append(cols, model.ColumnMeta{
			Name:    name,
			Type:    typ,
			PK:      isPK,
			NotNull: notnull != 0,
			Default: defaultVal,
			Unique:  uniqueCols[name] || isPK,
		})
	}
	return cols, nil
}

func (i *Inspector) uniqueColumns(ctx context.Context, tableName string) (map[string]bool, error) {
	rows, err := i.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%q)", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	uniqueCols := make(map[string]bool)
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, err
		}

		if unique != 0 {
			infoRows, err := i.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%q)", name))
			if err != nil {
				return nil, err
			}
			
			var cols []string
			for infoRows.Next() {
				var seqno, cid int
				var colName string
				if err := infoRows.Scan(&seqno, &cid, &colName); err == nil {
					cols = append(cols, colName)
				}
			}
			infoRows.Close()

			if len(cols) == 1 {
				uniqueCols[cols[0]] = true
			}
		}
	}
	return uniqueCols, nil
}
