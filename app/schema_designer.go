package studioapp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var typePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_(), ]*$`)

type schemaDDLEntity struct {
	Name      string   `json:"name"`
	RootTable string   `json:"root_table"`
	Tables    []string `json:"tables"`
}

type schemaDDLRequest struct {
	Domain   string            `json:"domain"`
	Tables   []schemaDDLTable  `json:"tables"`
	Entities []schemaDDLEntity `json:"entities,omitempty"`
}

type schemaDDLTable struct {
	Name                 string                 `json:"name"`
	Columns              []schemaDDLColumn      `json:"columns"`
	Indexes              []schemaDDLIndex       `json:"indexes,omitempty"`
	VersionedForeignKeys []schemaDDLVersionedFK `json:"versioned_foreign_keys,omitempty"`
	// PrimaryKey lists the key columns in key order. A single-column key may
	// instead be expressed by schemaDDLColumn.PrimaryKey; both forms render the
	// same DDL. A composite key can only be expressed here.
	PrimaryKey []string `json:"primary_key,omitempty"`
	// AppendOnly and Verifiable are table attributes that cannot be undone once
	// applied: rows of an APPEND ONLY table cannot be updated or deleted, and a
	// VERIFIABLE table's live rows are covered by the state tree, so their
	// membership can be proven against a signed ledger digest.
	AppendOnly bool `json:"append_only,omitempty"`
	Verifiable bool `json:"verifiable,omitempty"`
	// VerifiableGroups make the rows that share a key value provable as a
	// complete set. There is no CREATE TABLE form, so they are always applied
	// as a separate ALTER TABLE, and they cannot be undone.
	VerifiableGroups []schemaDDLVerifiableGroup `json:"verifiable_groups,omitempty"`
}

type schemaDDLVerifiableGroup struct {
	Columns           []string `json:"columns"`
	References        string   `json:"references"`
	ReferencedColumns []string `json:"referenced_columns"`
}

type schemaDDLIndex struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Method  string   `json:"method"` // "hash" or "btree"
}

type schemaDDLColumn struct {
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Nullable     bool                `json:"nullable"`
	PrimaryKey   bool                `json:"primary_key"`
	Unique       bool                `json:"unique"`
	DefaultValue string              `json:"default_value,omitempty"`
	References   *schemaDDLReference `json:"references,omitempty"`
}

type schemaDDLReference struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

type schemaDDLVersionedFK struct {
	Column           string `json:"column"`
	LSNColumn        string `json:"lsn_column"`
	ReferencesDomain string `json:"references_domain"`
	ReferencesTable  string `json:"references_table"`
	ReferencesColumn string `json:"references_column"`
}

type schemaDDLResponse struct {
	DDL        string   `json:"ddl"`
	Statements []string `json:"statements"`
}

// tablePrimaryKeyColumns is the table's key in key order. It prefers the
// table-level list and falls back to the column flags, so a table whose key was
// expressed either way reports the same key.
func tablePrimaryKeyColumns(table schemaDDLTable) []string {
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

// columnDefOptions controls how one column renders inside a CREATE TABLE body.
type columnDefOptions struct {
	// inlineReferences folds a foreign key into the column definition. The
	// designer emits foreign keys as separate ALTER TABLE statements, so it
	// leaves this off; a single ADD COLUMN has to carry it inline.
	inlineReferences bool
	// inlinePrimaryKey allows PRIMARY KEY on the column itself. A composite key
	// renders as a table-level constraint, so its columns must not claim it.
	inlinePrimaryKey bool
	// notNull forces NOT NULL. Every column of a primary key needs it, whether
	// or not the column was marked nullable.
	notNull bool
}

func renderColumnDefinition(column schemaDDLColumn, opts columnDefOptions) (string, error) {
	name := strings.TrimSpace(column.Name)
	if !isValidIdentifier(name) {
		return "", fmt.Errorf("invalid column name %q", name)
	}
	// The engine maintains _lsn itself and rejects declaring it (42701), so
	// catching it here says why instead of letting the statement fail.
	if isInternalSchemaColumn(name) {
		return "", fmt.Errorf("column %q is maintained by the engine and cannot be declared", name)
	}
	columnType := strings.ToUpper(strings.TrimSpace(column.Type))
	if columnType == "" || !typePattern.MatchString(columnType) {
		return "", fmt.Errorf("invalid type %q for column %s", column.Type, name)
	}

	definition := fmt.Sprintf("%s %s", name, columnType)
	if opts.notNull || column.PrimaryKey || !column.Nullable {
		definition += " NOT NULL"
	}
	if opts.inlinePrimaryKey && column.PrimaryKey {
		definition += " PRIMARY KEY"
	}
	if column.Unique && !column.PrimaryKey {
		definition += " UNIQUE"
	}
	if value := strings.TrimSpace(column.DefaultValue); value != "" {
		definition += " DEFAULT " + value
	}
	if opts.inlineReferences && column.References != nil {
		refTable := strings.TrimSpace(column.References.Table)
		refColumn := strings.TrimSpace(column.References.Column)
		if refTable != "" && refColumn != "" {
			definition += fmt.Sprintf(" REFERENCES %s(%s)", refTable, refColumn)
		}
	}
	return definition, nil
}

// renderVersionedFKDefinitions renders the table-level VERSIONED FOREIGN KEY
// constraints.
func renderVersionedFKDefinitions(table schemaDDLTable) ([]string, error) {
	tableName := strings.TrimSpace(table.Name)
	defs := make([]string, 0, len(table.VersionedForeignKeys))
	for _, vfk := range table.VersionedForeignKeys {
		column := strings.TrimSpace(vfk.Column)
		lsnColumn := strings.TrimSpace(vfk.LSNColumn)
		refDomain := strings.TrimSpace(vfk.ReferencesDomain)
		refTable := strings.TrimSpace(vfk.ReferencesTable)
		refColumn := strings.TrimSpace(vfk.ReferencesColumn)
		if column == "" || lsnColumn == "" || refTable == "" || refColumn == "" {
			return nil, fmt.Errorf("versioned foreign key in table %q has missing fields", tableName)
		}
		refTarget := fmt.Sprintf("%s(%s)", refTable, refColumn)
		if refDomain != "" {
			refTarget = fmt.Sprintf("%s.%s(%s)", refDomain, refTable, refColumn)
		}
		defs = append(defs, fmt.Sprintf("VERSIONED FOREIGN KEY (%s) REFERENCES %s AS OF %s", column, refTarget, lsnColumn))
	}
	return defs, nil
}

// renderVerifiableGroupStatement renders the ALTER TABLE that declares one
// group. Both tables must already be VERIFIABLE and the referenced columns must
// be the referenced table's primary key.
func renderVerifiableGroupStatement(tableName string, group schemaDDLVerifiableGroup) (string, error) {
	columns, err := trimmedIdentifierList(group.Columns)
	if err != nil {
		return "", fmt.Errorf("verifiable group on table %q: %w", tableName, err)
	}
	referencedColumns, err := trimmedIdentifierList(group.ReferencedColumns)
	if err != nil {
		return "", fmt.Errorf("verifiable group on table %q: %w", tableName, err)
	}
	references := strings.TrimSpace(group.References)
	if !isValidIdentifier(references) {
		return "", fmt.Errorf("verifiable group on table %q references invalid table %q", tableName, group.References)
	}
	return fmt.Sprintf(
		"ALTER TABLE %s ADD VERIFIABLE GROUP (%s) REFERENCES %s (%s);",
		tableName, strings.Join(columns, ", "), references, strings.Join(referencedColumns, ", "),
	), nil
}

func trimmedIdentifierList(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one column is required")
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if !isValidIdentifier(trimmed) {
			return nil, fmt.Errorf("invalid column name %q", value)
		}
		out = append(out, trimmed)
	}
	return out, nil
}

// verifiableGroupKey identifies a group within its table, so two versions of a
// schema can be compared group by group.
func verifiableGroupKey(group schemaDDLVerifiableGroup) string {
	columns, _ := trimmedIdentifierList(group.Columns)
	return strings.ToLower(strings.Join(columns, ","))
}

func verifiableGroupsEquivalent(left, right schemaDDLVerifiableGroup) bool {
	return sameColumnList(left.Columns, right.Columns) &&
		strings.EqualFold(strings.TrimSpace(left.References), strings.TrimSpace(right.References)) &&
		sameColumnList(left.ReferencedColumns, right.ReferencedColumns)
}

// tableAttributeSuffix renders the attributes that follow the column list.
// The parser accepts APPEND ONLY and VERIFIABLE in either order, each once.
func tableAttributeSuffix(table schemaDDLTable) string {
	suffix := ""
	if table.AppendOnly {
		suffix += " APPEND ONLY"
	}
	if table.Verifiable {
		suffix += " VERIFIABLE"
	}
	return suffix
}

// renderCreateTableStatement renders one CREATE TABLE: its columns, the
// table-level PRIMARY KEY of a composite key, its versioned foreign keys and
// its attributes. It is the single source of CREATE TABLE DDL — the designer
// and the diff both go through it so their output cannot drift apart.
func renderCreateTableStatement(table schemaDDLTable, inlineReferences bool) (string, error) {
	tableName := strings.TrimSpace(table.Name)
	if !isValidIdentifier(tableName) {
		return "", fmt.Errorf("invalid table name %q", tableName)
	}
	if len(table.Columns) == 0 {
		return "", fmt.Errorf("table %q must contain at least one column", tableName)
	}

	primaryKey := tablePrimaryKeyColumns(table)
	keyColumns := make(map[string]struct{}, len(primaryKey))
	for _, column := range primaryKey {
		keyColumns[column] = struct{}{}
	}
	// A single-column key reads better inline; a composite one has no inline
	// form at all.
	inlinePrimaryKey := len(primaryKey) <= 1

	defs := make([]string, 0, len(table.Columns)+1+len(table.VersionedForeignKeys))
	declared := make(map[string]struct{}, len(table.Columns))
	for _, column := range table.Columns {
		columnName := strings.TrimSpace(column.Name)
		_, inKey := keyColumns[columnName]
		definition, err := renderColumnDefinition(column, columnDefOptions{
			inlineReferences: inlineReferences,
			inlinePrimaryKey: inlinePrimaryKey,
			notNull:          inKey,
		})
		if err != nil {
			return "", fmt.Errorf("table %q: %w", tableName, err)
		}
		declared[columnName] = struct{}{}
		defs = append(defs, definition)
	}

	if !inlinePrimaryKey {
		for _, column := range primaryKey {
			if _, ok := declared[column]; !ok {
				return "", fmt.Errorf("table %q: primary key column %q is not declared", tableName, column)
			}
		}
		defs = append(defs, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(primaryKey, ", ")))
	}

	vfkDefs, err := renderVersionedFKDefinitions(table)
	if err != nil {
		return "", err
	}
	defs = append(defs, vfkDefs...)

	return fmt.Sprintf("CREATE TABLE %s (\n  %s\n)%s;", tableName, strings.Join(defs, ",\n  "), tableAttributeSuffix(table)), nil
}

// groupDeclaration is one verifiable group waiting to be emitted, held until
// every table is known so the table it references can be checked.
type groupDeclaration struct {
	Table     string
	Group     schemaDDLVerifiableGroup
	Statement string
}

type fkStatement struct {
	Table      string
	Column     string
	RefTable   string
	RefColumn  string
	Constraint string
}

func BuildSchemaDDLScript(request schemaDDLRequest) (schemaDDLResponse, error) {
	domain, err := normalizeSchemaDomain(request.Domain)
	if err != nil {
		return schemaDDLResponse{}, err
	}
	if len(request.Tables) == 0 {
		return schemaDDLResponse{}, fmt.Errorf("at least one table is required")
	}

	tables := make([]schemaDDLTable, len(request.Tables))
	copy(tables, request.Tables)
	sort.SliceStable(tables, func(left, right int) bool {
		return strings.TrimSpace(tables[left].Name) < strings.TrimSpace(tables[right].Name)
	})

	tableNames := map[string]struct{}{}
	createStatements := make([]string, 0, len(tables))
	indexStatements := make([]string, 0)
	verifiableGroups := make([]groupDeclaration, 0)
	foreignKeys := make([]fkStatement, 0)

	for _, table := range tables {
		tableName := strings.TrimSpace(table.Name)
		if !isValidIdentifier(tableName) {
			return schemaDDLResponse{}, fmt.Errorf("invalid table name %q", tableName)
		}
		if _, exists := tableNames[tableName]; exists {
			return schemaDDLResponse{}, fmt.Errorf("duplicate table name %q", tableName)
		}
		tableNames[tableName] = struct{}{}

		if len(table.Columns) == 0 {
			return schemaDDLResponse{}, fmt.Errorf("table %q must contain at least one column", tableName)
		}

		columnNames := map[string]struct{}{}
		for _, column := range table.Columns {
			columnName := strings.TrimSpace(column.Name)
			if !isValidIdentifier(columnName) {
				return schemaDDLResponse{}, fmt.Errorf("invalid column name %q in table %q", columnName, tableName)
			}
			if _, exists := columnNames[columnName]; exists {
				return schemaDDLResponse{}, fmt.Errorf("duplicate column name %q in table %q", columnName, tableName)
			}
			columnNames[columnName] = struct{}{}

			columnType := strings.ToUpper(strings.TrimSpace(column.Type))
			if columnType == "" || !typePattern.MatchString(columnType) {
				return schemaDDLResponse{}, fmt.Errorf("invalid type %q for %s.%s", column.Type, tableName, columnName)
			}

			if column.References != nil {
				refTable := strings.TrimSpace(column.References.Table)
				refColumn := strings.TrimSpace(column.References.Column)
				if !isValidIdentifier(refTable) || !isValidIdentifier(refColumn) {
					return schemaDDLResponse{}, fmt.Errorf("invalid foreign key reference in %s.%s", tableName, columnName)
				}
				foreignKeys = append(foreignKeys, fkStatement{
					Table:      tableName,
					Column:     columnName,
					RefTable:   refTable,
					RefColumn:  refColumn,
					Constraint: fmt.Sprintf("fk_%s_%s__%s_%s", tableName, columnName, refTable, refColumn),
				})
			}
		}

		for _, keyColumn := range tablePrimaryKeyColumns(table) {
			if _, exists := columnNames[keyColumn]; !exists {
				return schemaDDLResponse{}, fmt.Errorf("primary key column %q is not declared in table %q", keyColumn, tableName)
			}
		}

		// The designer emits foreign keys as separate ALTER TABLE statements, so
		// the CREATE TABLE body leaves them out.
		createStatement, err := renderCreateTableStatement(table, false)
		if err != nil {
			return schemaDDLResponse{}, err
		}
		createStatements = append(createStatements, createStatement)

		for _, group := range table.VerifiableGroups {
			groupStatement, groupErr := renderVerifiableGroupStatement(tableName, group)
			if groupErr != nil {
				return schemaDDLResponse{}, groupErr
			}
			if !table.Verifiable {
				return schemaDDLResponse{}, fmt.Errorf("table %q declares a verifiable group but is not VERIFIABLE", tableName)
			}
			verifiableGroups = append(verifiableGroups, groupDeclaration{Table: tableName, Group: group, Statement: groupStatement})
		}

		// Validate and collect index definitions
		indexNames := map[string]struct{}{}
		for _, idx := range table.Indexes {
			idxName := strings.TrimSpace(idx.Name)
			if !isValidIdentifier(idxName) {
				return schemaDDLResponse{}, fmt.Errorf("invalid index name %q on table %q", idxName, tableName)
			}
			if _, exists := indexNames[idxName]; exists {
				return schemaDDLResponse{}, fmt.Errorf("duplicate index name %q on table %q", idxName, tableName)
			}
			indexNames[idxName] = struct{}{}

			if len(idx.Columns) == 0 {
				return schemaDDLResponse{}, fmt.Errorf("index %q on table %q must specify at least one column", idxName, tableName)
			}
			for _, col := range idx.Columns {
				colName := strings.TrimSpace(col)
				if _, exists := columnNames[colName]; !exists {
					return schemaDDLResponse{}, fmt.Errorf("index %q references unknown column %q in table %q", idxName, colName, tableName)
				}
			}

			method := strings.ToLower(strings.TrimSpace(idx.Method))
			if method == "" {
				method = "btree"
			}
			if method != "hash" && method != "btree" {
				return schemaDDLResponse{}, fmt.Errorf("invalid index method %q for index %q on table %q", idx.Method, idxName, tableName)
			}

			colList := strings.Join(idx.Columns, ", ")
			indexStatements = append(indexStatements, fmt.Sprintf("CREATE INDEX %s ON %s (%s) USING %s;", idxName, tableName, colList, method))
		}
	}

	for _, fk := range foreignKeys {
		if _, ok := tableNames[fk.RefTable]; !ok {
			return schemaDDLResponse{}, fmt.Errorf("foreign key reference table %q does not exist", fk.RefTable)
		}
	}

	tablesByName := make(map[string]schemaDDLTable, len(tables))
	for _, table := range tables {
		tablesByName[strings.TrimSpace(table.Name)] = table
	}
	groupStatements := make([]string, 0, len(verifiableGroups))
	for _, declaration := range verifiableGroups {
		references := strings.TrimSpace(declaration.Group.References)
		referenced, ok := tablesByName[references]
		if !ok {
			return schemaDDLResponse{}, fmt.Errorf("verifiable group on table %q references table %q, which does not exist", declaration.Table, references)
		}
		if !referenced.Verifiable {
			return schemaDDLResponse{}, fmt.Errorf("verifiable group on table %q references table %q, which is not VERIFIABLE", declaration.Table, references)
		}
		if key := tablePrimaryKeyColumns(referenced); !sameColumnList(key, declaration.Group.ReferencedColumns) {
			return schemaDDLResponse{}, fmt.Errorf(
				"verifiable group on table %q must reference %q's primary key (%s), not (%s)",
				declaration.Table, references, strings.Join(key, ", "), strings.Join(declaration.Group.ReferencedColumns, ", "),
			)
		}
		groupStatements = append(groupStatements, declaration.Statement)
	}

	sort.SliceStable(foreignKeys, func(left, right int) bool {
		leftKey := fmt.Sprintf("%s.%s", foreignKeys[left].Table, foreignKeys[left].Column)
		rightKey := fmt.Sprintf("%s.%s", foreignKeys[right].Table, foreignKeys[right].Column)
		return leftKey < rightKey
	})

	statements := make([]string, 0, 2+len(createStatements)+len(foreignKeys)+len(indexStatements)+len(groupStatements))
	statements = append(statements, fmt.Sprintf("BEGIN DOMAIN %s;", domain))
	statements = append(statements, createStatements...)
	for _, fk := range foreignKeys {
		statements = append(statements, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s(%s);", fk.Table, fk.Constraint, fk.Column, fk.RefTable, fk.RefColumn))
	}
	statements = append(statements, indexStatements...)
	// Groups come last: both tables have to exist and be VERIFIABLE first.
	statements = append(statements, groupStatements...)
	statements = append(statements, "COMMIT;")

	return schemaDDLResponse{DDL: strings.Join(statements, "\n\n"), Statements: statements}, nil
}

func normalizeSchemaDomain(value string) (string, error) {
	domain := strings.TrimSpace(value)
	if domain == "" {
		domain = "default"
	}
	if !isValidIdentifier(domain) {
		return "", fmt.Errorf("invalid domain name %q", domain)
	}
	return domain, nil
}

func isValidIdentifier(value string) bool {
	return identifierPattern.MatchString(strings.TrimSpace(value))
}
