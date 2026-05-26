package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDiff_NoChange(t *testing.T) {
	cols := []ColumnMeta{
		{Name: "id", Type: "int", PK: true},
		{Name: "name", Type: "text"},
	}
	idxs := []Index{
		{Name: "idx_name", Unique: true, Columns: []string{"name"}},
	}
	a := SchemaSnapshot{
		At: time.Now().Add(-5 * time.Second),
		Tables: map[string]tableSnapshot{
			"users": {Columns: cols, Indexes: idxs},
		},
	}
	b := SchemaSnapshot{
		At: time.Now(),
		Tables: map[string]tableSnapshot{
			"users": {Columns: cols, Indexes: idxs},
		},
	}

	res := diff(a, b)
	assert.Empty(t, res.AddedTables)
	assert.Empty(t, res.DroppedTables)
	assert.Empty(t, res.Modified)
}

func TestDiff_AddedTable(t *testing.T) {
	a := SchemaSnapshot{At: time.Now().Add(-5 * time.Second), Tables: map[string]tableSnapshot{}}
	b := SchemaSnapshot{
		At: time.Now(),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "id", Type: "int", PK: true}},
			},
		},
	}

	res := diff(a, b)
	assert.Equal(t, []string{"users"}, res.AddedTables)
	assert.Empty(t, res.DroppedTables)
	assert.Empty(t, res.Modified)
}

func TestDiff_DroppedTable(t *testing.T) {
	a := SchemaSnapshot{
		At: time.Now().Add(-5 * time.Second),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "id", Type: "int", PK: true}},
			},
		},
	}
	b := SchemaSnapshot{At: time.Now(), Tables: map[string]tableSnapshot{}}

	res := diff(a, b)
	assert.Empty(t, res.AddedTables)
	assert.Equal(t, []string{"users"}, res.DroppedTables)
	assert.Empty(t, res.Modified)
}

func TestDiff_AddedColumn(t *testing.T) {
	a := SchemaSnapshot{
		At: time.Now().Add(-5 * time.Second),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "id", Type: "int", PK: true}},
			},
		},
	}
	b := SchemaSnapshot{
		At: time.Now(),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{
					{Name: "id", Type: "int", PK: true},
					{Name: "email", Type: "text"},
				},
			},
		},
	}

	res := diff(a, b)
	assert.Len(t, res.Modified, 1)
	assert.Equal(t, "users", res.Modified[0].Table)
	assert.Equal(t, []string{"email"}, res.Modified[0].AddedColumns)
	assert.Empty(t, res.Modified[0].DroppedColumns)
	assert.Empty(t, res.Modified[0].TypeChanges)
}

func TestDiff_DroppedColumn(t *testing.T) {
	a := SchemaSnapshot{
		At: time.Now().Add(-5 * time.Second),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{
					{Name: "id", Type: "int", PK: true},
					{Name: "email", Type: "text"},
				},
			},
		},
	}
	b := SchemaSnapshot{
		At: time.Now(),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "id", Type: "int", PK: true}},
			},
		},
	}

	res := diff(a, b)
	assert.Len(t, res.Modified, 1)
	assert.Equal(t, "users", res.Modified[0].Table)
	assert.Empty(t, res.Modified[0].AddedColumns)
	assert.Equal(t, []string{"email"}, res.Modified[0].DroppedColumns)
}

func TestDiff_TypeChange(t *testing.T) {
	a := SchemaSnapshot{
		At: time.Now().Add(-5 * time.Second),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "age", Type: "text"}},
			},
		},
	}
	b := SchemaSnapshot{
		At: time.Now(),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "age", Type: "int"}},
			},
		},
	}

	res := diff(a, b)
	assert.Len(t, res.Modified, 1)
	assert.Equal(t, []TypeChange{{Column: "age", Before: "text", After: "int"}}, res.Modified[0].TypeChanges)
}

func TestDiff_AddedIndex(t *testing.T) {
	a := SchemaSnapshot{
		At: time.Now().Add(-5 * time.Second),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "id", Type: "int", PK: true}},
			},
		},
	}
	b := SchemaSnapshot{
		At: time.Now(),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "id", Type: "int", PK: true}},
				Indexes: []Index{{Name: "idx_id", Unique: true, Columns: []string{"id"}}},
			},
		},
	}

	res := diff(a, b)
	assert.Len(t, res.Modified, 1)
	assert.Equal(t, []string{"idx_id"}, res.Modified[0].AddedIndexes)
	assert.Empty(t, res.Modified[0].DroppedIndexes)
}

func TestDiff_DroppedIndex(t *testing.T) {
	a := SchemaSnapshot{
		At: time.Now().Add(-5 * time.Second),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "id", Type: "int", PK: true}},
				Indexes: []Index{{Name: "idx_id", Unique: true, Columns: []string{"id"}}},
			},
		},
	}
	b := SchemaSnapshot{
		At: time.Now(),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{{Name: "id", Type: "int", PK: true}},
			},
		},
	}

	res := diff(a, b)
	assert.Len(t, res.Modified, 1)
	assert.Empty(t, res.Modified[0].AddedIndexes)
	assert.Equal(t, []string{"idx_id"}, res.Modified[0].DroppedIndexes)
}

func TestDiff_DeterministicSorting(t *testing.T) {
	// Diffs with mixed names should be sorted alphabetically in output
	a := SchemaSnapshot{
		At: time.Now().Add(-5 * time.Second),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{
					{Name: "z_col", Type: "text"},
					{Name: "a_col", Type: "text"},
				},
				Indexes: []Index{
					{Name: "idx_z", Unique: true},
					{Name: "idx_a", Unique: true},
				},
			},
			"accounts": {
				Columns: []ColumnMeta{{Name: "id", Type: "int"}},
			},
		},
	}
	b := SchemaSnapshot{
		At: time.Now(),
		Tables: map[string]tableSnapshot{
			"users": {
				Columns: []ColumnMeta{}, // dropped z_col, a_col
			},
			"accounts": {
				Columns: []ColumnMeta{}, // dropped id
			},
		},
	}

	res := diff(a, b)
	assert.Len(t, res.Modified, 2)
	assert.Equal(t, "accounts", res.Modified[0].Table) // accounts before users
	assert.Equal(t, "users", res.Modified[0+1].Table)
	assert.Equal(t, []string{"a_col", "z_col"}, res.Modified[1].DroppedColumns) // a_col before z_col
	assert.Equal(t, []string{"idx_a", "idx_z"}, res.Modified[1].DroppedIndexes) // idx_a before idx_z
}

type fakeInspector struct {
	tables     []string
	columns    map[string][]ColumnMeta
	indexes    map[string][]Index
	tablesErr  error
	columnsErr error
}

func (f *fakeInspector) Tables(ctx context.Context) ([]string, error) {
	return f.tables, f.tablesErr
}

func (f *fakeInspector) Columns(ctx context.Context, tableName string) ([]string, error) {
	return nil, nil
}

func (f *fakeInspector) Relations(ctx context.Context, tableName string) ([]string, error) {
	return nil, nil
}

func (f *fakeInspector) Indexes(ctx context.Context, tableName string) ([]Index, error) {
	return f.indexes[tableName], nil
}

func (f *fakeInspector) ColumnMeta(ctx context.Context, tableName string) ([]ColumnMeta, error) {
	return f.columns[tableName], f.columnsErr
}

func TestDiffer_Operations(t *testing.T) {
	fi := &fakeInspector{
		tables: []string{"users"},
		columns: map[string][]ColumnMeta{
			"users": {{Name: "id", Type: "int", PK: true}},
		},
		indexes: map[string][]Index{
			"users": {},
		},
	}

	d := NewDiffer(fi)

	// Before two snapshots, Diff() returns false
	_, ok := d.Diff()
	assert.False(t, ok)

	snap1, err := d.Snapshot(context.Background())
	assert.NoError(t, err)
	assert.NotZero(t, snap1.At)

	_, ok = d.Diff()
	assert.False(t, ok)

	// Modify target schema on fake inspector
	fi.tables = append(fi.tables, "orders")
	fi.columns["orders"] = []ColumnMeta{{Name: "id", Type: "int", PK: true}}

	snap2, err := d.Snapshot(context.Background())
	assert.NoError(t, err)

	res, ok := d.Diff()
	assert.True(t, ok)
	assert.Equal(t, []string{"orders"}, res.AddedTables)
	assert.Equal(t, snap1.At, res.Before)
	assert.Equal(t, snap2.At, res.After)
}
