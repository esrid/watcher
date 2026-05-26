// Package postgres provides database schema introspection for PostgreSQL databases.
package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/esrid/watcher/internal/schema/model"
)

// Inspector implements schema introspection for PostgreSQL.
type Inspector struct {
	DB *sql.DB
}

// New returns a new Postgres Inspector.
func New(db *sql.DB) *Inspector {
	return &Inspector{DB: db}
}

func (i *Inspector) Tables(ctx context.Context) ([]string, error) {
	rows, err := i.DB.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'")
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
	query := `
		SELECT
			c.column_name,
			c.data_type,
			CASE WHEN pk.column_name IS NOT NULL THEN '1' ELSE '0' END
		FROM information_schema.columns c
		LEFT JOIN (
			SELECT ku.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage ku ON tc.constraint_name = ku.constraint_name
			WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_name = $1 AND tc.table_schema = 'public'
		) pk ON c.column_name = pk.column_name
		WHERE c.table_name = $1 AND c.table_schema = 'public'
		ORDER BY c.ordinal_position
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
	return cols, nil
}

func (i *Inspector) Relations(ctx context.Context, tableName string) ([]string, error) {
	query := `
		SELECT
			kcu.column_name AS source_col,
			ccu.table_name AS target_table,
			ccu.column_name AS target_col
		FROM information_schema.table_constraints AS tc
		JOIN information_schema.key_column_usage AS kcu
			ON tc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage AS ccu
			ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_name = $1;
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
	return relations, nil
}

func (i *Inspector) Indexes(ctx context.Context, tableName string) ([]model.Index, error) {
	query := `
		SELECT
			i.relname                           AS index_name,
			ix.indisunique                      AS is_unique,
			ix.indpred IS NOT NULL              AS is_partial,
			a.attname                           AS column_name
		FROM pg_class t
		JOIN pg_index ix         ON t.oid = ix.indrelid
		JOIN pg_class i          ON i.oid = ix.indexrelid
		JOIN LATERAL unnest(ix.indkey)
			 WITH ORDINALITY AS x(attnum, ordinality) ON true
		JOIN pg_attribute a      ON a.attrelid = t.oid AND a.attnum = x.attnum
		WHERE t.relname = $1
		  AND t.relkind = 'r'
		  AND NOT ix.indisprimary
		ORDER BY i.relname, x.ordinality
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
		var isUnique, isPartial bool
		if err := rows.Scan(&indexName, &isUnique, &isPartial, &colName); err != nil {
			return nil, err
		}

		if currentIdx == nil || currentIdx.Name != indexName {
			if currentIdx != nil {
				indexes = append(indexes, *currentIdx)
			}
			currentIdx = &model.Index{
				Name:    indexName,
				Unique:  isUnique,
				Partial: isPartial,
			}
		}
		currentIdx.Columns = append(currentIdx.Columns, colName)
	}
	if currentIdx != nil {
		indexes = append(indexes, *currentIdx)
	}

	return indexes, nil
}

func (i *Inspector) ColumnMeta(ctx context.Context, tableName string) ([]model.ColumnMeta, error) {
	query := `
		SELECT
			c.column_name,
			c.data_type,
			c.is_nullable = 'NO' AS not_null,
			COALESCE(c.column_default, '') AS col_default,
			CASE WHEN pk.column_name IS NOT NULL THEN true ELSE false END AS is_pk,
			(
				uq.column_name IS NOT NULL
				OR EXISTS (
					SELECT 1
					FROM pg_class t
					JOIN pg_index ix ON t.oid = ix.indrelid
					JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ix.indkey[0]
					WHERE t.relname = c.table_name
					  AND t.relkind = 'r'
					  AND ix.indisunique
					  AND ix.indnatts = 1
					  AND a.attname = c.column_name
				)
			) AS is_unique
		FROM information_schema.columns c
		LEFT JOIN (
			SELECT ku.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage ku
				ON tc.constraint_name = ku.constraint_name
			   AND tc.table_schema = ku.table_schema
			WHERE tc.constraint_type = 'PRIMARY KEY'
			  AND tc.table_name = $1 AND tc.table_schema = 'public'
		) pk ON c.column_name = pk.column_name
		LEFT JOIN (
			SELECT ku.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage ku
				ON tc.constraint_name = ku.constraint_name
			   AND tc.table_schema = ku.table_schema
			WHERE tc.constraint_type = 'UNIQUE'
			  AND tc.table_name = $1 AND tc.table_schema = 'public'
		) uq ON c.column_name = uq.column_name
		WHERE c.table_name = $1 AND c.table_schema = 'public'
		ORDER BY c.ordinal_position
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
	return cols, nil
}
