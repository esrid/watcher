package main

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/esrid/watcher/pkg/schema"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	// --- 1. L'utilisateur configure sa base de données ---
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	stmts := []string{
		// auth
		`CREATE TABLE roles (id INTEGER PRIMARY KEY, name TEXT NOT NULL, description TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL, name TEXT, role_id INTEGER, created_at TEXT, FOREIGN KEY(role_id) REFERENCES roles(id))`,
		`CREATE TABLE sessions (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, token TEXT NOT NULL, expires_at TEXT, FOREIGN KEY(user_id) REFERENCES users(id))`,
		`CREATE TABLE oauth_accounts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, provider TEXT, provider_id TEXT, FOREIGN KEY(user_id) REFERENCES users(id))`,
		// billing
		`CREATE TABLE plans (id INTEGER PRIMARY KEY, name TEXT, price INTEGER, max_seats INTEGER)`,
		`CREATE TABLE organizations (id INTEGER PRIMARY KEY, name TEXT NOT NULL, plan_id INTEGER, owner_id INTEGER, FOREIGN KEY(plan_id) REFERENCES plans(id), FOREIGN KEY(owner_id) REFERENCES users(id))`,
		`CREATE TABLE org_members (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, user_id INTEGER NOT NULL, role TEXT, FOREIGN KEY(org_id) REFERENCES organizations(id), FOREIGN KEY(user_id) REFERENCES users(id))`,
		`CREATE TABLE invoices (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, amount INTEGER, status TEXT, FOREIGN KEY(org_id) REFERENCES organizations(id))`,
		`CREATE TABLE invoice_items (id INTEGER PRIMARY KEY, invoice_id INTEGER NOT NULL, description TEXT, amount INTEGER, FOREIGN KEY(invoice_id) REFERENCES invoices(id))`,
		// projects
		`CREATE TABLE projects (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT, created_by INTEGER, FOREIGN KEY(org_id) REFERENCES organizations(id), FOREIGN KEY(created_by) REFERENCES users(id))`,
		`CREATE TABLE project_members (id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, user_id INTEGER NOT NULL, FOREIGN KEY(project_id) REFERENCES projects(id), FOREIGN KEY(user_id) REFERENCES users(id))`,
		// content
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, author_id INTEGER, title TEXT, status TEXT, FOREIGN KEY(project_id) REFERENCES projects(id), FOREIGN KEY(author_id) REFERENCES users(id))`,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE post_tags (post_id INTEGER NOT NULL, tag_id INTEGER NOT NULL, PRIMARY KEY(post_id, tag_id), FOREIGN KEY(post_id) REFERENCES posts(id), FOREIGN KEY(tag_id) REFERENCES tags(id))`,
		`CREATE TABLE comments (id INTEGER PRIMARY KEY, post_id INTEGER NOT NULL, user_id INTEGER NOT NULL, body TEXT, FOREIGN KEY(post_id) REFERENCES posts(id), FOREIGN KEY(user_id) REFERENCES users(id))`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			panic(err)
		}
	}

	// --- 2. Il initialise votre bibliothèque ---
	inspector, err := schema.NewInspector(db)
	if err != nil {
		panic(err)
	}

	// --- 3. Il enregistre la route OÙ IL VEUT ---
	// Comme ça, AUCUN CONFLIT ! S'il utilise déjà "/", il peut mettre votre outil sur "/_debug/schema"
	http.HandleFunc("/_debug/schema", schema.HTTPHandler(inspector))

	// Ses propres routes métiers :
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Bienvenue sur l'application de l'utilisateur !"))
	})
	http.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name": "Alice"}]`))
	})

	fmt.Println("🚀 Serveur démarré sur http://localhost:8080")
	fmt.Println("👉 L'application de l'utilisateur est sur http://localhost:8080/")
	fmt.Println("👉 Votre inspecteur de schéma est sur http://localhost:8080/_debug/schema")
	
	err = http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}
