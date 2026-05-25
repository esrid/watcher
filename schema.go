// Package watcher provides real-time, interactive database schema introspection
// and visualization for SQLite, PostgreSQL, and MySQL databases.
package watcher

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/esrid/watcher/internal/schema/mysql"
	"github.com/esrid/watcher/internal/schema/postgres"
	"github.com/esrid/watcher/internal/schema/sqlite"
)

//go:embed ui.html
var uiFS embed.FS

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
}

// NewInspector automatically detects the driver type of the provided *sql.DB connection
// and returns the corresponding Inspector implementation.
//
// Supported drivers are:
//   - SQLite: CGO "*sqlite3.SQLiteDriver" (github.com/mattn/go-sqlite3) and pure Go "*sqlite.Driver" (modernc.org/sqlite)
//   - PostgreSQL: "*pq.Driver" (github.com/lib/pq) and "*stdlib.Driver" (github.com/jackc/pgx)
//   - MySQL / MariaDB: "*mysql.MySQLDriver" (github.com/go-sql-driver/mysql)
//
// Returns an error if the database driver is unsupported.
func NewInspector(db *sql.DB) (Inspector, error) {
	driverType := fmt.Sprintf("%T", db.Driver())
	switch driverType {
	case "*sqlite3.SQLiteDriver", "*sqlite.Driver":
		return sqlite.New(db), nil
	case "*pq.Driver", "*stdlib.Driver":
		return postgres.New(db), nil
	case "*mysql.MySQLDriver":
		return mysql.New(db), nil
	default:
		return nil, fmt.Errorf("unsupported driver: %s", driverType)
	}
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

// JSON-serializable types for the template payload.
type jsonCol struct {
	Name string `json:"name"`
	Type string `json:"type"`
	PK   bool   `json:"pk"`
	FK   bool   `json:"fk"`
}

type jsonTable struct {
	Name string    `json:"name"`
	Cols []jsonCol `json:"cols"`
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
	tmpl := template.Must(template.ParseFS(uiFS, "ui.html"))

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

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
			rawCols, _ := inspector.Columns(ctx, t)
			cols := make([]jsonCol, 0, len(rawCols))
			for _, c := range rawCols {
				p := strings.Split(c, "|")
				if len(p) != 3 {
					continue
				}
				cols = append(cols, jsonCol{
					Name: p[0],
					Type: normalizeType(p[1]),
					PK:   p[2] == "1",
					FK:   fkIndex[t][p[0]],
				})
			}
			tbls = append(tbls, jsonTable{Name: t, Cols: cols})
		}

		payload := struct {
			Tables    []jsonTable `json:"tables"`
			Relations []jsonRel   `json:"relations"`
		}{Tables: tbls, Relations: rels}

		b, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, "erreur sérialisation", http.StatusInternalServerError)
			return
		}

		if r.URL.Query().Get("format") == "json" || strings.Contains(r.Header.Get("Accept"), "application/json") {
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
