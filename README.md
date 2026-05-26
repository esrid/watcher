# DB Watcher

[![Go Version](https://img.shields.io/github/go-mod/go-version/esrid/watcher)](https://go.dev/)
[![License](https://img.shields.io/github/license/esrid/watcher)](./LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/esrid/watcher.svg)](https://pkg.go.dev/github.com/esrid/watcher)

**DB Watcher** is a lightweight Go library for real-time database schema introspection. Mount an interactive ERD dashboard and schema change tracker directly onto your existing HTTP server — no external dependencies, no separate process.

<div align="center">
  <table>
    <tr>
      <td align="center"><img src="erd-dark.png" alt="ERD Dashboard" width="280" /></td>
      <td align="center"><img src="card-detail.png" alt="Table Card Detail" width="280" /></td>
      <td align="center"><img src="changes-view.png" alt="Schema Changes View" width="280" /></td>
    </tr>
    <tr>
      <td align="center"><sub>ERD Dashboard</sub></td>
      <td align="center"><sub>Table Card Detail</sub></td>
      <td align="center"><sub>Schema Changes</sub></td>
    </tr>
  </table>
</div>

---

## Features

- **Multi-driver** — SQLite, PostgreSQL, MySQL/MariaDB. Instrumented wrappers (`otelsql`, etc.) unwrapped automatically.
- **Rich introspection** — columns with `NOT NULL`, `DEFAULT`, `UNIQUE`, PKs, FKs, indexes (unique, composite, partial).
- **Interactive ERD** — draggable cards, animated FK paths, click to highlight relationships, double-click to collapse.
- **Built-in schema changes** — the Changes tab is included in the dashboard. No second handler needed — diffs are tracked and delivered by the same polling request as the schema.
- **Global search** — `⌘K` or `/` filters tables by name; filter stays active after closing the overlay.
- **Persistent layout** — card positions and collapsed states saved in `localStorage`.
- **Light & Dark mode** — follows OS preference, togglable, persisted across reloads.
- **JSON API** — every endpoint serves the HTML dashboard or raw JSON.

---

## Installation

```bash
go get github.com/esrid/watcher
```

---

## Setup

One handler is all you need:

```go
package main

import (
    "database/sql"
    "log"
    "net/http"

    "github.com/esrid/watcher"
    _ "github.com/mattn/go-sqlite3" // blank-import your driver
)

func main() {
    db, err := sql.Open("sqlite3", "app.db")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    inspector, err := watcher.NewInspector(db)
    if err != nil {
        log.Fatal(err)
    }

    http.HandleFunc("/_schema", watcher.HTTPHandler(inspector))

    log.Println("dashboard → http://localhost:8080/_schema")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

The dashboard polls itself every 1.5 seconds. Each poll takes a schema snapshot, computes the diff from the previous state, and delivers both in a single response. The ERD updates live. The **Changes** tab shows diffs as soon as the schema changes — no second handler, no background goroutine, nothing else to configure.

You can mount the handler on any path you want — `/`, `/_debug/schema`, `/internal/db`, anything. The dashboard automatically polls the path it is served from, so no configuration is needed on your side.

---

## Dashboard

### ERD tab

| Interaction | Action |
|---|---|
| Drag card header | Move the card |
| Click card header | Highlight FK relationships |
| Double-click card header | Collapse / expand card |
| `⌘K` or `/` | Open table search |
| `Esc` | Close search (filter stays active) |
| `Ctrl`/`⌘` + scroll | Zoom |
| `Ctrl`/`⌘` `+` / `-` / `0` | Zoom in / out / reset |

Card positions and collapsed states are saved in `localStorage` and restored on reload.

### Changes tab

Shows the diff between the last two polls: tables added or dropped, columns added or dropped, type changes, indexes added or dropped. The last non-empty diff is persisted — changes stay visible after the schema stabilizes and are only replaced when the next migration triggers a new diff.

---

## Supported Drivers

| Driver type | Package |
|---|---|
| `*sqlite3.SQLiteDriver` | `github.com/mattn/go-sqlite3` |
| `*sqlite.Driver` | `modernc.org/sqlite` |
| `*pq.Driver` | `github.com/lib/pq` |
| `*stdlib.Driver` | `github.com/jackc/pgx/v5/stdlib` |
| `*mysql.MySQLDriver` | `github.com/go-sql-driver/mysql` |

Wrappers that implement `Unwrap() driver.Driver` (e.g. `otelsql`) are resolved automatically. Unknown wrappers fall back to dialect probing.

---

## API Reference

### `NewInspector`

```go
func NewInspector(db *sql.DB) (Inspector, error)
```

Detects the driver and returns the matching `Inspector`. Returns an error if the driver is not recognised and dialect probing fails.

### `HTTPHandler`

```go
func HTTPHandler(inspector Inspector) http.HandlerFunc
```

Serves the dashboard. On each JSON poll it takes a schema snapshot, computes the diff, and returns everything in one payload. Responds with HTML for browser requests, JSON when `?format=json` is set or `Accept: application/json` is present.

JSON response shape:

```json
{
  "tables": [
    {
      "name": "users",
      "cols": [
        { "name": "id",    "type": "INTEGER", "pk": true,  "fk": false, "notNull": true,  "default": "",    "unique": false },
        { "name": "email", "type": "TEXT",    "pk": false, "fk": false, "notNull": true,  "default": "",    "unique": true  }
      ],
      "indexes": [
        { "name": "idx_active_users", "unique": false, "columns": ["created_at"], "partial": true }
      ]
    }
  ],
  "relations": [
    { "src": "orders", "srcCol": "user_id", "tgt": "users", "tgtCol": "id" }
  ],
  "diff": {
    "before": "2024-01-15T10:00:00Z",
    "after":  "2024-01-15T10:01:30Z",
    "addedTables":   ["audit_log"],
    "droppedTables": [],
    "modified": [
      {
        "table":          "users",
        "addedColumns":   ["deleted_at"],
        "droppedColumns": [],
        "typeChanges":    [],
        "addedIndexes":   ["idx_users_deleted_at"],
        "droppedIndexes": []
      }
    ]
  }
}
```

`diff` is omitted from the response until at least two polls have occurred (i.e. there is a before and after to compare). After that it is always present, even when empty.

### Advanced: `ChangesHandler`

```go
func ChangesHandler(d *Differ) http.HandlerFunc
```

For use-cases that need diff data outside the dashboard — scripts, CI health checks, other clients. Build a `Differ` manually and mount this handler wherever suits:

```go
differ := watcher.NewDiffer(inspector)
differ.Watch(context.Background(), 10*time.Second, nil)

http.HandleFunc("/api/schema/diff", watcher.ChangesHandler(differ))
```

- `GET` — returns the current `SchemaDiff` as JSON.
- `POST` — takes a new snapshot, returns the resulting diff.

---

## Testing

```bash
# Unit tests
go test ./...

# Integration tests — requires Docker (Testcontainers, real Postgres and MySQL)
go test -tags=integration -v ./...
```

---

## License

MIT — see [LICENSE](LICENSE).
