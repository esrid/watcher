package watcher

import (
	"context"
	"sort"
	"sync"
	"time"
)

// SchemaSnapshot is a point-in-time capture of the full schema.
type SchemaSnapshot struct {
	At     time.Time
	Tables map[string]tableSnapshot // keyed by table name
}

type tableSnapshot struct {
	Columns []ColumnMeta
	Indexes []Index
}

// SchemaDiff describes what changed between two snapshots.
type SchemaDiff struct {
	Before        time.Time   `json:"before"`
	After         time.Time   `json:"after"`
	AddedTables   []string    `json:"addedTables"`
	DroppedTables []string    `json:"droppedTables"`
	Modified      []TableDiff `json:"modified"`
}

// TableDiff describes changes within a single table.
type TableDiff struct {
	Table          string       `json:"table"`
	AddedColumns   []string     `json:"addedColumns"`
	DroppedColumns []string     `json:"droppedColumns"`
	TypeChanges    []TypeChange `json:"typeChanges"`
	AddedIndexes   []string     `json:"addedIndexes"`
	DroppedIndexes []string     `json:"droppedIndexes"`
}

// TypeChange records a column whose type changed between snapshots.
type TypeChange struct {
	Column string `json:"column"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// Differ takes schema snapshots and computes diffs between consecutive ones.
type Differ struct {
	mu        sync.RWMutex
	inspector Inspector
	prev      *SchemaSnapshot // nil until first Snapshot call
	curr      *SchemaSnapshot // nil until first Snapshot call
}

// NewDiffer creates a new Differ.
func NewDiffer(inspector Inspector) *Differ {
	return &Differ{
		inspector: inspector,
	}
}

// Snapshot captures the current schema state. Thread-safe.
// The new snapshot becomes "curr"; the previous "curr" becomes "prev".
func (d *Differ) Snapshot(ctx context.Context) (SchemaSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	tables, err := d.inspector.Tables(ctx)
	if err != nil {
		return SchemaSnapshot{}, err
	}

	snapTables := make(map[string]tableSnapshot, len(tables))
	for _, t := range tables {
		cols, err := d.inspector.ColumnMeta(ctx, t)
		if err != nil {
			return SchemaSnapshot{}, err
		}

		idxs, err := d.inspector.Indexes(ctx, t)
		if err != nil {
			return SchemaSnapshot{}, err
		}

		snapTables[t] = tableSnapshot{
			Columns: cols,
			Indexes: idxs,
		}
	}

	snap := SchemaSnapshot{
		At:     time.Now(),
		Tables: snapTables,
	}

	d.prev = d.curr
	d.curr = &snap

	return snap, nil
}

// Diff returns the diff between the two most recent snapshots.
// Returns zero SchemaDiff and false when fewer than two snapshots exist.
func (d *Differ) Diff() (SchemaDiff, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.prev == nil || d.curr == nil {
		return SchemaDiff{}, false
	}

	return diff(*d.prev, *d.curr), true
}

// diff computes the schema diff between snapshot a (before) and b (after).
func diff(a, b SchemaSnapshot) SchemaDiff {
	var addedTables []string
	var droppedTables []string
	var modified []TableDiff

	// 1. Added/Dropped tables
	for t := range b.Tables {
		if _, ok := a.Tables[t]; !ok {
			addedTables = append(addedTables, t)
		}
	}
	for t := range a.Tables {
		if _, ok := b.Tables[t]; !ok {
			droppedTables = append(droppedTables, t)
		}
	}

	sort.Strings(addedTables)
	sort.Strings(droppedTables)

	// 2. Table modifications (columns and indexes)
	for t, bTab := range b.Tables {
		aTab, ok := a.Tables[t]
		if !ok {
			continue // covered by addedTables
		}

		var addedCols []string
		var droppedCols []string
		var typeChanges []TypeChange
		var addedIdxs []string
		var droppedIdxs []string

		// Column diffs
		bColsMap := make(map[string]ColumnMeta, len(bTab.Columns))
		for _, col := range bTab.Columns {
			bColsMap[col.Name] = col
		}
		aColsMap := make(map[string]ColumnMeta, len(aTab.Columns))
		for _, col := range aTab.Columns {
			aColsMap[col.Name] = col
		}

		for _, col := range bTab.Columns {
			aCol, ok := aColsMap[col.Name]
			if !ok {
				addedCols = append(addedCols, col.Name)
			} else if aCol.Type != col.Type {
				typeChanges = append(typeChanges, TypeChange{
					Column: col.Name,
					Before: aCol.Type,
					After:  col.Type,
				})
			}
		}

		for _, col := range aTab.Columns {
			if _, ok := bColsMap[col.Name]; !ok {
				droppedCols = append(droppedCols, col.Name)
			}
		}

		// Index diffs
		bIdxMap := make(map[string]Index, len(bTab.Indexes))
		for _, idx := range bTab.Indexes {
			bIdxMap[idx.Name] = idx
		}
		aIdxMap := make(map[string]Index, len(aTab.Indexes))
		for _, idx := range aTab.Indexes {
			aIdxMap[idx.Name] = idx
		}

		for _, idx := range bTab.Indexes {
			if _, ok := aIdxMap[idx.Name]; !ok {
				addedIdxs = append(addedIdxs, idx.Name)
			}
		}

		for _, idx := range aTab.Indexes {
			if _, ok := bIdxMap[idx.Name]; !ok {
				droppedIdxs = append(droppedIdxs, idx.Name)
			}
		}

		if len(addedCols) > 0 || len(droppedCols) > 0 || len(typeChanges) > 0 || len(addedIdxs) > 0 || len(droppedIdxs) > 0 {
			sort.Strings(addedCols)
			sort.Strings(droppedCols)
			sort.Slice(typeChanges, func(i, j int) bool {
				return typeChanges[i].Column < typeChanges[j].Column
			})
			sort.Strings(addedIdxs)
			sort.Strings(droppedIdxs)

			modified = append(modified, TableDiff{
				Table:          t,
				AddedColumns:   addedCols,
				DroppedColumns: droppedCols,
				TypeChanges:    typeChanges,
				AddedIndexes:   addedIdxs,
				DroppedIndexes: droppedIdxs,
			})
		}
	}

	sort.Slice(modified, func(i, j int) bool {
		return modified[i].Table < modified[j].Table
	})

	return SchemaDiff{
		Before:        a.At,
		After:         b.At,
		AddedTables:   addedTables,
		DroppedTables: droppedTables,
		Modified:      modified,
	}
}
