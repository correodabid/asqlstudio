package studioapp

import (
	"strings"
	"testing"

	api "github.com/correodabid/asql/pkg/adminapi"
)

// TestSchemaDDLIndexKeepsTheMethodItWasGiven pins that the builder never
// invents an index method. It used to write USING btree for an index whose
// method it did not know, which is the opposite of what the engine does: a
// CREATE INDEX with no USING makes a hash index (asql v0.9.0, #112). A
// baseline built from a snapshot that left the method out therefore rewrote a
// hash index as a btree one.
func TestSchemaDDLIndexKeepsTheMethodItWasGiven(t *testing.T) {
	script, err := BuildSchemaDDLScript(schemaDDLRequest{
		Domain: "shop",
		Tables: []schemaDDLTable{{
			Name: "items",
			Columns: []schemaDDLColumn{
				{Name: "id", Type: "INT", PrimaryKey: true},
				{Name: "a", Type: "INT"},
				{Name: "b", Type: "INT"},
				{Name: "c", Type: "INT"},
			},
			Indexes: []schemaDDLIndex{
				{Name: "idx_unstated", Columns: []string{"a"}},
				{Name: "idx_hash", Columns: []string{"b"}, Method: "hash"},
				{Name: "idx_btree", Columns: []string{"c"}, Method: "btree"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("BuildSchemaDDLScript: %v", err)
	}

	for _, want := range []string{
		"CREATE INDEX idx_unstated ON items (a);",
		"CREATE INDEX idx_hash ON items (b) USING hash;",
		"CREATE INDEX idx_btree ON items (c) USING btree;",
	} {
		if !strings.Contains(script.DDL, want) {
			t.Errorf("the DDL does not contain %q\n%s", want, script.DDL)
		}
	}
	if strings.Contains(script.DDL, "idx_unstated ON items (a) USING") {
		t.Errorf("the builder asserted a method it was not given\n%s", script.DDL)
	}
}

func TestSchemaDDLIndexRejectsAnUnknownMethod(t *testing.T) {
	_, err := BuildSchemaDDLScript(schemaDDLRequest{
		Domain: "shop",
		Tables: []schemaDDLTable{{
			Name:    "items",
			Columns: []schemaDDLColumn{{Name: "id", Type: "INT", PrimaryKey: true}},
			Indexes: []schemaDDLIndex{{Name: "idx_bad", Columns: []string{"id"}, Method: "gist"}},
		}},
	})
	if err == nil {
		t.Fatal("an index method of \"gist\" was accepted")
	}
	if !strings.Contains(err.Error(), "gist") {
		t.Errorf("the error does not name the method: %v", err)
	}
}

// TestEngineIndexMethodSurvivesBaseline checks the premise of the fix against a
// real engine -- a CREATE INDEX with no USING is a hash index -- and that a
// baseline round trip preserves each index's method. It also pins that the
// indexes backing a primary key or a UNIQUE column stay out of the baseline:
// the engine names them "__auto_<table>_<column>" and "__pk_<table>", and a
// baseline that emitted them would declare an index the key already implies.
func TestEngineIndexMethodSurvivesBaseline(t *testing.T) {
	source := newSchemaEngineClient(t)
	applyStatements(t, source, "shop", []string{
		"CREATE TABLE lines (order_id INT NOT NULL, line_no INT NOT NULL, qty INT, note TEXT, PRIMARY KEY (order_id, line_no))",
		"CREATE TABLE items (id INT PRIMARY KEY, sku TEXT UNIQUE, note TEXT)",
		// No USING: the engine makes a hash index.
		"CREATE INDEX idx_lines_note ON lines (note)",
		"CREATE INDEX idx_lines_qty ON lines (qty) USING btree",
	})

	original := liveSnapshot(t, source, "shop")
	methods := indexMethodsByName(t, original)
	if got := methods["idx_lines_note"]; got != "hash" {
		t.Fatalf("an index created without USING is %q, want hash: the engine's default changed and the baseline's assumption with it", got)
	}
	if got := methods["idx_lines_qty"]; got != "btree" {
		t.Fatalf("idx_lines_qty is %q, want btree", got)
	}

	baseline, err := schemaSnapshotToDDL(original, "shop")
	if err != nil {
		t.Fatalf("schemaSnapshotToDDL: %v", err)
	}
	script, err := BuildSchemaDDLScript(baseline)
	if err != nil {
		t.Fatalf("BuildSchemaDDLScript: %v", err)
	}
	for _, internal := range []string{"__pk_lines", "__auto_items_id", "__auto_items_sku"} {
		if strings.Contains(script.DDL, internal) {
			t.Errorf("the baseline declares the engine's own index %q\n%s", internal, script.DDL)
		}
	}

	replica := newSchemaEngineClient(t)
	applyStatements(t, replica, "shop", script.Statements)
	rebuilt := indexMethodsByName(t, liveSnapshot(t, replica, "shop"))

	for name, want := range methods {
		if got, ok := rebuilt[name]; !ok {
			t.Errorf("the baseline lost index %q", name)
		} else if got != want {
			t.Errorf("index %q is %q after the round trip, was %q", name, got, want)
		}
	}
	for name := range rebuilt {
		if _, ok := methods[name]; !ok {
			t.Errorf("the baseline added index %q, which the source did not have", name)
		}
	}
}

func indexMethodsByName(t *testing.T, snapshot *api.SchemaSnapshotResponse) map[string]string {
	t.Helper()
	methods := map[string]string{}
	for _, domain := range snapshot.Domains {
		for _, table := range domain.Tables {
			for _, index := range table.Indexes {
				methods[index.Name] = index.Method
			}
		}
	}
	return methods
}
