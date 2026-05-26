package watcher

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// SchemaSnapshot is a point-in-time capture of the full schema.
type SchemaSnapshot struct {
	At     time.Time
	Tables map[string]TableSnapshot // keyed by table name
}

// TableSnapshot describes a single table structure in a snapshot.
type TableSnapshot struct {
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

// Empty returns true if there are no changes in the diff.
func (sd SchemaDiff) Empty() bool {
	return len(sd.AddedTables) == 0 && len(sd.DroppedTables) == 0 && len(sd.Modified) == 0
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
	lastDiff  SchemaDiff      // last non-empty diff
	hasDiff   bool            // true once we have at least 2 snapshots
}

// NewDiffer creates a new Differ.
func NewDiffer(inspector Inspector) *Differ {
	return &Differ{inspector: inspector}
}

// Watch takes a snapshot on every tick of interval until ctx is cancelled.
// It calls Snapshot immediately on the first tick, so the first diff is
// available after two intervals. Snapshot errors are logged and do not stop
// the loop.
func (d *Differ) Watch(ctx context.Context, interval time.Duration, onErr func(error)) {
	go func() {
		defer func() {
			if r := recover(); r != nil && onErr != nil {
				onErr(fmt.Errorf("schema watcher panicked: %v", r))
			}
		}()

		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := d.Snapshot(ctx); err != nil && onErr != nil {
					// Avoid reporting error if the context has been cancelled
					if ctx.Err() == nil {
						onErr(err)
					}
				}
			}
		}
	}()
}

// Snapshot captures the current schema state. Thread-safe.
// The new snapshot becomes "curr"; the previous "curr" becomes "prev".
func (d *Differ) Snapshot(ctx context.Context) (SchemaSnapshot, error) {
	// 1. Gather all data unlocked to avoid lock contention
	tables, err := d.inspector.Tables(ctx)
	if err != nil {
		return SchemaSnapshot{}, fmt.Errorf("failed to fetch tables: %w", err)
	}

	snapTables := make(map[string]TableSnapshot, len(tables))
	for _, t := range tables {
		cols, err := d.inspector.ColumnMeta(ctx, t)
		if err != nil {
			return SchemaSnapshot{}, fmt.Errorf("failed to fetch columns for table %s: %w", t, err)
		}

		idxs, err := d.inspector.Indexes(ctx, t)
		if err != nil {
			return SchemaSnapshot{}, fmt.Errorf("failed to fetch indexes for table %s: %w", t, err)
		}

		snapTables[t] = TableSnapshot{
			Columns: cols,
			Indexes: idxs,
		}
	}

	snap := SchemaSnapshot{
		At:     time.Now(),
		Tables: snapTables,
	}

	// Read the current snapshot pointer under a brief read-lock to compute diff unlocked
	d.mu.RLock()
	currentSnap := d.curr
	d.mu.RUnlock()

	var newDiff SchemaDiff
	var hasNewDiff bool
	if currentSnap != nil {
		newDiff = diff(*currentSnap, snap)
		hasNewDiff = true
	}

	// 2. Perform quick updates under write lock
	d.mu.Lock()
	d.prev = d.curr
	d.curr = &snap

	if hasNewDiff {
		d.hasDiff = true
		if !newDiff.Empty() {
			d.lastDiff = newDiff
		}
	}
	d.mu.Unlock()

	return snap, nil
}

// Diff returns the last non-empty diff, or the latest empty diff if no changes have ever occurred.
// Returns zero SchemaDiff and false when fewer than two snapshots exist.
func (d *Differ) Diff() (SchemaDiff, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.hasDiff {
		return SchemaDiff{}, false
	}

	return d.lastDiff, true
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

	slices.Sort(addedTables)
	slices.Sort(droppedTables)

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
			slices.Sort(addedCols)
			slices.Sort(droppedCols)
			slices.SortFunc(typeChanges, func(a, b TypeChange) int {
				return cmp.Compare(a.Column, b.Column)
			})
			slices.Sort(addedIdxs)
			slices.Sort(droppedIdxs)

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

	slices.SortFunc(modified, func(a, b TableDiff) int {
		return cmp.Compare(a.Table, b.Table)
	})

	return SchemaDiff{
		Before:        a.At,
		After:         b.At,
		AddedTables:   addedTables,
		DroppedTables: droppedTables,
		Modified:      modified,
	}
}
