//go:build integration

package schema_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/esrid/watcher/pkg/schema"
)

// ── column parsing ────────────────────────────────────────────────────────────

// col mirrors the "name|type|pk" wire format produced by every inspector.
type col struct {
	name string
	typ  string
	pk   bool
}

func parseCol(s string) (col, bool) {
	parts := strings.Split(s, "|")
	if len(parts) != 3 {
		return col{}, false
	}
	return col{name: parts[0], typ: parts[1], pk: parts[2] == "1"}, true
}

func colNames(cols []col) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.name
	}
	return names
}

func pkNames(cols []col) []string {
	var names []string
	for _, c := range cols {
		if c.pk {
			names = append(names, c.name)
		}
	}
	return names
}

// ── Postgres suite ────────────────────────────────────────────────────────────

type PostgresSuite struct {
	suite.Suite
	db   *sql.DB
	insp schema.Inspector
}

// SetupSuite starts the container once for the whole suite.
func (s *PostgresSuite) SetupSuite() {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("tester"),
		tcpostgres.WithPassword("secret"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2),
		),
	)
	s.Require().NoError(err, "start postgres container")
	s.T().Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	s.Require().NoError(err, "postgres DSN")

	s.db, err = sql.Open("postgres", dsn)
	s.Require().NoError(err, "open postgres db")
	s.T().Cleanup(func() { s.db.Close() })
	s.Require().NoError(s.db.PingContext(ctx), "ping postgres")

	insp, err := schema.NewInspector(s.db)
	s.Require().NoError(err, "NewInspector")
	s.insp = insp
}

// SetupTest creates the schema before each test; TearDownTest drops it after —
// each test runs against a fresh, empty public schema.
func (s *PostgresSuite) SetupTest() {
	_, err := s.db.ExecContext(context.Background(), `
		DROP SCHEMA public CASCADE;
		CREATE SCHEMA public;
	`)
	s.Require().NoError(err, "reset schema")
}

func (s *PostgresSuite) exec(stmts ...string) {
	s.T().Helper()
	for _, q := range stmts {
		_, err := s.db.ExecContext(context.Background(), q)
		s.Require().NoError(err, "exec: %s", q)
	}
}

func (s *PostgresSuite) seed() {
	s.exec(
		`CREATE TABLE users (
			id         SERIAL PRIMARY KEY,
			username   VARCHAR(100) NOT NULL,
			email      VARCHAR(200) NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE TABLE orders (
			id        SERIAL PRIMARY KEY,
			user_id   INTEGER NOT NULL REFERENCES users(id),
			total     NUMERIC(10,2),
			placed_at TIMESTAMP DEFAULT NOW()
		)`,
	)
}

// --- Tables ---

func (s *PostgresSuite) TestTables_EmptySchema() {
	tables, err := s.insp.Tables(context.Background())
	s.Require().NoError(err)
	s.Empty(tables, "fresh schema must have no tables")
}

func (s *PostgresSuite) TestTables_ReturnsExactSet() {
	s.seed()

	tables, err := s.insp.Tables(context.Background())
	s.Require().NoError(err)
	s.ElementsMatch([]string{"users", "orders"}, tables)
}

// --- Columns ---

func (s *PostgresSuite) TestColumns_Users() {
	s.seed()

	raw, err := s.insp.Columns(context.Background(), "users")
	s.Require().NoError(err)
	s.Require().NotEmpty(raw, "users must have columns")

	cols := s.parseCols(raw)
	s.ElementsMatch([]string{"id", "username", "email", "created_at"}, colNames(cols))
	s.ElementsMatch([]string{"id"}, pkNames(cols))
	s.assertTypesNotEmpty(cols)
}

func (s *PostgresSuite) TestColumns_Orders() {
	s.seed()

	raw, err := s.insp.Columns(context.Background(), "orders")
	s.Require().NoError(err)
	s.Require().NotEmpty(raw, "orders must have columns")

	cols := s.parseCols(raw)
	s.ElementsMatch([]string{"id", "user_id", "total", "placed_at"}, colNames(cols))
	s.ElementsMatch([]string{"id"}, pkNames(cols))
	s.assertTypesNotEmpty(cols)
}

// --- Relations ---

func (s *PostgresSuite) TestRelations_UsersHasNoFK() {
	s.seed()

	rels, err := s.insp.Relations(context.Background(), "users")
	s.Require().NoError(err)
	s.Empty(rels, "users has no foreign keys")
}

func (s *PostgresSuite) TestRelations_OrdersHasExactlyOneFK() {
	s.seed()

	rels, err := s.insp.Relations(context.Background(), "orders")
	s.Require().NoError(err)
	s.ElementsMatch([]string{"user_id -> users.id"}, rels)
}

func (s *PostgresSuite) TestRelations_MultipleFKs() {
	s.exec(
		`CREATE TABLE customers (id SERIAL PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE products  (id SERIAL PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE line_items (
			id          SERIAL PRIMARY KEY,
			customer_id INTEGER NOT NULL REFERENCES customers(id),
			product_id  INTEGER NOT NULL REFERENCES products(id),
			qty         INTEGER NOT NULL
		)`,
	)

	rels, err := s.insp.Relations(context.Background(), "line_items")
	s.Require().NoError(err)
	s.ElementsMatch([]string{
		"customer_id -> customers.id",
		"product_id -> products.id",
	}, rels)
}

// --- helpers ---

func (s *PostgresSuite) parseCols(raw []string) []col {
	s.T().Helper()
	cols := make([]col, 0, len(raw))
	for _, r := range raw {
		c, ok := parseCol(r)
		s.Require().Truef(ok, "column descriptor %q is not in name|type|pk format", r)
		cols = append(cols, c)
	}
	return cols
}

func (s *PostgresSuite) assertTypesNotEmpty(cols []col) {
	s.T().Helper()
	for _, c := range cols {
		s.NotEmptyf(strings.TrimSpace(c.typ), "column %q has an empty type", c.name)
	}
}

// Suite launcher — required by testify/suite.
func TestPostgresSuite(t *testing.T) {
	suite.Run(t, new(PostgresSuite))
}

// ── MySQL suite ───────────────────────────────────────────────────────────────

type MySQLSuite struct {
	suite.Suite
	db   *sql.DB
	insp schema.Inspector
}

func (s *MySQLSuite) SetupSuite() {
	ctx := context.Background()

	ctr, err := tcmysql.Run(ctx,
		"mysql:8.4",
		tcmysql.WithDatabase("testdb"),
		tcmysql.WithUsername("tester"),
		tcmysql.WithPassword("secret"),
	)
	s.Require().NoError(err, "start mysql container")
	s.T().Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx)
	s.Require().NoError(err, "mysql DSN")

	s.db, err = sql.Open("mysql", dsn)
	s.Require().NoError(err, "open mysql db")
	s.T().Cleanup(func() { s.db.Close() })
	s.Require().NoError(s.db.PingContext(ctx), "ping mysql")

	insp, err := schema.NewInspector(s.db)
	s.Require().NoError(err, "NewInspector")
	s.insp = insp
}

// SetupTest drops all user tables so each test starts from a clean database.
// MySQL does not support DROP SCHEMA / CREATE SCHEMA like Postgres, so we
// drop tables individually (FK-safe order via foreign_key_checks=0).
func (s *MySQLSuite) SetupTest() {
	ctx := context.Background()
	_, err := s.db.ExecContext(ctx, "SET foreign_key_checks = 0")
	s.Require().NoError(err)

	rows, err := s.db.QueryContext(ctx, `
		SELECT TABLE_NAME
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'
	`)
	s.Require().NoError(err)
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		s.Require().NoError(rows.Scan(&name))
		tables = append(tables, name)
	}
	s.Require().NoError(rows.Err())

	for _, t := range tables {
		_, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS `"+t+"`")
		s.Require().NoError(err, "drop table %s", t)
	}

	_, err = s.db.ExecContext(ctx, "SET foreign_key_checks = 1")
	s.Require().NoError(err)
}

func (s *MySQLSuite) exec(stmts ...string) {
	s.T().Helper()
	for _, q := range stmts {
		_, err := s.db.ExecContext(context.Background(), q)
		s.Require().NoError(err, "exec: %s", q)
	}
}

func (s *MySQLSuite) seed() {
	s.exec(
		`CREATE TABLE users (
			id         INT AUTO_INCREMENT PRIMARY KEY,
			username   VARCHAR(100) NOT NULL,
			email      VARCHAR(200) NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB`,
		`CREATE TABLE orders (
			id        INT AUTO_INCREMENT PRIMARY KEY,
			user_id   INT NOT NULL,
			total     DECIMAL(10,2),
			placed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT fk_orders_user FOREIGN KEY (user_id) REFERENCES users(id)
		) ENGINE=InnoDB`,
	)
}

// --- Tables ---

func (s *MySQLSuite) TestTables_EmptySchema() {
	tables, err := s.insp.Tables(context.Background())
	s.Require().NoError(err)
	s.Empty(tables, "fresh schema must have no tables")
}

func (s *MySQLSuite) TestTables_ReturnsExactSet() {
	s.seed()

	tables, err := s.insp.Tables(context.Background())
	s.Require().NoError(err)
	s.ElementsMatch([]string{"users", "orders"}, tables)
}

// --- Columns ---

func (s *MySQLSuite) TestColumns_Users() {
	s.seed()

	raw, err := s.insp.Columns(context.Background(), "users")
	s.Require().NoError(err)
	s.Require().NotEmpty(raw, "users must have columns")

	cols := s.parseCols(raw)
	s.ElementsMatch([]string{"id", "username", "email", "created_at"}, colNames(cols))
	s.ElementsMatch([]string{"id"}, pkNames(cols))
	s.assertTypesNotEmpty(cols)
}

func (s *MySQLSuite) TestColumns_Orders() {
	s.seed()

	raw, err := s.insp.Columns(context.Background(), "orders")
	s.Require().NoError(err)
	s.Require().NotEmpty(raw, "orders must have columns")

	cols := s.parseCols(raw)
	s.ElementsMatch([]string{"id", "user_id", "total", "placed_at"}, colNames(cols))
	s.ElementsMatch([]string{"id"}, pkNames(cols))
	s.assertTypesNotEmpty(cols)
}

// --- Relations ---

func (s *MySQLSuite) TestRelations_UsersHasNoFK() {
	s.seed()

	rels, err := s.insp.Relations(context.Background(), "users")
	s.Require().NoError(err)
	s.Empty(rels, "users has no foreign keys")
}

func (s *MySQLSuite) TestRelations_OrdersHasExactlyOneFK() {
	s.seed()

	rels, err := s.insp.Relations(context.Background(), "orders")
	s.Require().NoError(err)
	s.ElementsMatch([]string{"user_id -> users.id"}, rels)
}

func (s *MySQLSuite) TestRelations_MultipleFKs() {
	s.exec(
		`CREATE TABLE customers (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100) NOT NULL) ENGINE=InnoDB`,
		`CREATE TABLE products  (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100) NOT NULL) ENGINE=InnoDB`,
		`CREATE TABLE line_items (
			id          INT AUTO_INCREMENT PRIMARY KEY,
			customer_id INT NOT NULL,
			product_id  INT NOT NULL,
			qty         INT NOT NULL,
			CONSTRAINT fk_li_customer FOREIGN KEY (customer_id) REFERENCES customers(id),
			CONSTRAINT fk_li_product  FOREIGN KEY (product_id)  REFERENCES products(id)
		) ENGINE=InnoDB`,
	)

	rels, err := s.insp.Relations(context.Background(), "line_items")
	s.Require().NoError(err)
	s.ElementsMatch([]string{
		"customer_id -> customers.id",
		"product_id -> products.id",
	}, rels)
}

// --- helpers ---

func (s *MySQLSuite) parseCols(raw []string) []col {
	s.T().Helper()
	cols := make([]col, 0, len(raw))
	for _, r := range raw {
		c, ok := parseCol(r)
		s.Require().Truef(ok, "column descriptor %q is not in name|type|pk format", r)
		cols = append(cols, c)
	}
	return cols
}

func (s *MySQLSuite) assertTypesNotEmpty(cols []col) {
	s.T().Helper()
	for _, c := range cols {
		s.NotEmptyf(strings.TrimSpace(c.typ), "column %q has an empty type", c.name)
	}
}

func TestMySQLSuite(t *testing.T) {
	suite.Run(t, new(MySQLSuite))
}

// ── NewInspector dispatch ─────────────────────────────────────────────────────
// These are the only two tests that need their own container and cannot share
// the suite's — they just verify the driver detection wiring, not the queries.

func TestNewInspector_DispatchPostgres(t *testing.T) {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("tester"),
		tcpostgres.WithPassword("secret"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	insp, err := schema.NewInspector(db)
	require.NoError(t, err)
	require.NotNil(t, insp)
}

func TestNewInspector_DispatchMySQL(t *testing.T) {
	ctx := context.Background()
	ctr, err := tcmysql.Run(ctx, "mysql:8.4",
		tcmysql.WithDatabase("testdb"),
		tcmysql.WithUsername("tester"),
		tcmysql.WithPassword("secret"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	insp, err := schema.NewInspector(db)
	require.NoError(t, err)
	require.NotNil(t, insp)
}
