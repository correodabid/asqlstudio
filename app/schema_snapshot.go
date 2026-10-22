package studioapp

import (
	"strings"

	api "github.com/correodabid/asql/pkg/adminapi"
)

// snapshotPrimaryKeyColumns is the table's key in key order. It prefers the
// table-level list, the engine's source of truth, and falls back to the column
// flags, which is all an engine older than v0.5.0 reports.
func snapshotPrimaryKeyColumns(table api.SchemaSnapshotTable) []string {
	if len(table.PrimaryKey) > 0 {
		key := make([]string, 0, len(table.PrimaryKey))
		for _, column := range table.PrimaryKey {
			if trimmed := strings.TrimSpace(column); trimmed != "" {
				key = append(key, trimmed)
			}
		}
		if len(key) > 0 {
			return key
		}
	}
	var key []string
	for _, column := range table.Columns {
		if column.PrimaryKey {
			if trimmed := strings.TrimSpace(column.Name); trimmed != "" {
				key = append(key, trimmed)
			}
		}
	}
	return key
}

// internalColumnNames are columns the engine maintains on every table. They are
// visible — SELECT * returns them, which is why the snapshot lists them — but
// they are not declared, so a baseline that carried them would generate DDL
// that redeclares a column the engine adds by itself.
var internalColumnNames = map[string]struct{}{
	"_lsn": {},
}

func isInternalSchemaColumn(name string) bool {
	_, ok := internalColumnNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// isInternalSchemaIndex reports the indexes the engine creates for a primary
// key: "__auto_<table>_<column>" for a single-column key and "__pk_<table>" for
// a composite one. They follow the key, so a baseline must not carry them as
// index definitions of its own.
func isInternalSchemaIndex(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(lower, "__auto_") || strings.HasPrefix(lower, "__pk_")
}
