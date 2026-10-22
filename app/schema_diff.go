package studioapp

import (
	"fmt"
	"sort"
	"strings"
)

type schemaDiffRequest struct {
	Base   schemaDDLRequest `json:"base"`
	Target schemaDDLRequest `json:"target"`
}

type schemaDiffResponse struct {
	Domain     string                `json:"domain"`
	Safe       bool                  `json:"safe"`
	Operations []schemaDiffOperation `json:"operations"`
	Statements []string              `json:"statements"`
	Warnings   []string              `json:"warnings,omitempty"`
}

type schemaDiffOperation struct {
	Type      string `json:"type"`
	Table     string `json:"table"`
	Column    string `json:"column,omitempty"`
	Statement string `json:"statement,omitempty"`
	Safe      bool   `json:"safe"`
	Breaking  bool   `json:"breaking,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func BuildSchemaDiff(base schemaDDLRequest, target schemaDDLRequest) (schemaDiffResponse, error) {
	domain, err := normalizeSchemaDomain(target.Domain)
	if err != nil {
		return schemaDiffResponse{}, err
	}

	if _, err := BuildSchemaDDLScript(target); err != nil {
		return schemaDiffResponse{}, err
	}

	baseDomain := strings.TrimSpace(base.Domain)
	if baseDomain == "" {
		baseDomain = domain
	}
	if baseDomain != domain {
		return schemaDiffResponse{}, fmt.Errorf("base domain %q does not match target domain %q", baseDomain, domain)
	}

	baseTables := map[string]schemaDDLTable{}
	targetTables := map[string]schemaDDLTable{}
	baseEntities := map[string]schemaDDLEntity{}
	targetEntities := map[string]schemaDDLEntity{}

	for _, table := range base.Tables {
		baseTables[strings.TrimSpace(table.Name)] = table
	}
	for _, table := range target.Tables {
		targetTables[strings.TrimSpace(table.Name)] = table
	}
	for _, entity := range base.Entities {
		baseEntities[strings.TrimSpace(entity.Name)] = entity
	}
	for _, entity := range target.Entities {
		targetEntities[strings.TrimSpace(entity.Name)] = entity
	}

	operations := make([]schemaDiffOperation, 0)
	statements := make([]string, 0)
	warnings := make([]string, 0)
	safe := true

	baseNames := sortedTableNames(baseTables)
	targetNames := sortedTableNames(targetTables)

	for _, tableName := range targetNames {
		targetTable := targetTables[tableName]
		baseTable, exists := baseTables[tableName]
		if !exists {
			statement, err := buildCreateTableStatement(targetTable)
			if err != nil {
				return schemaDiffResponse{}, err
			}
			operations = append(operations, schemaDiffOperation{
				Type:      "add_table",
				Table:     tableName,
				Statement: statement,
				Safe:      true,
			})
			statements = append(statements, statement)
			continue
		}

		baseColumns := columnMap(baseTable.Columns)
		targetColumns := columnMap(targetTable.Columns)
		baseColumnNames := sortedColumnNames(baseColumns)
		targetColumnNames := sortedColumnNames(targetColumns)

		for _, columnName := range targetColumnNames {
			targetColumn := targetColumns[columnName]
			baseColumn, columnExists := baseColumns[columnName]
			if !columnExists {
				// FK REFERENCES is folded inline into the ADD COLUMN statement.
				// The parser supports: ALTER TABLE t ADD COLUMN col TYPE REFERENCES ref(col)
				statement := buildAddColumnStatement(tableName, targetColumn)
				operations = append(operations, schemaDiffOperation{
					Type:      "add_column",
					Table:     tableName,
					Column:    columnName,
					Statement: statement,
					Safe:      true,
				})
				statements = append(statements, statement)
				continue
			}

			if !columnsEquivalent(baseColumn, targetColumn) {
				// FK-only changes on existing columns cannot be applied automatically —
				// the engine has no ALTER COLUMN or ADD/DROP CONSTRAINT SQL.
				// Classify by impact and show a clear reason; no Statement emitted.
				if columnCoreEquivalent(baseColumn, targetColumn) {
					switch {
					case baseColumn.References == nil && targetColumn.References != nil:
						safe = false
						operations = append(operations, schemaDiffOperation{
							Type:   "add_foreign_key",
							Table:  tableName,
							Column: columnName,
							Safe:   false,
							Reason: fmt.Sprintf("FK REFERENCES %s(%s) added to existing column — cannot apply automatically; drop and re-add the column with the REFERENCES clause", strings.TrimSpace(targetColumn.References.Table), strings.TrimSpace(targetColumn.References.Column)),
						})
						warnings = append(warnings, fmt.Sprintf("%s.%s: FK added to existing column — requires drop + re-add", tableName, columnName))
					case baseColumn.References != nil && targetColumn.References == nil:
						safe = false
						operations = append(operations, schemaDiffOperation{
							Type:     "drop_foreign_key",
							Table:    tableName,
							Column:   columnName,
							Safe:     false,
							Breaking: true,
							Reason:   "FK constraint removed from existing column — cannot apply automatically; drop and re-add the column without the REFERENCES clause",
						})
						warnings = append(warnings, fmt.Sprintf("%s.%s: FK removed — requires drop + re-add", tableName, columnName))
					default:
						safe = false
						operations = append(operations, schemaDiffOperation{
							Type:   "modify_foreign_key",
							Table:  tableName,
							Column: columnName,
							Safe:   false,
							Reason: "FK target changed on existing column — cannot apply automatically; drop and re-add the column with the new REFERENCES clause",
						})
						warnings = append(warnings, fmt.Sprintf("%s.%s: FK target changed — requires drop + re-add", tableName, columnName))
					}
				} else {
					safe = false
					operations = append(operations, schemaDiffOperation{
						Type:   "modify_column",
						Table:  tableName,
						Column: columnName,
						Safe:   false,
						Reason: "column definition changed; type or constraint changes cannot be applied automatically — manual migration (add new column, backfill, drop old) required to avoid data integrity issues",
					})
					warnings = append(warnings, fmt.Sprintf("%s.%s definition changed — requires manual migration", tableName, columnName))
				}
			}
		}

		for _, columnName := range baseColumnNames {
			if _, stillExists := targetColumns[columnName]; !stillExists {
				safe = false
				operations = append(operations, schemaDiffOperation{
					Type:     "drop_column",
					Table:    tableName,
					Column:   columnName,
					Safe:     false,
					Breaking: true,
					Reason:   "live queries referencing this column will fail immediately; WAL retains all historical values so time-travel still works and data is recoverable via replay — no WAL-level data loss",
				})
				warnings = append(warnings, fmt.Sprintf("%s.%s dropped — current data lost, WAL history preserved; rollback requires manual re-add with backfill", tableName, columnName))
			}
		}

		baseVFKs := versionedFKMap(baseTable.VersionedForeignKeys)
		targetVFKs := versionedFKMap(targetTable.VersionedForeignKeys)

		for _, vfkColumn := range sortedVersionedFKColumns(targetVFKs) {
			targetVFK := targetVFKs[vfkColumn]
			baseVFK, exists := baseVFKs[vfkColumn]
			if !exists {
				safe = false
				operations = append(operations, schemaDiffOperation{
					Type:   "add_versioned_foreign_key",
					Table:  tableName,
					Column: vfkColumn,
					Safe:   false,
					Reason: "versioned foreign key addition changes temporal capture semantics and requires explicit preflight review",
				})
				warnings = append(warnings, fmt.Sprintf("%s.%s added as VERSIONED FOREIGN KEY: review temporal capture and historical semantics", tableName, vfkColumn))
				continue
			}
			if !versionedFKsEquivalent(baseVFK, targetVFK) {
				safe = false
				operations = append(operations, schemaDiffOperation{
					Type:   "modify_versioned_foreign_key",
					Table:  tableName,
					Column: vfkColumn,
					Safe:   false,
					Reason: "versioned foreign key definition changed (likely historical-semantics change)",
				})
				warnings = append(warnings, fmt.Sprintf("%s.%s VERSIONED FOREIGN KEY changed: review captured token semantics, replay impact, and rollback path", tableName, vfkColumn))
			}
		}

		for _, vfkColumn := range sortedVersionedFKColumns(baseVFKs) {
			if _, stillExists := targetVFKs[vfkColumn]; stillExists {
				continue
			}
			safe = false
			operations = append(operations, schemaDiffOperation{
				Type:   "drop_versioned_foreign_key",
				Table:  tableName,
				Column: vfkColumn,
				Safe:   false,
				Reason: "removing a versioned foreign key changes historical reference semantics",
			})
			warnings = append(warnings, fmt.Sprintf("%s.%s VERSIONED FOREIGN KEY removed: historical-reference behavior changes", tableName, vfkColumn))
		}

		// Index diff
		baseIndexes := indexMap(baseTable.Indexes)
		targetIndexes := indexMap(targetTable.Indexes)

		for _, idxName := range sortedIndexNames(targetIndexes) {
			targetIdx := targetIndexes[idxName]
			baseIdx, exists := baseIndexes[idxName]
			if !exists {
				colList := strings.Join(targetIdx.Columns, ", ")
				method := strings.ToLower(strings.TrimSpace(targetIdx.Method))
				if method == "" {
					method = "btree"
				}
				statement := fmt.Sprintf("CREATE INDEX %s ON %s (%s) USING %s;", idxName, tableName, colList, method)
				operations = append(operations, schemaDiffOperation{
					Type:      "add_index",
					Table:     tableName,
					Column:    idxName,
					Statement: statement,
					Safe:      true,
				})
				statements = append(statements, statement)
				continue
			}
			if !indexesEquivalent(baseIdx, targetIdx) {
				colList := strings.Join(targetIdx.Columns, ", ")
				method := strings.ToLower(strings.TrimSpace(targetIdx.Method))
				if method == "" {
					method = "btree"
				}
				modifyStmt := fmt.Sprintf("DROP INDEX %s;\nCREATE INDEX %s ON %s (%s) USING %s;", idxName, idxName, tableName, colList, method)
				operations = append(operations, schemaDiffOperation{
					Type:      "modify_index",
					Table:     tableName,
					Column:    idxName,
					Statement: modifyStmt,
					Safe:      false,
					Breaking:  true,
					Reason:    "index must be dropped and recreated — indexes are derived projections with no stored data, so no WAL-level data loss; queries fall back to full scan during the window",
				})
				warnings = append(warnings, fmt.Sprintf("%s: index %s changed — drop and recreate required", tableName, idxName))
				safe = false
			}
		}

		for _, idxName := range sortedIndexNames(baseIndexes) {
			if _, stillExists := targetIndexes[idxName]; !stillExists {
				operations = append(operations, schemaDiffOperation{
					Type:      "drop_index",
					Table:     tableName,
					Column:    idxName,
					Statement: fmt.Sprintf("DROP INDEX %s;", idxName),
					Safe:      false,
					Breaking:  true,
					Reason:    "indexes are derived projections with no stored data — no WAL-level data loss; live queries fall back to full scans until the index is recreated",
				})
				warnings = append(warnings, fmt.Sprintf("%s: index %s removed — no data loss, but queries using this index will fall back to full scan", tableName, idxName))
				safe = false
			}
		}

		// Primary key. The engine can add one to an existing table, but it
		// cannot drop or replace one, so only the addition carries a statement.
		basePK := tablePrimaryKeyColumns(baseTable)
		targetPK := tablePrimaryKeyColumns(targetTable)
		if !sameColumnList(basePK, targetPK) {
			switch {
			case len(basePK) == 0:
				safe = false
				statement := fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (%s);", tableName, strings.Join(targetPK, ", "))
				operations = append(operations, schemaDiffOperation{
					Type:      "add_primary_key",
					Table:     tableName,
					Column:    strings.Join(targetPK, ", "),
					Statement: statement,
					Safe:      false,
					Reason:    "the engine checks the existing rows first and fails the whole statement if any key column is NULL or any key tuple repeats, so nothing is changed on failure — but a primary key cannot be dropped or replaced afterwards; review the data before applying",
				})
				warnings = append(warnings, fmt.Sprintf("%s: primary key (%s) added — irreversible, and it fails if existing rows have NULL or duplicate keys", tableName, strings.Join(targetPK, ", ")))
			case len(targetPK) == 0:
				safe = false
				operations = append(operations, schemaDiffOperation{
					Type:     "drop_primary_key",
					Table:    tableName,
					Column:   strings.Join(basePK, ", "),
					Safe:     false,
					Breaking: true,
					Reason:   "a primary key cannot be dropped — recreate the table without it and migrate the rows, which rewrites their identity in history",
				})
				warnings = append(warnings, fmt.Sprintf("%s: primary key (%s) removed — requires a table rewrite", tableName, strings.Join(basePK, ", ")))
			default:
				safe = false
				operations = append(operations, schemaDiffOperation{
					Type:     "modify_primary_key",
					Table:    tableName,
					Column:   strings.Join(targetPK, ", "),
					Safe:     false,
					Breaking: true,
					Reason:   fmt.Sprintf("a primary key cannot be replaced — the key is (%s) and cannot become (%s) in place; recreate the table and migrate the rows", strings.Join(basePK, ", "), strings.Join(targetPK, ", ")),
				})
				warnings = append(warnings, fmt.Sprintf("%s: primary key changed from (%s) to (%s) — requires a table rewrite", tableName, strings.Join(basePK, ", "), strings.Join(targetPK, ", ")))
			}
		}

		// APPEND ONLY and VERIFIABLE can be set on an existing table and never
		// unset, so the diff can only ask for them, never take them back.
		for _, attribute := range tableAttributeDiffs(tableName, baseTable, targetTable) {
			safe = false
			operations = append(operations, attribute.operation)
			warnings = append(warnings, attribute.warning)
		}

		// Verifiable groups, which are declared one ALTER at a time and cannot
		// be undropped either.
		groupDiffs, err := verifiableGroupDiffs(tableName, baseTable, targetTable)
		if err != nil {
			return schemaDiffResponse{}, err
		}
		for _, group := range groupDiffs {
			safe = false
			operations = append(operations, group.operation)
			warnings = append(warnings, group.warning)
		}
	}

	for _, tableName := range baseNames {
		if _, exists := targetTables[tableName]; exists {
			continue
		}
		safe = false
		operations = append(operations, schemaDiffOperation{
			Type:      "drop_table",
			Table:     tableName,
			Statement: fmt.Sprintf("DROP TABLE %s;", tableName),
			Safe:      false,
			Breaking:  true,
			Reason:    "live queries against this table will fail immediately; WAL retains all historical rows so time-travel still works and data is recoverable via replay — no WAL-level data loss",
		})
		warnings = append(warnings, fmt.Sprintf("%s dropped — current data lost, WAL history preserved", tableName))
	}

	for _, entityName := range sortedEntityNames(targetEntities) {
		targetEntity := targetEntities[entityName]
		baseEntity, exists := baseEntities[entityName]
		if !exists {
			safe = false
			operations = append(operations, schemaDiffOperation{
				Type:   "add_entity",
				Table:  targetEntity.RootTable,
				Column: entityName,
				Safe:   false,
				Reason: "no data loss, but entity version tracking begins from this LSN forward; rows written before this point will not have captured entity versions and time-travel explanation for those rows will be incomplete — requires explicit migration review",
			})
			warnings = append(warnings, fmt.Sprintf("entity %s added — version tracking starts from this LSN; historical rows pre-date entity capture", entityName))
			continue
		}
		if !entitiesEquivalent(baseEntity, targetEntity) {
			safe = false
			operations = append(operations, schemaDiffOperation{
				Type:   "modify_entity",
				Table:  targetEntity.RootTable,
				Column: entityName,
				Safe:   false,
				Reason: "entity definition changed (root/includes affect historical semantics)",
			})
			warnings = append(warnings, fmt.Sprintf("entity %s changed: review aggregate boundary, captured versions, and replay-visible history", entityName))
		}
	}

	for _, entityName := range sortedEntityNames(baseEntities) {
		if _, exists := targetEntities[entityName]; exists {
			continue
		}
		safe = false
		operations = append(operations, schemaDiffOperation{
			Type:   "drop_entity",
			Column: entityName,
			Safe:   false,
			Reason: "removing an entity changes aggregate-version semantics",
		})
		warnings = append(warnings, fmt.Sprintf("entity %s removed: versioned-reference behavior and historical explanation may change", entityName))
	}

	return schemaDiffResponse{
		Domain:     domain,
		Safe:       safe,
		Operations: operations,
		Statements: statements,
		Warnings:   warnings,
	}, nil
}

func sortedTableNames(tables map[string]schemaDDLTable) []string {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func columnMap(columns []schemaDDLColumn) map[string]schemaDDLColumn {
	mapped := make(map[string]schemaDDLColumn, len(columns))
	for _, column := range columns {
		mapped[strings.TrimSpace(column.Name)] = column
	}
	return mapped
}

func sortedColumnNames(columns map[string]schemaDDLColumn) []string {
	names := make([]string, 0, len(columns))
	for name := range columns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func versionedFKMap(vfks []schemaDDLVersionedFK) map[string]schemaDDLVersionedFK {
	mapped := make(map[string]schemaDDLVersionedFK, len(vfks))
	for _, vfk := range vfks {
		mapped[strings.TrimSpace(vfk.Column)] = vfk
	}
	return mapped
}

func sortedVersionedFKColumns(vfks map[string]schemaDDLVersionedFK) []string {
	names := make([]string, 0, len(vfks))
	for name := range vfks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedEntityNames(entities map[string]schemaDDLEntity) []string {
	names := make([]string, 0, len(entities))
	for name := range entities {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sameColumnList compares two column lists with order significant: the order of
// a primary key's columns is part of the key.
func sameColumnList(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !strings.EqualFold(strings.TrimSpace(left[i]), strings.TrimSpace(right[i])) {
			return false
		}
	}
	return true
}

// tableAttributeDiff is one irreversible table attribute that differs between
// base and target.
type tableAttributeDiff struct {
	operation schemaDiffOperation
	warning   string
}

// tableAttributeDiffs reports the APPEND ONLY and VERIFIABLE changes between
// two versions of a table. Setting either one is expressible as DDL and cannot
// be undone; clearing either one is not expressible at all.
func tableAttributeDiffs(tableName string, base, target schemaDDLTable) []tableAttributeDiff {
	var diffs []tableAttributeDiff

	if target.AppendOnly != base.AppendOnly {
		if target.AppendOnly {
			diffs = append(diffs, tableAttributeDiff{
				operation: schemaDiffOperation{
					Type:      "set_append_only",
					Table:     tableName,
					Statement: fmt.Sprintf("ALTER TABLE %s SET APPEND ONLY;", tableName),
					Safe:      false,
					Reason:    "UPDATE and DELETE on this table start failing for every principal, and the attribute cannot be undone — the rows already written stay as they are",
				},
				warning: fmt.Sprintf("%s: APPEND ONLY set — irreversible, and UPDATE/DELETE on the table stop working", tableName),
			})
		} else {
			diffs = append(diffs, tableAttributeDiff{
				operation: schemaDiffOperation{
					Type:     "unset_append_only",
					Table:    tableName,
					Safe:     false,
					Breaking: true,
					Reason:   "APPEND ONLY cannot be undone — recreate the table without it and migrate the rows",
				},
				warning: fmt.Sprintf("%s: APPEND ONLY cannot be cleared — requires a table rewrite", tableName),
			})
		}
	}

	if target.Verifiable != base.Verifiable {
		if target.Verifiable {
			diffs = append(diffs, tableAttributeDiff{
				operation: schemaDiffOperation{
					Type:      "set_verifiable",
					Table:     tableName,
					Statement: fmt.Sprintf("ALTER TABLE %s SET VERIFIABLE;", tableName),
					Safe:      false,
					Reason:    "the table's live rows join the state tree, so their membership can be proven against a signed digest; it needs a primary key and the ADMIN privilege, restricts nothing, and cannot be undone",
				},
				warning: fmt.Sprintf("%s: VERIFIABLE set — irreversible; the table needs a primary key and the node needs a state key to sign row proofs", tableName),
			})
		} else {
			diffs = append(diffs, tableAttributeDiff{
				operation: schemaDiffOperation{
					Type:     "unset_verifiable",
					Table:    tableName,
					Safe:     false,
					Breaking: true,
					Reason:   "VERIFIABLE cannot be undone — recreate the table without it and migrate the rows",
				},
				warning: fmt.Sprintf("%s: VERIFIABLE cannot be cleared — requires a table rewrite", tableName),
			})
		}
	}

	return diffs
}

// verifiableGroupDiffs reports the groups a table gains or loses. Declaring one
// is expressible as DDL and cannot be undone; removing one is not expressible.
func verifiableGroupDiffs(tableName string, base, target schemaDDLTable) ([]tableAttributeDiff, error) {
	baseGroups := map[string]schemaDDLVerifiableGroup{}
	for _, group := range base.VerifiableGroups {
		baseGroups[verifiableGroupKey(group)] = group
	}
	targetGroups := map[string]schemaDDLVerifiableGroup{}
	for _, group := range target.VerifiableGroups {
		targetGroups[verifiableGroupKey(group)] = group
	}

	var diffs []tableAttributeDiff
	for _, group := range target.VerifiableGroups {
		key := verifiableGroupKey(group)
		columns := strings.Join(group.Columns, ", ")
		existing, exists := baseGroups[key]
		if exists && verifiableGroupsEquivalent(existing, group) {
			continue
		}
		statement, err := renderVerifiableGroupStatement(tableName, group)
		if err != nil {
			return nil, err
		}
		if exists {
			diffs = append(diffs, tableAttributeDiff{
				operation: schemaDiffOperation{
					Type:     "modify_verifiable_group",
					Table:    tableName,
					Column:   columns,
					Safe:     false,
					Breaking: true,
					Reason:   fmt.Sprintf("a verifiable group cannot be changed — the group on (%s) already references %s (%s); recreate the table to change what it references", columns, existing.References, strings.Join(existing.ReferencedColumns, ", ")),
				},
				warning: fmt.Sprintf("%s: verifiable group (%s) changed — requires a table rewrite", tableName, columns),
			})
			continue
		}
		diffs = append(diffs, tableAttributeDiff{
			operation: schemaDiffOperation{
				Type:      "add_verifiable_group",
				Table:     tableName,
				Column:    columns,
				Statement: statement,
				Safe:      false,
				Reason:    fmt.Sprintf("every row of %s gains a group leaf, so a proof can show all the rows of a group and that a row has none; both tables must already be VERIFIABLE, the referenced columns must be %s's primary key, and the group cannot be undone — afterwards DROP TABLE %s and DROP COLUMN of the group's columns are refused", group.References, group.References, group.References),
			},
			warning: fmt.Sprintf("%s: verifiable group (%s) REFERENCES %s added — irreversible, and it locks the referenced table and columns against dropping", tableName, columns, group.References),
		})
	}

	for _, group := range base.VerifiableGroups {
		if _, stillThere := targetGroups[verifiableGroupKey(group)]; stillThere {
			continue
		}
		columns := strings.Join(group.Columns, ", ")
		diffs = append(diffs, tableAttributeDiff{
			operation: schemaDiffOperation{
				Type:     "drop_verifiable_group",
				Table:    tableName,
				Column:   columns,
				Safe:     false,
				Breaking: true,
				Reason:   "a verifiable group cannot be dropped — recreate the table without it and migrate the rows",
			},
			warning: fmt.Sprintf("%s: verifiable group (%s) cannot be removed — requires a table rewrite", tableName, columns),
		})
	}

	return diffs, nil
}

// buildCreateTableStatement renders a table the target has and the base does
// not. A single statement has to carry everything about the table, so foreign
// keys are folded into their column definitions.
func buildCreateTableStatement(table schemaDDLTable) (string, error) {
	return renderCreateTableStatement(table, true)
}

func buildAddColumnStatement(tableName string, column schemaDDLColumn) string {
	def, _ := columnDefinition(column)
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", tableName, def)
}

// columnCoreEquivalent checks column equivalence ignoring the FK References field.
// Use this to detect whether a diff is purely a FK change vs. an actual type/constraint change.
func columnCoreEquivalent(left, right schemaDDLColumn) bool {
	if strings.TrimSpace(left.Name) != strings.TrimSpace(right.Name) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(left.Type), strings.TrimSpace(right.Type)) {
		return false
	}
	if left.Nullable != right.Nullable || left.PrimaryKey != right.PrimaryKey || left.Unique != right.Unique {
		return false
	}
	if strings.TrimSpace(left.DefaultValue) != strings.TrimSpace(right.DefaultValue) {
		return false
	}
	return true
}

// columnDefinition renders one column on its own, for ALTER TABLE ADD COLUMN:
// everything the column carries has to be inline, foreign key included.
func columnDefinition(column schemaDDLColumn) (string, error) {
	return renderColumnDefinition(column, columnDefOptions{inlineReferences: true, inlinePrimaryKey: true})
}

func columnsEquivalent(left schemaDDLColumn, right schemaDDLColumn) bool {
	if strings.TrimSpace(left.Name) != strings.TrimSpace(right.Name) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(left.Type), strings.TrimSpace(right.Type)) {
		return false
	}
	if left.Nullable != right.Nullable || left.PrimaryKey != right.PrimaryKey || left.Unique != right.Unique {
		return false
	}
	if strings.TrimSpace(left.DefaultValue) != strings.TrimSpace(right.DefaultValue) {
		return false
	}
	leftHasRef := left.References != nil
	rightHasRef := right.References != nil
	if leftHasRef != rightHasRef {
		return false
	}
	if !leftHasRef {
		return true
	}
	return strings.TrimSpace(left.References.Table) == strings.TrimSpace(right.References.Table) && strings.TrimSpace(left.References.Column) == strings.TrimSpace(right.References.Column)
}

func versionedFKsEquivalent(left schemaDDLVersionedFK, right schemaDDLVersionedFK) bool {
	return strings.EqualFold(strings.TrimSpace(left.Column), strings.TrimSpace(right.Column)) &&
		strings.EqualFold(strings.TrimSpace(left.LSNColumn), strings.TrimSpace(right.LSNColumn)) &&
		strings.EqualFold(strings.TrimSpace(left.ReferencesDomain), strings.TrimSpace(right.ReferencesDomain)) &&
		strings.EqualFold(strings.TrimSpace(left.ReferencesTable), strings.TrimSpace(right.ReferencesTable)) &&
		strings.EqualFold(strings.TrimSpace(left.ReferencesColumn), strings.TrimSpace(right.ReferencesColumn))
}

func entitiesEquivalent(left schemaDDLEntity, right schemaDDLEntity) bool {
	if !strings.EqualFold(strings.TrimSpace(left.Name), strings.TrimSpace(right.Name)) ||
		!strings.EqualFold(strings.TrimSpace(left.RootTable), strings.TrimSpace(right.RootTable)) {
		return false
	}
	leftTables := append([]string(nil), left.Tables...)
	rightTables := append([]string(nil), right.Tables...)
	for i := range leftTables {
		leftTables[i] = strings.TrimSpace(leftTables[i])
	}
	for i := range rightTables {
		rightTables[i] = strings.TrimSpace(rightTables[i])
	}
	sort.Strings(leftTables)
	sort.Strings(rightTables)
	if len(leftTables) != len(rightTables) {
		return false
	}
	for i := range leftTables {
		if !strings.EqualFold(leftTables[i], rightTables[i]) {
			return false
		}
	}
	return true
}

func indexMap(indexes []schemaDDLIndex) map[string]schemaDDLIndex {
	mapped := make(map[string]schemaDDLIndex, len(indexes))
	for _, idx := range indexes {
		mapped[strings.TrimSpace(idx.Name)] = idx
	}
	return mapped
}

func sortedIndexNames(indexes map[string]schemaDDLIndex) []string {
	names := make([]string, 0, len(indexes))
	for name := range indexes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func indexesEquivalent(left schemaDDLIndex, right schemaDDLIndex) bool {
	if strings.TrimSpace(left.Name) != strings.TrimSpace(right.Name) {
		return false
	}
	leftMethod := strings.ToLower(strings.TrimSpace(left.Method))
	rightMethod := strings.ToLower(strings.TrimSpace(right.Method))
	if leftMethod == "" {
		leftMethod = "btree"
	}
	if rightMethod == "" {
		rightMethod = "btree"
	}
	if leftMethod != rightMethod {
		return false
	}
	if len(left.Columns) != len(right.Columns) {
		return false
	}
	for i := range left.Columns {
		if strings.TrimSpace(left.Columns[i]) != strings.TrimSpace(right.Columns[i]) {
			return false
		}
	}
	return true
}
