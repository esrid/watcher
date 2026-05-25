//go:build integration

package watcher

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestProbeDialect_IntegrationPostgres(t *testing.T) {
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

	// Test probeDialect directly using the unexported function
	insp, err := probeDialect(db, "custom_opaque_wrapper")
	require.NoError(t, err)
	require.NotNil(t, insp)

	// Verify it operates correctly on postgres
	tables, err := insp.Tables(ctx)
	require.NoError(t, err)
	assert.Empty(t, tables)
}

func TestProbeDialect_IntegrationMySQL(t *testing.T) {
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

	// Test probeDialect directly using the unexported function
	insp, err := probeDialect(db, "custom_opaque_wrapper")
	require.NoError(t, err)
	require.NotNil(t, insp)

	// Verify it operates correctly on mysql
	tables, err := insp.Tables(ctx)
	require.NoError(t, err)
	assert.Empty(t, tables)
}
