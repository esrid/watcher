# 👁️ DB Watcher

[![Go Version](https://img.shields.io/github/go-mod/go-version/esrid/watcher)](https://go.dev/)
[![License](https://img.shields.io/github/license/esrid/watcher)](./LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/esrid/watcher.svg)](https://pkg.go.dev/github.com/esrid/watcher)

**DB Watcher** is a lightweight, zero-dependency schema introspection library for Go. It allows developers to mount a beautiful, real-time database schema inspector dashboard directly onto their existing web application in seconds.

---

## ✨ Features

* **Multi-DB & Multi-Driver Support**: Out-of-the-box support for SQLite (both CGO `go-sqlite3` and pure-Go `modernc.org/sqlite`), PostgreSQL (`lib/pq` and `pgx/v5`), and MySQL (`go-sql-driver/mysql`).
* **Real-time Schema Monitoring**: The web dashboard automatically polls the backend and updates the schema layout in real-time when tables, columns, or foreign keys change, with **zero page refreshes**.
* **Persistent Layout Customization**: Drag-and-drop tables anywhere to organize your ERD layout. Dragged positions are preserved across updates and page reloads via `localStorage`.
* **Interactive Relationship Mapping**: Click on any table header to instantly highlight its incoming and outgoing foreign key relationships.
* **Polymorphic HTTP Handler**: A single HTTP handler serves both the rich HTML dashboard and a raw JSON API (`?format=json`), avoiding routing conflicts in your application.

---

## 🚀 Getting Started

### 1. Install

```bash
go get github.com/esrid/watcher
```

### 2. Quick Example

Instantiate your database connection, pass it to `NewInspector`, and mount the handler at any route you like:

```go
package main

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/esrid/watcher"

	_ "github.com/mattn/go-sqlite3" // or pure-Go _ "modernc.org/sqlite"
)

func main() {
	// Connect to your database
	db, err := sql.Open("sqlite3", "app.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// 1. Initialize the schema inspector
	inspector, err := watcher.NewInspector(db)
	if err != nil {
		panic(err)
	}

	// 2. Register the debug dashboard route
	// You can mount this at any custom endpoint (e.g., behind auth middlewares)
	http.HandleFunc("/_debug/schema", watcher.HTTPHandler(inspector))

	// Your business routes
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Welcome to my application!"))
	})

	fmt.Println("🚀 Dashboard active at http://localhost:8080/_debug/schema")
	http.ListenAndServe(":8080", nil)
}
```

---

## 🛠️ API Reference

### `watcher.NewInspector`

```go
func NewInspector(db *sql.DB) (Inspector, error)
```
Detects the underlying database driver type and returns a concrete `Inspector` instance. Supported drivers are:
- `*sqlite3.SQLiteDriver` (`github.com/mattn/go-sqlite3`)
- `*sqlite.Driver` (`modernc.org/sqlite` pure Go SQLite)
- `*pq.Driver` (`github.com/lib/pq` PostgreSQL)
- `*stdlib.Driver` (`github.com/jackc/pgx` PostgreSQL stdlib)
- `*mysql.MySQLDriver` (`github.com/go-sql-driver/mysql` MySQL / MariaDB)

### `watcher.HTTPHandler`

```go
func HTTPHandler(inspector Inspector) http.HandlerFunc
```
An `http.Handler` that serves:
- **HTML Dashboard**: For web browsers and standard `GET` requests.
- **JSON Payload**: When requested with `?format=json` or `Accept: application/json` headers. Returns:
  ```json
  {
    "tables": [
      {
        "name": "users",
        "cols": [
          {"name": "id", "type": "int", "pk": true, "fk": false},
          {"name": "email", "type": "varchar", "pk": false, "fk": false}
        ]
      }
    ],
    "relations": [
      {
        "src": "orders",
        "srcCol": "user_id",
        "tgt": "users",
        "tgtCol": "id"
      }
    ]
  }
  ```

---

## 🧪 Development & Testing

DB Watcher uses [Testcontainers for Go](https://golang.testcontainers.org/) to run high-fidelity integration tests against real instances of PostgreSQL and MySQL.

### Run Unit Tests
```bash
go test ./...
```

### Run Real-Database Integration Tests
*(Requires Docker to be running locally)*
```bash
go test -tags=integration -v ./...
```

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
# watcher
