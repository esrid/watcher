package model

// Index describes one database index on a table.
type Index struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"` // key order
	Partial bool     `json:"partial"` // true when a WHERE predicate exists
}

// ColumnMeta extends raw column data with constraint information.
type ColumnMeta struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	PK      bool   `json:"pk"`
	FK      bool   `json:"fk"`     // populated by HTTPHandler from Relations(), not Inspector
	NotNull bool   `json:"notNull"`
	Default string `json:"default"` // empty string = no explicit default
	Unique  bool   `json:"unique"`  // true when column has a UNIQUE constraint/index
}
