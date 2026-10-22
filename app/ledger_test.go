package studioapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLedgerStatusFromDigestReportsModes(t *testing.T) {
	tests := []struct {
		name         string
		row          map[string]interface{}
		mode         string
		signed       bool
		provable     bool
		rowsProvable bool
	}{
		{
			name: "no digest",
			row:  nil,
			mode: ledgerModeOff,
		},
		{
			name: "columns but no row",
			row:  map[string]interface{}{"chain": nil, "lsn": nil},
			mode: ledgerModeOff,
		},
		{
			name: "chain without signature",
			row:  map[string]interface{}{"version": "1", "lsn": "12", "chain": "ab12", "sig": ""},
			mode: ledgerModeChain,
		},
		{
			name:     "signed version 1",
			row:      map[string]interface{}{"version": "1", "lsn": "12", "chain": "ab12", "sig": "c2ln"},
			mode:     ledgerModeSigned,
			signed:   true,
			provable: false,
		},
		{
			name:     "signed version 2 covers the log tree",
			row:      map[string]interface{}{"version": "2", "lsn": "12", "chain": "ab12", "sig": "c2ln", "tree_root": "ff01"},
			mode:     ledgerModeSigned,
			signed:   true,
			provable: true,
		},
		{
			name:         "signed version 3 covers the state tree",
			row:          map[string]interface{}{"version": "3", "lsn": "12", "chain": "ab12", "sig": "c2ln", "tree_root": "ff01", "state_root": "ee02"},
			mode:         ledgerModeSigned,
			signed:       true,
			provable:     true,
			rowsProvable: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := ledgerStatusFromDigest(test.row)
			if status.Mode != test.mode {
				t.Fatalf("mode = %q, want %q", status.Mode, test.mode)
			}
			if status.Signed != test.signed {
				t.Fatalf("signed = %v, want %v", status.Signed, test.signed)
			}
			if status.Provable != test.provable {
				t.Fatalf("provable = %v, want %v", status.Provable, test.provable)
			}
			if status.RowsProvable != test.rowsProvable {
				t.Fatalf("rows provable = %v, want %v", status.RowsProvable, test.rowsProvable)
			}
			// Anything the node cannot prove has to come with a reason the panel
			// can show.
			if !status.RowsProvable && strings.TrimSpace(status.Explanation) == "" {
				t.Fatal("a node that cannot prove rows needs an explanation")
			}
		})
	}
}

func TestEncodeLedgerRowKeysKeepsValueTypes(t *testing.T) {
	encoded, err := encodeLedgerRowKeys([]ledgerRowKey{
		{Domain: "shop", Table: "lines", PK: []string{"7", "2"}},
		{Table: "audited", PK: []string{"abc"}},
	})
	if err != nil {
		t.Fatalf("encodeLedgerRowKeys: %v", err)
	}

	var decoded []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("the encoded keys are not JSON: %v (%s)", err, encoded)
	}
	if len(decoded) != 2 {
		t.Fatalf("encoded %d keys, want 2", len(decoded))
	}
	// The state tree hashes the JSON scalar, so a numeric key must not become a
	// string: 7 and "7" are different rows.
	if got := string(decoded[0]["pk"]); got != "[7,2]" {
		t.Fatalf("numeric key encoded as %s, want [7,2]", got)
	}
	if got := string(decoded[1]["pk"]); got != `["abc"]` {
		t.Fatalf("text key encoded as %s, want [\"abc\"]", got)
	}
	if _, ok := decoded[1]["domain"]; ok {
		t.Fatal("a key without a domain should not send an empty one")
	}
}

func TestEncodeLedgerRowKeysRejectsIncompleteKeys(t *testing.T) {
	for _, test := range []struct {
		name string
		keys []ledgerRowKey
	}{
		{name: "no keys", keys: nil},
		{name: "no table", keys: []ledgerRowKey{{PK: []string{"1"}}}},
		{name: "no values", keys: []ledgerRowKey{{Table: "lines"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := encodeLedgerRowKeys(test.keys); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestRawJSONFieldPassesProofBytesThrough(t *testing.T) {
	row := map[string]interface{}{
		"proof":  `["ab","cd"]`,
		"digest": `{"lsn":7}`,
		"broken": "not json",
		"empty":  "",
	}
	if got := string(rawJSONField(row, "proof")); got != `["ab","cd"]` {
		t.Fatalf("proof = %s, want the bytes unchanged", got)
	}
	if got := string(rawJSONField(row, "digest")); got != `{"lsn":7}` {
		t.Fatalf("digest = %s, want the bytes unchanged", got)
	}
	if got := string(rawJSONField(row, "broken")); got != `"not json"` {
		t.Fatalf("a non-JSON column should go over as a JSON string, got %s", got)
	}
	if got := string(rawJSONField(row, "empty")); got != "null" {
		t.Fatalf("empty = %s, want null", got)
	}
	if got := string(rawJSONField(row, "absent")); got != "null" {
		t.Fatalf("absent = %s, want null", got)
	}
}

func TestLedgerProveConsistencyRequiresFromLSN(t *testing.T) {
	app := &App{}
	if _, err := app.LedgerProveConsistency(0, 10); err == nil {
		t.Fatal("expected an error without from_lsn")
	}
	if _, err := app.LedgerProveConsistency(10, 5); err == nil {
		t.Fatal("expected an error when to_lsn precedes from_lsn")
	}
}

func TestAttestQueryRejectsNonSelect(t *testing.T) {
	app := &App{}
	for _, sql := range []string{"", "   ", "DELETE FROM users", "UPDATE users SET id = 1"} {
		if _, err := app.AttestQuery(attestQueryRequest{SQL: sql}); err == nil {
			t.Fatalf("expected an error for %q", sql)
		}
	}
}

func TestEncodeLedgerRowKeysAsksForGroups(t *testing.T) {
	encoded, err := encodeLedgerRowKeys([]ledgerRowKey{
		{Domain: "lab", Table: "artifacts", PK: []string{"0192abc"}},
		{Domain: "lab", Table: "artifact_parents", Group: []string{"0192abc"}},
		{Table: "artifact_parents", Group: []string{"0192def"}, GroupColumns: []string{"child_id", " "}},
	})
	if err != nil {
		t.Fatalf("encodeLedgerRowKeys: %v", err)
	}

	var decoded []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("the encoded keys are not JSON: %v (%s)", err, encoded)
	}
	if len(decoded) != 3 {
		t.Fatalf("encoded %d entries, want 3", len(decoded))
	}
	if _, ok := decoded[0]["group"]; ok {
		t.Fatal("a row entry should not carry a group")
	}
	if got := string(decoded[1]["group"]); got != `["0192abc"]` {
		t.Fatalf("group = %s, want [\"0192abc\"]", got)
	}
	if _, ok := decoded[1]["pk"]; ok {
		t.Fatal("a group entry should not carry a pk")
	}
	if _, ok := decoded[1]["group_columns"]; ok {
		t.Fatal("group_columns is only needed when a table has several groups")
	}
	if got := string(decoded[2]["group_columns"]); got != `["child_id"]` {
		t.Fatalf("group_columns = %s, want [\"child_id\"]", got)
	}
}

func TestEncodeLedgerRowKeysRejectsRowAndGroupInOneEntry(t *testing.T) {
	_, err := encodeLedgerRowKeys([]ledgerRowKey{
		{Table: "artifact_parents", PK: []string{"1"}, Group: []string{"2"}},
	})
	if err == nil {
		t.Fatal("expected an error for an entry asking for both a row and a group")
	}
	if !strings.Contains(err.Error(), "one entry for each") {
		t.Fatalf("unexpected error %v", err)
	}
}
