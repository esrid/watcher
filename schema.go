// Package watcher provides real-time, interactive database schema introspection
// and visualization for SQLite, PostgreSQL, and MySQL databases.
package watcher

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/esrid/watcher/internal/schema/model"
	"github.com/esrid/watcher/internal/schema/mysql"
	"github.com/esrid/watcher/internal/schema/postgres"
	"github.com/esrid/watcher/internal/schema/sqlite"
)

//go:embed ui.html
var uiFS embed.FS

// Index describes one database index on a table.
type Index = model.Index

// ColumnMeta extends raw column data with constraint information.
type ColumnMeta = model.ColumnMeta

// Inspector defines the contract for database schema introspection.
// Implementations query database metadata catalog schemas to extract table
// structures, columns, and foreign key relationships.
type Inspector interface {
	// Tables returns a list of all user-defined table names.
	Tables(ctx context.Context) ([]string, error)
	// Columns returns column descriptors for a table in "name|type|pk" format.
	Columns(ctx context.Context, tableName string) ([]string, error)
	// Relations returns foreign-key descriptors for a table in "fromCol -> targetTable.targetCol" format.
	Relations(ctx context.Context, tableName string) ([]string, error)
	// Indexes returns a list of all indexes defined on a table (except primary keys).
	Indexes(ctx context.Context, tableName string) ([]Index, error)
	// ColumnMeta returns column descriptors with extra constraints.
	ColumnMeta(ctx context.Context, tableName string) ([]ColumnMeta, error)
}

// NewInspector automatically detects the driver type of the provided *sql.DB connection
// and returns the corresponding Inspector implementation.
//
// Supported drivers are:
//   - SQLite: CGO "*sqlite3.SQLiteDriver" (github.com/mattn/go-sqlite3) and pure Go "*sqlite.Driver" (modernc.org/sqlite)
//   - PostgreSQL: "*pq.Driver" (github.com/lib/pq) and "*stdlib.Driver" (github.com/jackc/pgx)
//   - MySQL / MariaDB: "*mysql.MySQLDriver" (github.com/go-sql-driver/mysql)
//
// Instrumented wrappers such as otelsql are also supported: NewInspector first
// attempts to unwrap the driver via the Unwrap() driver.Driver interface, and
// if that is not available it falls back to dialect probing (one lightweight
// query per candidate dialect).
//
// Returns an error if the database driver is unsupported.
func NewInspector(db *sql.DB) (Inspector, error) {
	driverType := fmt.Sprintf("%T", unwrapDriver(db.Driver()))
	switch driverType {
	case "*sqlite3.SQLiteDriver", "*sqlite.Driver":
		return sqlite.New(db), nil
	case "*pq.Driver", "*stdlib.Driver":
		return postgres.New(db), nil
	case "*mysql.MySQLDriver":
		return mysql.New(db), nil
	default:
		// Opaque wrapper (e.g. otelsql whose inner driver field is unexported):
		// probe the dialect by running a driver-specific no-op query.
		return probeDialect(db, driverType)
	}
}

// unwrapDriver recursively unwraps instrumented/middleware drivers that expose
// their inner driver via the Unwrap() driver.Driver method.
func unwrapDriver(d driver.Driver) driver.Driver {
	type unwrapper interface {
		Unwrap() driver.Driver
	}
	for {
		u, ok := d.(unwrapper)
		if !ok {
			return d
		}
		inner := u.Unwrap()
		if inner == d {
			return d
		}
		d = inner
	}
}

// probeDialect detects the SQL dialect by running a driver-specific query.
// Used as fallback when the driver type cannot be resolved via unwrapping.
func probeDialect(db *sql.DB, driverType string) (Inspector, error) {
	ctx := context.Background()
	var v string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v); err == nil {
		return sqlite.New(db), nil
	}
	if err := db.QueryRowContext(ctx, "SELECT pg_catalog.version()").Scan(&v); err == nil {
		return postgres.New(db), nil
	}
	if err := db.QueryRowContext(ctx, "SELECT @@version_comment").Scan(&v); err == nil {
		return mysql.New(db), nil
	}
	return nil, fmt.Errorf("unsupported driver: %s", driverType)
}

func InspectDatabase(ctx context.Context, inspector Inspector) error {
	tables, err := inspector.Tables(ctx)
	if err != nil {
		return fmt.Errorf("erreur liste tables: %w", err)
	}
	for _, t := range tables {
		fmt.Printf("- %s\n", t)
		relations, _ := inspector.Relations(ctx, t)
		for _, r := range relations {
			fmt.Printf("  ↳ Relation : %s\n", r)
		}
	}
	return nil
}

// normalizeType cleans up verbose DB type names for display.
func normalizeType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case strings.HasPrefix(t, "character varying"):
		return "varchar"
	case t == "integer" || t == "int4":
		return "int"
	case t == "bigint" || t == "int8":
		return "bigint"
	case t == "boolean":
		return "bool"
	case strings.HasPrefix(t, "timestamp without"):
		return "timestamp"
	case strings.HasPrefix(t, "timestamp with"):
		return "timestamptz"
	case strings.HasPrefix(t, "double precision"):
		return "float8"
	case t == "":
		return "text"
	}
	return t
}

type jsonIndex struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
	Partial bool     `json:"partial"`
}

type jsonTable struct {
	Name    string       `json:"name"`
	Cols    []ColumnMeta `json:"cols"`
	Indexes []jsonIndex  `json:"indexes"`
}

type jsonRel struct {
	Src    string `json:"src"`
	SrcCol string `json:"srcCol"`
	Tgt    string `json:"tgt"`
	TgtCol string `json:"tgtCol"`
}

// HTTPHandler returns an http.HandlerFunc that serves the schema watcher dashboard.
//
// Behavior is polymorphic:
//   - Web browser / HTML view: Serves the interactive drag-and-drop dashboard.
//   - Machine / JSON view: Serves the raw schema payload when requested via the "?format=json"
//     query parameter or an "Accept: application/json" header.
func HTTPHandler(inspector Inspector) http.HandlerFunc {
	tmpl   := template.Must(template.ParseFS(uiFS, "ui.html"))
	differ := NewDiffer(inspector)

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		wantsJSON := r.URL.Query().Get("format") == "json" ||
			strings.Contains(r.Header.Get("Accept"), "application/json")

		// Snapshot on every JSON poll so the diff stays current.
		if wantsJSON {
			_, _ = differ.Snapshot(ctx)
		}

		tables, err := inspector.Tables(ctx)
		if err != nil {
			http.Error(w, "erreur lecture tables", http.StatusInternalServerError)
			return
		}

		fkIndex := make(map[string]map[string]bool, len(tables))
		for _, t := range tables {
			fkIndex[t] = map[string]bool{}
		}

		var rels []jsonRel
		for _, t := range tables {
			raw, _ := inspector.Relations(ctx, t)
			for _, r := range raw {
				parts := strings.Split(r, " -> ")
				if len(parts) != 2 {
					continue
				}
				fromCol := parts[0]
				fkIndex[t][fromCol] = true
				tp := strings.Split(parts[1], ".")
				if len(tp) == 2 {
					rels = append(rels, jsonRel{Src: t, SrcCol: fromCol, Tgt: tp[0], TgtCol: tp[1]})
				}
			}
		}

		var tbls []jsonTable
		for _, t := range tables {
			cols, _ := inspector.ColumnMeta(ctx, t)
			for idx := range cols {
				cols[idx].Type = normalizeType(cols[idx].Type)
				cols[idx].FK = fkIndex[t][cols[idx].Name]
			}

			idxs, _ := inspector.Indexes(ctx, t)
			jsonIdxs := make([]jsonIndex, len(idxs))
			for idx, item := range idxs {
				jsonIdxs[idx] = jsonIndex{
					Name:    item.Name,
					Unique:  item.Unique,
					Columns: item.Columns,
					Partial: item.Partial,
				}
			}

			tbls = append(tbls, jsonTable{
				Name:    t,
				Cols:    cols,
				Indexes: jsonIdxs,
			})
		}

		var schDiff *SchemaDiff
		if d, ok := differ.Diff(); ok {
			schDiff = &d
		}

		payload := struct {
			Tables    []jsonTable  `json:"tables"`
			Relations []jsonRel    `json:"relations"`
			Diff      *SchemaDiff  `json:"diff,omitempty"`
		}{Tables: tbls, Relations: rels, Diff: schDiff}

		b, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, "erreur sérialisation", http.StatusInternalServerError)
			return
		}

		if wantsJSON {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, struct{ JSON template.JS }{JSON: template.JS(b)}); err != nil {
			http.Error(w, "erreur rendu HTML", http.StatusInternalServerError)
		}
	}
}

// ChangesHandler serves schema diffs.
//
// GET  → returns the latest SchemaDiff as JSON (empty diff if < 2 snapshots).
// POST → takes a new snapshot, then returns the resulting SchemaDiff as JSON.
func ChangesHandler(d *Differ) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			if _, err := d.Snapshot(r.Context()); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprintf(w, `{"error": %q}`, err.Error())
				return
			}
		} else if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		diffRes, ok := d.Diff()
		if !ok {
			_, _ = w.Write([]byte(`{}`))
			return
		}

		b, err := json.Marshal(diffRes)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": "erreur sérialisation"}`))
			return
		}
		_, _ = w.Write(b)
	}
}
