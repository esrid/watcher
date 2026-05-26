// Package mysql provides database schema introspection for MySQL and MariaDB databases.
package mysql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/esrid/watcher/internal/schema/model"
)

// Inspector implements schema introspection for MySQL / MariaDB.
type Inspector struct {
	DB *sql.DB
}

// New returns a new MySQL Inspector.
func New(db *sql.DB) *Inspector {
	return &Inspector{DB: db}
}

// Tables returns all user-defined table names in the current database.
func (i *Inspector) Tables(ctx context.Context) ([]string, error) {
	rows, err := i.DB.QueryContext(ctx, `
		SELECT TABLE_NAME
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME
	`)
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
	return tables, rows.Err()
}

// Columns returns column descriptors for tableName in "name|type|pk" format,
// where pk is "1" for primary-key columns, "0" otherwise.
func (i *Inspector) Columns(ctx context.Context, tableName string) ([]string, error) {
	query := `
		SELECT
			c.COLUMN_NAME,
			c.DATA_TYPE,
			CASE WHEN c.COLUMN_KEY = 'PRI' THEN '1' ELSE '0' END AS is_pk
		FROM information_schema.COLUMNS c
		WHERE c.TABLE_SCHEMA = DATABASE()
		  AND c.TABLE_NAME  = ?
		ORDER BY c.ORDINAL_POSITION
	`
	rows, err := i.DB.QueryContext(ctx, query, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name, typ, pk string
		if err := rows.Scan(&name, &typ, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, fmt.Sprintf("%s|%s|%s", name, typ, pk))
	}
	return cols, rows.Err()
}

// Relations returns foreign-key descriptors for tableName in "fromCol -> targetTable.targetCol" format.
func (i *Inspector) Relations(ctx context.Context, tableName string) ([]string, error) {
	query := `
		SELECT
			kcu.COLUMN_NAME        AS source_col,
			kcu.REFERENCED_TABLE_NAME  AS target_table,
			kcu.REFERENCED_COLUMN_NAME AS target_col
		FROM information_schema.KEY_COLUMN_USAGE kcu
		JOIN information_schema.TABLE_CONSTRAINTS tc
			ON  tc.CONSTRAINT_NAME   = kcu.CONSTRAINT_NAME
			AND tc.TABLE_SCHEMA      = kcu.TABLE_SCHEMA
			AND tc.TABLE_NAME        = kcu.TABLE_NAME
		WHERE tc.CONSTRAINT_TYPE = 'FOREIGN KEY'
		  AND kcu.TABLE_SCHEMA   = DATABASE()
		  AND kcu.TABLE_NAME     = ?
		ORDER BY kcu.ORDINAL_POSITION
	`
	rows, err := i.DB.QueryContext(ctx, query, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []string
	for rows.Next() {
		var sourceCol, targetTable, targetCol string
		if err := rows.Scan(&sourceCol, &targetTable, &targetCol); err != nil {
			return nil, err
		}
		relations = append(relations, fmt.Sprintf("%s -> %s.%s", sourceCol, targetTable, targetCol))
	}
	return relations, rows.Err()
}

func (i *Inspector) Indexes(ctx context.Context, tableName string) ([]model.Index, error) {
	query := `
		SELECT INDEX_NAME, NON_UNIQUE = 0 AS is_unique, COLUMN_NAME
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME   = ?
		  AND INDEX_NAME  != 'PRIMARY'
		ORDER BY INDEX_NAME, SEQ_IN_INDEX
	`
	rows, err := i.DB.QueryContext(ctx, query, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []model.Index
	var currentIdx *model.Index

	for rows.Next() {
		var indexName, colName string
		var isUnique bool
		if err := rows.Scan(&indexName, &isUnique, &colName); err != nil {
			return nil, err
		}

		if currentIdx == nil || currentIdx.Name != indexName {
			if currentIdx != nil {
				indexes = append(indexes, *currentIdx)
			}
			currentIdx = &model.Index{
				Name:    indexName,
				Unique:  isUnique,
				Partial: false,
			}
		}
		currentIdx.Columns = append(currentIdx.Columns, colName)
	}
	if currentIdx != nil {
		indexes = append(indexes, *currentIdx)
	}

	return indexes, rows.Err()
}

func (i *Inspector) ColumnMeta(ctx context.Context, tableName string) ([]model.ColumnMeta, error) {
	query := `
		SELECT
			c.COLUMN_NAME,
			c.DATA_TYPE,
			c.IS_NULLABLE = 'NO' AS not_null,
			COALESCE(c.COLUMN_DEFAULT, '') AS col_default,
			c.COLUMN_KEY = 'PRI' AS is_pk,
			(
				c.COLUMN_KEY = 'UNI'
				OR EXISTS (
					SELECT 1
					FROM information_schema.STATISTICS s
					WHERE s.TABLE_SCHEMA = DATABASE()
					  AND s.TABLE_NAME = c.TABLE_NAME
					  AND s.COLUMN_NAME = c.COLUMN_NAME
					  AND s.NON_UNIQUE = 0
					  AND (
						  SELECT COUNT(*)
						  FROM information_schema.STATISTICS s2
						  WHERE s2.TABLE_SCHEMA = s.TABLE_SCHEMA
							AND s2.TABLE_NAME = s.TABLE_NAME
							AND s2.INDEX_NAME = s.INDEX_NAME
					  ) = 1
				)
			) AS is_unique
		FROM information_schema.COLUMNS c
		WHERE c.TABLE_SCHEMA = DATABASE() AND c.TABLE_NAME = ?
		ORDER BY c.ORDINAL_POSITION
	`
	rows, err := i.DB.QueryContext(ctx, query, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []model.ColumnMeta
	for rows.Next() {
		var name, typ string
		var notNull, isPK, isUnique bool
		var colDefault string
		if err := rows.Scan(&name, &typ, &notNull, &colDefault, &isPK, &isUnique); err != nil {
			return nil, err
		}
		cols = append(cols, model.ColumnMeta{
			Name:    name,
			Type:    typ,
			PK:      isPK,
			NotNull: notNull,
			Default: colDefault,
			Unique:  isUnique || isPK,
		})
	}
	return cols, rows.Err()
}
