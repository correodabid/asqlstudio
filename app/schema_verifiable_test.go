package studioapp

import (
	"encoding/json"
	"strings"
	"testing"

	api "github.com/correodabid/asql/pkg/adminapi"
)

// compositeKeySnapshotJSON is what asql_admin.schema_snapshot returns for a
// domain with a composite-key table, an APPEND ONLY table and a VERIFIABLE
// table. The engine reports a composite key on the table only — never on its
// columns — and adds the _lsn column and the __pk_/__auto_ indexes itself.
const compositeKeySnapshotJSON = `{
  "status": "SNAPSHOT",
  "domains": [
    {
      "name": "shop",
      "tables": [
        {
          "name": "lines",
          "primary_key": ["order_id", "line_no"],
          "columns": [
            {"name": "order_id", "type": "INT"},
            {"name": "line_no", "type": "INT"},
            {"name": "qty", "type": "INT"},
            {"name": "_lsn", "type": "TEXT"}
          ],
          "indexes": [
            {"name": "__pk_lines", "columns": ["order_id", "line_no"], "method": "hash"},
            {"name": "idx_lines_qty", "columns": ["qty"], "method": "btree"}
          ]
        },
        {
          "name": "ledger",
          "append_only": true,
          "primary_key": ["id"],
          "columns": [
            {"name": "id", "type": "INT", "primary_key": true, "unique": true},
            {"name": "amount", "type": "INT"},
            {"name": "_lsn", "type": "TEXT"}
          ],
          "indexes": [
            {"name": "__auto_ledger_id", "columns": ["id"], "method": "hash"}
          ]
        },
        {
          "name": "audited",
          "verifiable": true,
          "primary_key": ["id"],
          "columns": [
            {"name": "id", "type": "INT", "primary_key": true, "unique": true},
            {"name": "who", "type": "TEXT"},
            {"name": "_lsn", "type": "TEXT"}
          ]
        }
      ]
    }
  ]
}`

func decodeSnapshot(t *testing.T, raw string) *api.SchemaSnapshotResponse {
	t.Helper()
	var snapshot api.SchemaSnapshotResponse
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return &snapshot
}

func ddlTableByName(t *testing.T, ddl schemaDDLRequest, name string) schemaDDLTable {
	t.Helper()
	for _, table := range ddl.Tables {
		if table.Name == name {
			return table
		}
	}
	t.Fatalf("baseline has no table %q", name)
	return schemaDDLTable{}
}

func TestSchemaSnapshotToDDLReadsCompositePrimaryKey(t *testing.T) {
	ddl, err := schemaSnapshotToDDL(decodeSnapshot(t, compositeKeySnapshotJSON), "shop")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}

	lines := ddlTableByName(t, ddl, "lines")
	if got, want := lines.PrimaryKey, []string{"order_id", "line_no"}; !sameColumnList(got, want) {
		t.Fatalf("lines primary key = %v, want %v", got, want)
	}
	for _, column := range lines.Columns {
		switch column.Name {
		case "order_id", "line_no":
			if column.Nullable {
				t.Fatalf("key column %s should not be nullable", column.Name)
			}
		case "qty":
			if !column.Nullable {
				t.Fatalf("non-key column %s should stay nullable", column.Name)
			}
		}
	}
}

func TestSchemaSnapshotToDDLReadsTableAttributes(t *testing.T) {
	ddl, err := schemaSnapshotToDDL(decodeSnapshot(t, compositeKeySnapshotJSON), "shop")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}

	if ledger := ddlTableByName(t, ddl, "ledger"); !ledger.AppendOnly || ledger.Verifiable {
		t.Fatalf("ledger append_only=%v verifiable=%v, want true/false", ledger.AppendOnly, ledger.Verifiable)
	}
	if audited := ddlTableByName(t, ddl, "audited"); !audited.Verifiable || audited.AppendOnly {
		t.Fatalf("audited verifiable=%v append_only=%v, want true/false", audited.Verifiable, audited.AppendOnly)
	}
	if lines := ddlTableByName(t, ddl, "lines"); lines.AppendOnly || lines.Verifiable {
		t.Fatal("lines should carry neither attribute")
	}
}

func TestSchemaSnapshotToDDLDropsEngineManagedObjects(t *testing.T) {
	ddl, err := schemaSnapshotToDDL(decodeSnapshot(t, compositeKeySnapshotJSON), "shop")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}

	for _, table := range ddl.Tables {
		for _, column := range table.Columns {
			if column.Name == "_lsn" {
				t.Fatalf("%s: baseline kept the engine-managed column _lsn", table.Name)
			}
		}
		for _, index := range table.Indexes {
			if strings.HasPrefix(index.Name, "__") {
				t.Fatalf("%s: baseline kept the engine-managed index %s", table.Name, index.Name)
			}
		}
	}

	// A user-declared index must survive the filter.
	lines := ddlTableByName(t, ddl, "lines")
	if len(lines.Indexes) != 1 || lines.Indexes[0].Name != "idx_lines_qty" {
		t.Fatalf("lines indexes = %+v, want only idx_lines_qty", lines.Indexes)
	}
}

// TestSchemaBaselineRoundTrip is the whole point of the baseline: the DDL the
// studio regenerates from a snapshot has to describe the same schema.
func TestSchemaBaselineRoundTrip(t *testing.T) {
	ddl, err := schemaSnapshotToDDL(decodeSnapshot(t, compositeKeySnapshotJSON), "shop")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}
	script, err := BuildSchemaDDLScript(ddl)
	if err != nil {
		t.Fatalf("BuildSchemaDDLScript: %v", err)
	}

	for _, want := range []string{
		"PRIMARY KEY (order_id, line_no)",
		"order_id INT NOT NULL",
		"line_no INT NOT NULL",
		"CREATE TABLE ledger (\n  id INT NOT NULL PRIMARY KEY,\n  amount INT\n) APPEND ONLY;",
		"CREATE TABLE audited (\n  id INT NOT NULL PRIMARY KEY,\n  who TEXT\n) VERIFIABLE;",
		"CREATE INDEX idx_lines_qty ON lines (qty) USING btree;",
	} {
		if !strings.Contains(script.DDL, want) {
			t.Fatalf("regenerated DDL is missing %q:\n%s", want, script.DDL)
		}
	}
	for _, unwanted := range []string{"_lsn", "__pk_lines", "__auto_ledger_id"} {
		if strings.Contains(script.DDL, unwanted) {
			t.Fatalf("regenerated DDL leaked %q:\n%s", unwanted, script.DDL)
		}
	}

	// A baseline diffed against itself must be empty, or every apply would
	// propose spurious migrations.
	diff, err := BuildSchemaDiff(ddl, ddl)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}
	if !diff.Safe || len(diff.Operations) != 0 || len(diff.Statements) != 0 {
		t.Fatalf("self-diff is not empty: safe=%v operations=%+v statements=%v", diff.Safe, diff.Operations, diff.Statements)
	}
}

func TestBuildSchemaDDLScriptRejectsUndeclaredKeyColumn(t *testing.T) {
	_, err := BuildSchemaDDLScript(schemaDDLRequest{
		Domain: "shop",
		Tables: []schemaDDLTable{{
			Name:       "lines",
			PrimaryKey: []string{"order_id", "missing"},
			Columns: []schemaDDLColumn{
				{Name: "order_id", Type: "INT"},
				{Name: "qty", Type: "INT", Nullable: true},
			},
		}},
	})
	if err == nil {
		t.Fatal("expected an error for a key column that is not declared")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error should name the missing column, got %v", err)
	}
}

func TestBuildSchemaDDLScriptRendersBothTableAttributes(t *testing.T) {
	script, err := BuildSchemaDDLScript(schemaDDLRequest{
		Domain: "shop",
		Tables: []schemaDDLTable{{
			Name:       "ledger",
			AppendOnly: true,
			Verifiable: true,
			Columns:    []schemaDDLColumn{{Name: "id", Type: "INT", PrimaryKey: true}},
		}},
	})
	if err != nil {
		t.Fatalf("BuildSchemaDDLScript: %v", err)
	}
	if !strings.Contains(script.DDL, ") APPEND ONLY VERIFIABLE;") {
		t.Fatalf("expected both attributes after the column list:\n%s", script.DDL)
	}
}

func TestSchemaDiffAddsPrimaryKey(t *testing.T) {
	base := schemaDDLRequest{Domain: "shop", Tables: []schemaDDLTable{{
		Name:    "lines",
		Columns: []schemaDDLColumn{{Name: "order_id", Type: "INT", Nullable: true}, {Name: "line_no", Type: "INT", Nullable: true}},
	}}}
	target := schemaDDLRequest{Domain: "shop", Tables: []schemaDDLTable{{
		Name:       "lines",
		PrimaryKey: []string{"order_id", "line_no"},
		Columns:    []schemaDDLColumn{{Name: "order_id", Type: "INT"}, {Name: "line_no", Type: "INT"}},
	}}}

	diff, err := BuildSchemaDiff(base, target)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}

	operation := diffOperationOfType(t, diff, "add_primary_key")
	if operation.Statement != "ALTER TABLE lines ADD PRIMARY KEY (order_id, line_no);" {
		t.Fatalf("unexpected statement %q", operation.Statement)
	}
	if operation.Safe {
		t.Fatal("adding a primary key cannot be applied unreviewed")
	}
	// Unreviewed statements are the ones in diff.Statements; this must not be
	// one of them.
	for _, statement := range diff.Statements {
		if strings.Contains(statement, "ADD PRIMARY KEY") {
			t.Fatalf("ADD PRIMARY KEY must not be auto-applied, found %q", statement)
		}
	}
}

func TestSchemaDiffReportsPrimaryKeyChangeAsUnrepairable(t *testing.T) {
	base := schemaDDLRequest{Domain: "shop", Tables: []schemaDDLTable{{
		Name:       "lines",
		PrimaryKey: []string{"order_id"},
		Columns:    []schemaDDLColumn{{Name: "order_id", Type: "INT"}, {Name: "line_no", Type: "INT", Nullable: true}},
	}}}
	target := schemaDDLRequest{Domain: "shop", Tables: []schemaDDLTable{{
		Name:       "lines",
		PrimaryKey: []string{"order_id", "line_no"},
		Columns:    []schemaDDLColumn{{Name: "order_id", Type: "INT"}, {Name: "line_no", Type: "INT"}},
	}}}

	diff, err := BuildSchemaDiff(base, target)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}
	operation := diffOperationOfType(t, diff, "modify_primary_key")
	if operation.Statement != "" {
		t.Fatalf("a primary key change has no DDL, got %q", operation.Statement)
	}
	if !operation.Breaking {
		t.Fatal("a primary key change should be reported as breaking")
	}
}

func TestSchemaDiffSetsTableAttributes(t *testing.T) {
	base := schemaDDLRequest{Domain: "shop", Tables: []schemaDDLTable{{
		Name:    "ledger",
		Columns: []schemaDDLColumn{{Name: "id", Type: "INT", PrimaryKey: true}},
	}}}
	target := schemaDDLRequest{Domain: "shop", Tables: []schemaDDLTable{{
		Name:       "ledger",
		AppendOnly: true,
		Verifiable: true,
		Columns:    []schemaDDLColumn{{Name: "id", Type: "INT", PrimaryKey: true}},
	}}}

	diff, err := BuildSchemaDiff(base, target)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}
	if got := diffOperationOfType(t, diff, "set_append_only").Statement; got != "ALTER TABLE ledger SET APPEND ONLY;" {
		t.Fatalf("unexpected append-only statement %q", got)
	}
	if got := diffOperationOfType(t, diff, "set_verifiable").Statement; got != "ALTER TABLE ledger SET VERIFIABLE;" {
		t.Fatalf("unexpected verifiable statement %q", got)
	}
	if diff.Safe {
		t.Fatal("irreversible attribute changes must not be reported as safe")
	}

	// Clearing them is not expressible, and the diff has to say so.
	reverse, err := BuildSchemaDiff(target, base)
	if err != nil {
		t.Fatalf("BuildSchemaDiff reverse: %v", err)
	}
	for _, operationType := range []string{"unset_append_only", "unset_verifiable"} {
		operation := diffOperationOfType(t, reverse, operationType)
		if operation.Statement != "" {
			t.Fatalf("%s has no DDL, got %q", operationType, operation.Statement)
		}
		if !operation.Breaking {
			t.Fatalf("%s should be reported as breaking", operationType)
		}
	}
}

// TestSchemaDiffAddTableKeepsEverything covers a gap the shared renderer
// closed: the diff used to build CREATE TABLE on its own and silently dropped
// the versioned foreign keys and table attributes of a new table.
func TestSchemaDiffAddTableKeepsEverything(t *testing.T) {
	target := schemaDDLRequest{Domain: "shop", Tables: []schemaDDLTable{{
		Name:       "lines",
		PrimaryKey: []string{"order_id", "line_no"},
		AppendOnly: true,
		Columns: []schemaDDLColumn{
			{Name: "order_id", Type: "INT"},
			{Name: "line_no", Type: "INT"},
			{Name: "recipe_lsn", Type: "INT", Nullable: true},
		},
		VersionedForeignKeys: []schemaDDLVersionedFK{{
			Column: "order_id", LSNColumn: "recipe_lsn",
			ReferencesTable: "orders", ReferencesColumn: "id",
		}},
	}}}

	diff, err := BuildSchemaDiff(schemaDDLRequest{Domain: "shop"}, target)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}
	statement := diffOperationOfType(t, diff, "add_table").Statement
	for _, want := range []string{
		"PRIMARY KEY (order_id, line_no)",
		"VERSIONED FOREIGN KEY (order_id) REFERENCES orders(id) AS OF recipe_lsn",
		") APPEND ONLY;",
	} {
		if !strings.Contains(statement, want) {
			t.Fatalf("add_table statement is missing %q:\n%s", want, statement)
		}
	}
}

func TestSchemaTablesReportsCompositeKey(t *testing.T) {
	snapshot := decodeSnapshot(t, compositeKeySnapshotJSON)
	var lines api.SchemaSnapshotTable
	for _, table := range snapshot.Domains[0].Tables {
		if table.Name == "lines" {
			lines = table
		}
	}
	if got, want := snapshotPrimaryKeyColumns(lines), []string{"order_id", "line_no"}; !sameColumnList(got, want) {
		t.Fatalf("snapshotPrimaryKeyColumns = %v, want %v", got, want)
	}

	// An engine older than v0.5.0 reports no table-level key, only the flag.
	legacy := api.SchemaSnapshotTable{
		Name:    "items",
		Columns: []api.SchemaSnapshotColumn{{Name: "id", Type: "INT", PrimaryKey: true}, {Name: "name", Type: "TEXT"}},
	}
	if got, want := snapshotPrimaryKeyColumns(legacy), []string{"id"}; !sameColumnList(got, want) {
		t.Fatalf("legacy snapshot key = %v, want %v", got, want)
	}
}

func diffOperationOfType(t *testing.T, diff schemaDiffResponse, operationType string) schemaDiffOperation {
	t.Helper()
	for _, operation := range diff.Operations {
		if operation.Type == operationType {
			return operation
		}
	}
	t.Fatalf("diff has no %s operation: %+v", operationType, diff.Operations)
	return schemaDiffOperation{}
}

// ── Verifiable groups ───────────────────────────────────────────────────────

const verifiableGroupSnapshotJSON = `{
  "status": "SNAPSHOT",
  "domains": [
    {
      "name": "lab",
      "tables": [
        {
          "name": "artifacts",
          "verifiable": true,
          "primary_key": ["id"],
          "columns": [
            {"name": "id", "type": "TEXT", "primary_key": true, "unique": true},
            {"name": "name", "type": "TEXT"},
            {"name": "_lsn", "type": "TEXT"}
          ]
        },
        {
          "name": "artifact_parents",
          "verifiable": true,
          "primary_key": ["child_id", "parent_id"],
          "columns": [
            {"name": "child_id", "type": "TEXT"},
            {"name": "parent_id", "type": "TEXT"},
            {"name": "_lsn", "type": "TEXT"}
          ],
          "verifiable_groups": [
            {"columns": ["child_id"], "references": "artifacts", "referenced_columns": ["id"]}
          ]
        }
      ]
    }
  ]
}`

func TestSchemaSnapshotToDDLReadsVerifiableGroups(t *testing.T) {
	ddl, err := schemaSnapshotToDDL(decodeSnapshot(t, verifiableGroupSnapshotJSON), "lab")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}

	parents := ddlTableByName(t, ddl, "artifact_parents")
	if len(parents.VerifiableGroups) != 1 {
		t.Fatalf("artifact_parents groups = %+v, want one", parents.VerifiableGroups)
	}
	group := parents.VerifiableGroups[0]
	if !sameColumnList(group.Columns, []string{"child_id"}) {
		t.Fatalf("group columns = %v, want [child_id]", group.Columns)
	}
	if group.References != "artifacts" || !sameColumnList(group.ReferencedColumns, []string{"id"}) {
		t.Fatalf("group references %s (%v), want artifacts (id)", group.References, group.ReferencedColumns)
	}
	if artifacts := ddlTableByName(t, ddl, "artifacts"); len(artifacts.VerifiableGroups) != 0 {
		t.Fatalf("the referenced table declares no group, got %+v", artifacts.VerifiableGroups)
	}
}

func TestBuildSchemaDDLScriptDeclaresGroupsAfterTheTables(t *testing.T) {
	ddl, err := schemaSnapshotToDDL(decodeSnapshot(t, verifiableGroupSnapshotJSON), "lab")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}
	script, err := BuildSchemaDDLScript(ddl)
	if err != nil {
		t.Fatalf("BuildSchemaDDLScript: %v", err)
	}

	const want = "ALTER TABLE artifact_parents ADD VERIFIABLE GROUP (child_id) REFERENCES artifacts (id);"
	groupAt, createAt := -1, -1
	for i, statement := range script.Statements {
		if statement == want {
			groupAt = i
		}
		if strings.HasPrefix(statement, "CREATE TABLE artifacts ") {
			createAt = i
		}
	}
	if groupAt < 0 {
		t.Fatalf("the script does not declare the group:\n%s", script.DDL)
	}
	// The referenced table has to exist and be VERIFIABLE before the group.
	if createAt < 0 || groupAt < createAt {
		t.Fatalf("the group is declared before the table it references:\n%s", script.DDL)
	}
	if !strings.Contains(script.DDL, "CREATE TABLE artifacts (\n  id TEXT NOT NULL PRIMARY KEY,\n  name TEXT\n) VERIFIABLE;") {
		t.Fatalf("the referenced table is not VERIFIABLE:\n%s", script.DDL)
	}
}

func TestSchemaDiffAddsVerifiableGroup(t *testing.T) {
	base, err := schemaSnapshotToDDL(decodeSnapshot(t, verifiableGroupSnapshotJSON), "lab")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Strip the group from the base so the target asks for it.
	withoutGroup := schemaDDLRequest{Domain: base.Domain}
	for _, table := range base.Tables {
		table.VerifiableGroups = nil
		withoutGroup.Tables = append(withoutGroup.Tables, table)
	}

	diff, err := BuildSchemaDiff(withoutGroup, base)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}
	operation := diffOperationOfType(t, diff, "add_verifiable_group")
	if operation.Statement != "ALTER TABLE artifact_parents ADD VERIFIABLE GROUP (child_id) REFERENCES artifacts (id);" {
		t.Fatalf("unexpected statement %q", operation.Statement)
	}
	if operation.Safe {
		t.Fatal("declaring a group cannot be applied unreviewed: it cannot be undone")
	}
	for _, statement := range diff.Statements {
		if strings.Contains(statement, "VERIFIABLE GROUP") {
			t.Fatalf("ADD VERIFIABLE GROUP must not be auto-applied, found %q", statement)
		}
	}

	// Removing one is not expressible at all.
	reverse, err := BuildSchemaDiff(base, withoutGroup)
	if err != nil {
		t.Fatalf("BuildSchemaDiff reverse: %v", err)
	}
	dropped := diffOperationOfType(t, reverse, "drop_verifiable_group")
	if dropped.Statement != "" {
		t.Fatalf("dropping a group has no DDL, got %q", dropped.Statement)
	}
	if !dropped.Breaking {
		t.Fatal("dropping a group should be reported as breaking")
	}
}

func TestSchemaDiffReportsChangedVerifiableGroup(t *testing.T) {
	// Both referenced tables exist and are VERIFIABLE, so the only difference
	// is which one the group points at.
	referenced := []schemaDDLTable{
		{
			Name:       "artifacts",
			Verifiable: true,
			Columns:    []schemaDDLColumn{{Name: "id", Type: "TEXT", PrimaryKey: true}},
		},
		{
			Name:       "runs",
			Verifiable: true,
			Columns:    []schemaDDLColumn{{Name: "id", Type: "TEXT", PrimaryKey: true}},
		},
	}
	parents := func(references string) schemaDDLTable {
		return schemaDDLTable{
			Name:       "artifact_parents",
			Verifiable: true,
			PrimaryKey: []string{"child_id", "parent_id"},
			Columns:    []schemaDDLColumn{{Name: "child_id", Type: "TEXT"}, {Name: "parent_id", Type: "TEXT"}},
			VerifiableGroups: []schemaDDLVerifiableGroup{
				{Columns: []string{"child_id"}, References: references, ReferencedColumns: []string{"id"}},
			},
		}
	}
	base := schemaDDLRequest{Domain: "lab", Tables: append(append([]schemaDDLTable{}, referenced...), parents("artifacts"))}
	target := schemaDDLRequest{Domain: "lab", Tables: append(append([]schemaDDLTable{}, referenced...), parents("runs"))}

	diff, err := BuildSchemaDiff(base, target)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}
	operation := diffOperationOfType(t, diff, "modify_verifiable_group")
	if operation.Statement != "" {
		t.Fatalf("a changed group has no DDL, got %q", operation.Statement)
	}
	if !strings.Contains(operation.Reason, "artifacts") {
		t.Fatalf("the reason should name the group it already references, got %q", operation.Reason)
	}
}

func TestRenderVerifiableGroupStatementValidatesIdentifiers(t *testing.T) {
	for _, test := range []struct {
		name  string
		group schemaDDLVerifiableGroup
	}{
		{name: "no columns", group: schemaDDLVerifiableGroup{References: "artifacts", ReferencedColumns: []string{"id"}}},
		{name: "no referenced columns", group: schemaDDLVerifiableGroup{Columns: []string{"child_id"}, References: "artifacts"}},
		{name: "bad reference", group: schemaDDLVerifiableGroup{Columns: []string{"child_id"}, References: "artifacts; DROP TABLE x", ReferencedColumns: []string{"id"}}},
		{name: "bad column", group: schemaDDLVerifiableGroup{Columns: []string{"child_id)"}, References: "artifacts", ReferencedColumns: []string{"id"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := renderVerifiableGroupStatement("artifact_parents", test.group); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestBuildSchemaDDLScriptValidatesVerifiableGroupReference(t *testing.T) {
	group := schemaDDLVerifiableGroup{Columns: []string{"child_id"}, References: "artifacts", ReferencedColumns: []string{"id"}}
	parents := schemaDDLTable{
		Name:             "artifact_parents",
		Verifiable:       true,
		PrimaryKey:       []string{"child_id", "parent_id"},
		Columns:          []schemaDDLColumn{{Name: "child_id", Type: "TEXT"}, {Name: "parent_id", Type: "TEXT"}},
		VerifiableGroups: []schemaDDLVerifiableGroup{group},
	}

	tests := []struct {
		name   string
		tables []schemaDDLTable
		want   string
	}{
		{
			name:   "referenced table missing",
			tables: []schemaDDLTable{parents},
			want:   "does not exist",
		},
		{
			name: "referenced table not verifiable",
			tables: []schemaDDLTable{
				{Name: "artifacts", Columns: []schemaDDLColumn{{Name: "id", Type: "TEXT", PrimaryKey: true}}},
				parents,
			},
			want: "not VERIFIABLE",
		},
		{
			name: "referenced columns are not the key",
			tables: []schemaDDLTable{
				{
					Name:       "artifacts",
					Verifiable: true,
					Columns:    []schemaDDLColumn{{Name: "id", Type: "TEXT", PrimaryKey: true}, {Name: "slug", Type: "TEXT", Nullable: true}},
				},
				func() schemaDDLTable {
					t := parents
					t.VerifiableGroups = []schemaDDLVerifiableGroup{{Columns: []string{"child_id"}, References: "artifacts", ReferencedColumns: []string{"slug"}}}
					return t
				}(),
			},
			want: "primary key",
		},
		{
			name: "declaring table not verifiable",
			tables: []schemaDDLTable{
				{Name: "artifacts", Verifiable: true, Columns: []schemaDDLColumn{{Name: "id", Type: "TEXT", PrimaryKey: true}}},
				func() schemaDDLTable {
					t := parents
					t.Verifiable = false
					return t
				}(),
			},
			want: "is not VERIFIABLE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildSchemaDDLScript(schemaDDLRequest{Domain: "lab", Tables: test.tables})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q should mention %q", err, test.want)
			}
		})
	}
}

// TestSchemaSnapshotToDDLKeepsDeclaredNotNull covers the not_null the catalog
// reports from v0.7.0 on. Before it, a column declared NOT NULL that was not
// part of the key came back nullable, so the regenerated DDL dropped the
// constraint.
func TestSchemaSnapshotToDDLKeepsDeclaredNotNull(t *testing.T) {
	const snapshot = `{
      "status": "SNAPSHOT",
      "domains": [{
        "name": "shop",
        "tables": [{
          "name": "items",
          "primary_key": ["id"],
          "columns": [
            {"name": "id", "type": "INT", "primary_key": true, "not_null": true, "unique": true},
            {"name": "sku", "type": "TEXT", "not_null": true},
            {"name": "note", "type": "TEXT"}
          ]
        }]
      }]
    }`

	ddl, err := schemaSnapshotToDDL(decodeSnapshot(t, snapshot), "shop")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}
	items := ddlTableByName(t, ddl, "items")
	nullable := map[string]bool{}
	for _, column := range items.Columns {
		nullable[column.Name] = column.Nullable
	}
	if nullable["sku"] {
		t.Fatal("sku is declared NOT NULL and must not come back nullable")
	}
	if nullable["id"] {
		t.Fatal("a key column must not come back nullable")
	}
	if !nullable["note"] {
		t.Fatal("note is nullable and must stay so")
	}

	script, err := BuildSchemaDDLScript(ddl)
	if err != nil {
		t.Fatalf("BuildSchemaDDLScript: %v", err)
	}
	if !strings.Contains(script.DDL, "sku TEXT NOT NULL") {
		t.Fatalf("the regenerated DDL dropped NOT NULL on sku:\n%s", script.DDL)
	}
	if strings.Contains(script.DDL, "note TEXT NOT NULL") {
		t.Fatalf("the regenerated DDL invented NOT NULL on note:\n%s", script.DDL)
	}
}

// TestBuildSchemaDDLScriptRejectsEngineManagedColumn covers the column the
// engine maintains: declaring _lsn fails with 42701 from asql v0.8.0 on, so the
// studio says why rather than letting the statement fail.
func TestBuildSchemaDDLScriptRejectsEngineManagedColumn(t *testing.T) {
	_, err := BuildSchemaDDLScript(schemaDDLRequest{
		Domain: "shop",
		Tables: []schemaDDLTable{{
			Name: "items",
			Columns: []schemaDDLColumn{
				{Name: "id", Type: "INT", PrimaryKey: true},
				{Name: "_lsn", Type: "TEXT", Nullable: true},
			},
		}},
	})
	if err == nil {
		t.Fatal("expected an error for a column the engine maintains")
	}
	if !strings.Contains(err.Error(), "_lsn") || !strings.Contains(err.Error(), "engine") {
		t.Fatalf("the error should name the column and say the engine owns it, got %v", err)
	}

	// The ADD COLUMN path goes through the same renderer.
	if _, err := columnDefinition(schemaDDLColumn{Name: "_lsn", Type: "TEXT", Nullable: true}); err == nil {
		t.Fatal("expected ALTER TABLE ADD COLUMN _lsn to be rejected too")
	}
}
