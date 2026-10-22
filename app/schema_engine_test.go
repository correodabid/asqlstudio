package studioapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	api "github.com/correodabid/asql/pkg/adminapi"
	"github.com/correodabid/asql/pkg/fixtures"
)

// These tests put the baseline and the diff in front of a real engine. The
// features they cover landed in asql v0.5.0, so against an older engine they
// skip rather than fail: the studio has to work with both.

// requireVerifiableGroups skips when the engine predates v0.6.0.
func requireVerifiableGroups(t *testing.T, client *engineClient) {
	t.Helper()
	requireVerifiableRows(t, client)
	ctx := context.Background()
	begin, err := client.BeginTx(ctx, &api.BeginTxRequest{Mode: "domain", Domains: []string{"groupprobe"}})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _, _ = client.RollbackTx(ctx, &api.RollbackTxRequest{TxID: begin.TxID}) }()
	for _, sql := range []string{
		"CREATE TABLE probe_parent (id TEXT PRIMARY KEY) VERIFIABLE",
		"CREATE TABLE probe_edges (child_id TEXT NOT NULL, parent_id TEXT NOT NULL, PRIMARY KEY (child_id, parent_id)) VERIFIABLE",
		"ALTER TABLE probe_edges ADD VERIFIABLE GROUP (child_id) REFERENCES probe_parent (id)",
	} {
		if _, err := client.Execute(ctx, &api.ExecuteRequest{TxID: begin.TxID, SQL: sql}); err != nil {
			t.Skipf("this engine does not support verifiable groups (%v); upgrade to asql v0.6.0 to run this test", err)
		}
	}
}

func requireVerifiableRows(t *testing.T, client *engineClient) {
	t.Helper()
	ctx := context.Background()
	begin, err := client.BeginTx(ctx, &api.BeginTxRequest{Mode: "domain", Domains: []string{"probe"}})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _, _ = client.RollbackTx(ctx, &api.RollbackTxRequest{TxID: begin.TxID}) }()

	for _, sql := range []string{
		"CREATE TABLE probe_composite (a INT NOT NULL, b INT NOT NULL, PRIMARY KEY (a, b))",
		"CREATE TABLE probe_verifiable (id INT PRIMARY KEY) VERIFIABLE",
	} {
		if _, err := client.Execute(ctx, &api.ExecuteRequest{TxID: begin.TxID, SQL: sql}); err != nil {
			t.Skipf("this engine does not support verifiable rows (%v); upgrade to asql v0.5.0 to run this test", err)
		}
	}
}

func newSchemaEngineClient(t *testing.T) *engineClient {
	t.Helper()
	client := newEngineClient(startStudioPGWireServer(t, ""), "")
	t.Cleanup(client.Close)
	return client
}

func applyStatements(t *testing.T, client *engineClient, domain string, statements []string) {
	t.Helper()
	ctx := context.Background()
	begin, err := client.BeginTx(ctx, &api.BeginTxRequest{Mode: "domain", Domains: []string{domain}})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, statement := range statements {
		trimmed := strings.TrimSuffix(strings.TrimSpace(statement), ";")
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "BEGIN DOMAIN") || upper == "COMMIT" {
			continue
		}
		if _, err := client.Execute(ctx, &api.ExecuteRequest{TxID: begin.TxID, SQL: trimmed}); err != nil {
			_, _ = client.RollbackTx(ctx, &api.RollbackTxRequest{TxID: begin.TxID})
			t.Fatalf("apply %q: %v", trimmed, err)
		}
	}
	if _, err := client.CommitTx(ctx, &api.CommitTxRequest{TxID: begin.TxID}); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func liveSnapshot(t *testing.T, client *engineClient, domain string) *api.SchemaSnapshotResponse {
	t.Helper()
	snapshot, err := client.SchemaSnapshot(context.Background(), &api.SchemaSnapshotRequest{Domains: []string{domain}})
	if err != nil {
		t.Fatalf("SchemaSnapshot: %v", err)
	}
	return snapshot
}

// TestEngineBaselineRoundTrip applies the DDL the studio regenerates from a
// live snapshot to a second engine and compares the two snapshots. Anything the
// baseline drops or garbles shows up as a difference.
func TestEngineBaselineRoundTrip(t *testing.T) {
	source := newSchemaEngineClient(t)
	requireVerifiableRows(t, source)

	applyStatements(t, source, "shop", []string{
		"CREATE TABLE lines (order_id INT NOT NULL, line_no INT NOT NULL, qty INT, PRIMARY KEY (order_id, line_no))",
		"CREATE TABLE ledger (id INT PRIMARY KEY, amount INT) APPEND ONLY",
		"CREATE TABLE audited (id INT PRIMARY KEY, who TEXT) VERIFIABLE",
		// sku is NOT NULL without being part of the key: only the catalog's
		// not_null carries that, so a baseline that ignored it would drop it.
		"CREATE TABLE items (id INT PRIMARY KEY, sku TEXT NOT NULL, note TEXT)",
		"CREATE INDEX idx_lines_qty ON lines (qty) USING btree",
	})

	original := liveSnapshot(t, source, "shop")
	baseline, err := schemaSnapshotToDDL(original, "shop")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}
	script, err := BuildSchemaDDLScript(baseline)
	if err != nil {
		t.Fatalf("BuildSchemaDDLScript: %v", err)
	}

	replica := newSchemaEngineClient(t)
	applyStatements(t, replica, "shop", script.Statements)

	want, err := json.Marshal(original.Domains)
	if err != nil {
		t.Fatalf("marshal original: %v", err)
	}
	got, err := json.Marshal(liveSnapshot(t, replica, "shop").Domains)
	if err != nil {
		t.Fatalf("marshal replica: %v", err)
	}
	if string(want) != string(got) {
		t.Fatalf("the regenerated DDL describes a different schema\noriginal: %s\nreplica:  %s\nDDL:\n%s", want, got, script.DDL)
	}
}

// TestEngineAcceptsDiffStatements feeds the ALTER statements the diff emits for
// a primary key and the irreversible table attributes to a real engine, and
// checks the engine then reports what the diff asked for.
func TestEngineAcceptsDiffStatements(t *testing.T) {
	client := newSchemaEngineClient(t)
	requireVerifiableRows(t, client)

	applyStatements(t, client, "shop", []string{
		"CREATE TABLE lines (order_id INT NOT NULL, line_no INT NOT NULL, qty INT)",
		"CREATE TABLE ledger (id INT PRIMARY KEY, amount INT)",
	})

	base, err := schemaSnapshotToDDL(liveSnapshot(t, client, "shop"), "shop")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	target := schemaDDLRequest{Domain: "shop"}
	for _, table := range base.Tables {
		switch table.Name {
		case "lines":
			table.PrimaryKey = []string{"order_id", "line_no"}
		case "ledger":
			table.AppendOnly = true
			table.Verifiable = true
		}
		target.Tables = append(target.Tables, table)
	}

	diff, err := BuildSchemaDiff(base, target)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}

	var statements []string
	for _, operation := range diff.Operations {
		if operation.Statement != "" {
			statements = append(statements, operation.Statement)
		}
	}
	if len(statements) != 3 {
		t.Fatalf("the diff emitted %d statements, want 3 (primary key, append only, verifiable): %+v", len(statements), diff.Operations)
	}
	applyStatements(t, client, "shop", statements)

	for _, table := range liveSnapshot(t, client, "shop").Domains[0].Tables {
		switch table.Name {
		case "lines":
			if got, want := snapshotPrimaryKeyColumns(table), []string{"order_id", "line_no"}; !sameColumnList(got, want) {
				t.Fatalf("lines key = %v, want %v", got, want)
			}
		case "ledger":
			if !table.AppendOnly || !table.Verifiable {
				t.Fatalf("ledger append_only=%v verifiable=%v, want both set", table.AppendOnly, table.Verifiable)
			}
		}
	}
}

// TestEngineVerifiableGroupRoundTrip declares a group on a real engine, reads
// it back through the baseline, and replays the regenerated DDL on a second
// engine. A group has no CREATE TABLE form, so the script has to order the
// ALTER after both tables exist and are VERIFIABLE.
func TestEngineVerifiableGroupRoundTrip(t *testing.T) {
	source := newSchemaEngineClient(t)
	requireVerifiableGroups(t, source)

	applyStatements(t, source, "lab", []string{
		"CREATE TABLE artifacts (id TEXT PRIMARY KEY, name TEXT) VERIFIABLE",
		"CREATE TABLE artifact_parents (child_id TEXT NOT NULL, parent_id TEXT NOT NULL, PRIMARY KEY (child_id, parent_id)) VERIFIABLE",
		"ALTER TABLE artifact_parents ADD VERIFIABLE GROUP (child_id) REFERENCES artifacts (id)",
	})

	original := liveSnapshot(t, source, "lab")
	var groups []api.SchemaSnapshotVerifiableGroup
	for _, table := range original.Domains[0].Tables {
		if table.Name == "artifact_parents" {
			groups = table.VerifiableGroups
		}
	}
	if len(groups) != 1 {
		t.Fatalf("the engine reported %d groups on artifact_parents, want one", len(groups))
	}
	if groups[0].References != "artifacts" || !sameColumnList(groups[0].Columns, []string{"child_id"}) {
		t.Fatalf("unexpected group %+v", groups[0])
	}

	baseline, err := schemaSnapshotToDDL(original, "lab")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}
	script, err := BuildSchemaDDLScript(baseline)
	if err != nil {
		t.Fatalf("BuildSchemaDDLScript: %v", err)
	}

	replica := newSchemaEngineClient(t)
	applyStatements(t, replica, "lab", script.Statements)

	want, err := json.Marshal(original.Domains)
	if err != nil {
		t.Fatalf("marshal original: %v", err)
	}
	got, err := json.Marshal(liveSnapshot(t, replica, "lab").Domains)
	if err != nil {
		t.Fatalf("marshal replica: %v", err)
	}
	if string(want) != string(got) {
		t.Fatalf("the regenerated DDL describes a different schema\noriginal: %s\nreplica:  %s\nDDL:\n%s", want, got, script.DDL)
	}
}

// TestEngineAcceptsVerifiableGroupDiff feeds the ALTER the diff emits for a new
// group to a real engine.
func TestEngineAcceptsVerifiableGroupDiff(t *testing.T) {
	client := newSchemaEngineClient(t)
	requireVerifiableGroups(t, client)

	applyStatements(t, client, "lab", []string{
		"CREATE TABLE artifacts (id TEXT PRIMARY KEY, name TEXT) VERIFIABLE",
		"CREATE TABLE artifact_parents (child_id TEXT NOT NULL, parent_id TEXT NOT NULL, PRIMARY KEY (child_id, parent_id)) VERIFIABLE",
	})

	base, err := schemaSnapshotToDDL(liveSnapshot(t, client, "lab"), "lab")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	target := schemaDDLRequest{Domain: "lab"}
	for _, table := range base.Tables {
		if table.Name == "artifact_parents" {
			table.VerifiableGroups = []schemaDDLVerifiableGroup{{
				Columns:           []string{"child_id"},
				References:        "artifacts",
				ReferencedColumns: []string{"id"},
			}}
		}
		target.Tables = append(target.Tables, table)
	}

	diff, err := BuildSchemaDiff(base, target)
	if err != nil {
		t.Fatalf("BuildSchemaDiff: %v", err)
	}
	statement := diffOperationOfType(t, diff, "add_verifiable_group").Statement
	applyStatements(t, client, "lab", []string{statement})

	for _, table := range liveSnapshot(t, client, "lab").Domains[0].Tables {
		if table.Name != "artifact_parents" {
			continue
		}
		if len(table.VerifiableGroups) != 1 {
			t.Fatalf("after %q the engine reports %d groups, want one", statement, len(table.VerifiableGroups))
		}
	}
}

// TestEngineFixtureRoundTripKeepsTableAttributes covers the Fixtures panel
// against the features of the last releases: asql v0.7.0 exports composite-key
// tables and keeps APPEND ONLY, VERIFIABLE and the verifiable groups, so a
// fixture taken from one engine has to rebuild the same schema on another,
// down to the NOT NULL outside the primary key and without redeclaring the
// engine-managed _lsn column (both fixed in asql v0.8.0).
func TestEngineFixtureRoundTripKeepsTableAttributes(t *testing.T) {
	ctx := context.Background()
	source := newSchemaEngineClient(t)
	requireVerifiableGroups(t, source)

	applyStatements(t, source, "lab", []string{
		"CREATE TABLE artifacts (id TEXT PRIMARY KEY, name TEXT NOT NULL) VERIFIABLE",
		"CREATE TABLE artifact_parents (child_id TEXT NOT NULL, parent_id TEXT NOT NULL, PRIMARY KEY (child_id, parent_id)) VERIFIABLE",
		"ALTER TABLE artifact_parents ADD VERIFIABLE GROUP (child_id) REFERENCES artifacts (id)",
		"CREATE TABLE ledger (id INT PRIMARY KEY, amount INT) APPEND ONLY",
		"INSERT INTO artifacts (id, name) VALUES ('a1', 'one')",
		"INSERT INTO artifacts (id, name) VALUES ('a2', 'two')",
		"INSERT INTO artifact_parents (child_id, parent_id) VALUES ('a2', 'a1')",
		"INSERT INTO ledger (id, amount) VALUES (1, 100)",
	})

	fixture, err := source.ExportFixture(ctx, fixtures.ExportOptions{Domains: []string{"lab"}, Name: "lab"})
	if err != nil {
		t.Fatalf("ExportFixture: %v", err)
	}

	replica := newSchemaEngineClient(t)
	if err := replica.ApplyFixture(ctx, fixture); err != nil {
		t.Fatalf("ApplyFixture: %v", err)
	}

	rebuilt := map[string]api.SchemaSnapshotTable{}
	for _, table := range liveSnapshot(t, replica, "lab").Domains[0].Tables {
		rebuilt[table.Name] = table
	}

	// What the fixture has to carry, and did not before v0.7.0.
	parents, ok := rebuilt["artifact_parents"]
	if !ok {
		t.Fatal("the fixture did not rebuild artifact_parents")
	}
	if got, want := snapshotPrimaryKeyColumns(parents), []string{"child_id", "parent_id"}; !sameColumnList(got, want) {
		t.Fatalf("rebuilt composite key = %v, want %v", got, want)
	}
	if !parents.Verifiable {
		t.Fatal("the rebuilt artifact_parents is not VERIFIABLE")
	}
	if len(parents.VerifiableGroups) != 1 || parents.VerifiableGroups[0].References != "artifacts" {
		t.Fatalf("the rebuilt verifiable group is %+v, want one referencing artifacts", parents.VerifiableGroups)
	}
	if ledger := rebuilt["ledger"]; !ledger.AppendOnly {
		t.Fatal("the rebuilt ledger is not APPEND ONLY")
	}

	// The rows have to come over too.
	rows, err := replica.queryWithDomains(ctx, []string{"lab"}, "SELECT child_id, parent_id FROM artifact_parents")
	if err != nil {
		t.Fatalf("read the replica: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the replica holds %d edges, want 1", len(rows))
	}

	// The engine manages _lsn: a fixture that redeclared it left the rebuilt
	// table with two.
	for name, table := range rebuilt {
		lsnColumns := 0
		for _, column := range table.Columns {
			if column.Name == "_lsn" {
				lsnColumns++
			}
		}
		if lsnColumns != 1 {
			t.Fatalf("%s was rebuilt with %d _lsn columns, want exactly the one the engine adds", name, lsnColumns)
		}
	}

	// NOT NULL outside the primary key is carried by the column flag alone.
	for _, column := range rebuilt["artifacts"].Columns {
		if column.Name == "name" && !column.NotNull {
			t.Fatal("the fixture dropped NOT NULL from artifacts.name, which is not part of the key")
		}
	}

	want, err := json.Marshal(liveSnapshot(t, source, "lab").Domains)
	if err != nil {
		t.Fatalf("marshal source: %v", err)
	}
	got, err := json.Marshal(liveSnapshot(t, replica, "lab").Domains)
	if err != nil {
		t.Fatalf("marshal replica: %v", err)
	}
	if string(want) != string(got) {
		t.Fatalf("the fixture rebuilt a different schema\nsource:  %s\nreplica: %s", want, got)
	}
}
