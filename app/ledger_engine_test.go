package studioapp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/correodabid/asql/pkg/adminapi"
	"github.com/correodabid/asql/pkg/embed"
)

// The ledger panel's own paths could only be unit tested until now:
// servertest starts a node in the default "chain" mode, where the proof
// tables refuse to answer. These tests start a node that signs its digests
// and holds a state key, so prove_consistency, prove_rows and attest_query
// all work, and then check the one property the panel exists for: what the
// studio hands the operator is what asqlctl reads. asql v0.9.0 publishes
// those shapes as Go types, so the assertion is a decode into them.

// startSigningLedgerNode starts an engine in ledger mode "signed" with a
// state key, which is what a version 3 digest -- the one row proofs need --
// requires. It returns the pgwire address.
func startSigningLedgerNode(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "node.key")

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ledger key: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatalf("marshal ledger key: %v", err)
	}
	// The engine wants PEM PKCS #8 and refuses a file others can read.
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), 0o600); err != nil {
		t.Fatalf("write ledger key: %v", err)
	}

	var stateKey [32]byte
	if _, err := rand.Read(stateKey[:]); err != nil {
		t.Fatalf("generate state key: %v", err)
	}
	statePEM := pem.EncodeToMemory(&pem.Block{Type: "ASQL LEDGER STATE KEY", Bytes: stateKey[:]})
	if err := os.WriteFile(keyPath+".state", statePEM, 0o600); err != nil {
		t.Fatalf("write state key: %v", err)
	}

	srv, err := embed.Start(context.Background(), embed.Options{
		DataDir:       filepath.Join(dir, "data"),
		LedgerMode:    "signed",
		LedgerKeyFile: keyPath,
	})
	if err != nil {
		t.Skipf("this engine cannot start a signing ledger (%v); upgrade to asql v0.8.0 to run this test", err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close signing node: %v", err)
		}
	})
	return srv.Addr()
}

func newSigningLedgerApp(t *testing.T) *App {
	t.Helper()
	client := newEngineClient(startSigningLedgerNode(t), "")
	t.Cleanup(client.Close)
	return &App{engine: client}
}

// waitForSignedDigest waits for the node to sign the committed chain head.
// Signing is on a cadence -- at most every interval, sooner after a number of
// LSNs, 1s and 10,000 by default -- so a digest does not exist the instant a
// commit returns, and the proof tables answer "no digest signed yet" until it
// does.
func waitForSignedDigest(t *testing.T, app *App) api.LedgerDigest {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		raw, err := app.LedgerDigest(0)
		if err == nil {
			var digest api.LedgerDigest
			remarshal(t, raw, &digest)
			if digest.LSN > 0 {
				return digest
			}
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the node signed no digest within 15s: %v", lastErr)
	return api.LedgerDigest{}
}

// remarshal takes what a studio method returns over the Wails bridge and
// decodes it into target, which is how the operator's saved file reaches
// asqlctl.
func remarshal(t *testing.T, value interface{}, target interface{}) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal studio response: %v", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("the studio's response does not decode as %T: %v\n%s", target, err, raw)
	}
}

// waitForDigestAfter waits for a digest that covers more than afterLSN, which
// is how the test gets two signed digests to prove between.
func waitForDigestAfter(t *testing.T, app *App, afterLSN uint64) api.LedgerDigest {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := app.LedgerDigest(0)
		if err == nil {
			var digest api.LedgerDigest
			remarshal(t, raw, &digest)
			if digest.LSN > afterLSN {
				return digest
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no digest after LSN %d was signed within 15s", afterLSN)
	return api.LedgerDigest{}
}

func TestEngineLedgerStatusReportsSignedMode(t *testing.T) {
	app := newSigningLedgerApp(t)
	applyStatements(t, app.engine, "shop", []string{"CREATE TABLE items (id INT PRIMARY KEY) VERIFIABLE"})

	waitForSignedDigest(t, app)

	raw, err := app.LedgerStatus()
	if err != nil {
		t.Fatalf("LedgerStatus: %v", err)
	}
	var status ledgerStatusResponse
	remarshal(t, raw, &status)

	if status.Mode != ledgerModeSigned {
		t.Fatalf("mode is %q, want %q (explanation: %q)", status.Mode, ledgerModeSigned, status.Explanation)
	}
	if !status.Signed || !status.Provable || !status.RowsProvable {
		t.Errorf("a node with a signing key and a state key reports signed=%v provable=%v rows_provable=%v, want all true: %q",
			status.Signed, status.Provable, status.RowsProvable, status.Explanation)
	}
	if status.Digest == nil {
		t.Fatal("a signing node reported no digest")
	}
}

// TestEngineLedgerDigestIsTheAdminapiDigest checks the digest the studio shows
// and saves against the type asqlctl reads, field by field.
func TestEngineLedgerDigestIsTheAdminapiDigest(t *testing.T) {
	app := newSigningLedgerApp(t)
	applyStatements(t, app.engine, "shop", []string{"CREATE TABLE items (id INT PRIMARY KEY) VERIFIABLE"})

	digest := waitForSignedDigest(t, app)

	if digest.Version < 3 {
		t.Errorf("digest version is %d, want 3 or more: a state key should make the node sign the state root", digest.Version)
	}
	if digest.LSN == 0 {
		t.Error("the digest covers LSN 0")
	}
	for name, value := range map[string]string{
		"chain":      digest.Chain,
		"tree_root":  digest.TreeRoot,
		"state_root": digest.StateRoot,
		"key_id":     digest.KeyID,
		"sig":        digest.Sig,
	} {
		if value == "" {
			t.Errorf("the digest carries no %s", name)
		}
	}
	if digest.SignedAt.IsZero() {
		t.Error("the digest carries no signed_at that decodes as a time")
	}
}

func TestEngineProveConsistencyIsTheAdminapiProof(t *testing.T) {
	app := newSigningLedgerApp(t)
	applyStatements(t, app.engine, "shop", []string{"CREATE TABLE items (id INT PRIMARY KEY) VERIFIABLE"})
	applyStatements(t, app.engine, "shop", []string{"INSERT INTO items (id) VALUES (1)"})

	// A consistency proof runs between two signed digests, so the test needs a
	// second one: asking from LSN 1 fails with "no digest was signed at LSN 1".
	first := waitForSignedDigest(t, app)
	applyStatements(t, app.engine, "shop", []string{"INSERT INTO items (id) VALUES (2)"})
	second := waitForDigestAfter(t, app, first.LSN)

	raw, err := app.LedgerProveConsistency(first.LSN, second.LSN)
	if err != nil {
		t.Fatalf("LedgerProveConsistency(%d, %d): %v", first.LSN, second.LSN, err)
	}

	// The studio wraps the proof's three JSON columns with the LSNs asked
	// for; the columns themselves are the bytes asqlctl verifies, so they
	// have to rebuild the official type exactly.
	var wrapper struct {
		FromDigest json.RawMessage `json:"from_digest"`
		ToDigest   json.RawMessage `json:"to_digest"`
		Proof      json.RawMessage `json:"proof"`
	}
	remarshal(t, raw, &wrapper)

	rebuilt, err := json.Marshal(map[string]json.RawMessage{
		"from_digest": wrapper.FromDigest,
		"to_digest":   wrapper.ToDigest,
		"proof":       wrapper.Proof,
	})
	if err != nil {
		t.Fatalf("rebuild proof: %v", err)
	}
	var proof api.ConsistencyProof
	if err := json.Unmarshal(rebuilt, &proof); err != nil {
		t.Fatalf("the proof does not decode as adminapi.ConsistencyProof: %v\n%s", err, rebuilt)
	}
	if proof.To.LSN != second.LSN {
		t.Errorf("the proof's to_digest covers LSN %d, want %d", proof.To.LSN, second.LSN)
	}
	if proof.From.LSN != first.LSN {
		t.Errorf("the proof's from_digest covers LSN %d, want %d", proof.From.LSN, first.LSN)
	}
	if proof.From.Chain == "" || proof.To.Chain == "" {
		t.Error("the proof's digests carry no chain value")
	}
}

func TestEngineProveRowsIsTheAdminapiProof(t *testing.T) {
	app := newSigningLedgerApp(t)
	applyStatements(t, app.engine, "shop", []string{
		"CREATE TABLE orders (id TEXT PRIMARY KEY) VERIFIABLE",
		"CREATE TABLE lines (order_id TEXT NOT NULL, sku TEXT NOT NULL, PRIMARY KEY (order_id, sku)) VERIFIABLE",
		"ALTER TABLE lines ADD VERIFIABLE GROUP (order_id) REFERENCES orders (id)",
	})
	applyStatements(t, app.engine, "shop", []string{
		"INSERT INTO orders (id) VALUES ('o1')",
		"INSERT INTO lines (order_id, sku) VALUES ('o1', 'a')",
		"INSERT INTO lines (order_id, sku) VALUES ('o1', 'b')",
	})

	waitForSignedDigest(t, app)

	raw, err := app.LedgerProveRows(ledgerRowProofRequest{Keys: []ledgerRowKey{
		{Domain: "shop", Table: "lines", Group: []string{"o1"}, GroupColumns: []string{"order_id"}},
	}})
	if err != nil {
		t.Fatalf("LedgerProveRows: %v", err)
	}

	var wrapper struct {
		Digest json.RawMessage `json:"digest"`
		Rows   json.RawMessage `json:"rows"`
		Groups json.RawMessage `json:"groups"`
		Proof  json.RawMessage `json:"proof"`
	}
	remarshal(t, raw, &wrapper)

	rebuilt, err := json.Marshal(map[string]json.RawMessage{
		"digest": wrapper.Digest,
		"rows":   wrapper.Rows,
		"groups": wrapper.Groups,
		"proof":  wrapper.Proof,
	})
	if err != nil {
		t.Fatalf("rebuild proof: %v", err)
	}
	var proof api.RowsProof
	if err := json.Unmarshal(rebuilt, &proof); err != nil {
		t.Fatalf("the proof does not decode as adminapi.RowsProof: %v\n%s", err, rebuilt)
	}

	if len(proof.Rows) != 2 {
		t.Fatalf("the proof cites %d rows, want the group's 2", len(proof.Rows))
	}
	if len(proof.Groups) != 1 {
		t.Fatalf("the proof shows %d groups complete, want 1", len(proof.Groups))
	}
	if got := proof.Groups[0].Columns; len(got) != 1 || got[0] != "order_id" {
		t.Errorf("the group's columns are %v, want [order_id]", got)
	}
	for _, row := range proof.Rows {
		if row.Table != "lines" {
			t.Errorf("a proven row is from %q, want lines", row.Table)
		}
		if len(row.PrimaryKeyColumns) != 2 {
			t.Errorf("a proven row of a composite key lists %d key columns, want 2", len(row.PrimaryKeyColumns))
		}
		// adminapi documents ProvenRow.Row as holding every column, "the
		// engine's _lsn included". v0.9.0 sends only the declared ones, so
		// this asserts what the engine actually proves; the discrepancy is
		// reported upstream rather than assumed here.
		for _, column := range []string{"order_id", "sku"} {
			if _, ok := row.Row[column]; !ok {
				t.Errorf("a proven row does not carry %q", column)
			}
		}
	}
	if len(proof.Proof.Depths) == 0 || len(proof.Proof.Siblings) == 0 {
		t.Error("the batch state proof is empty")
	}
}

// TestEngineAttestQueryIsTheAdminapiAttestedResult is the one that matters for
// the save button: asqlctl verify-result reads an AttestedResult, so the file
// the studio writes has to be one.
func TestEngineAttestQueryIsTheAdminapiAttestedResult(t *testing.T) {
	app := newSigningLedgerApp(t)
	applyStatements(t, app.engine, "shop", []string{"CREATE TABLE items (id INT PRIMARY KEY, sku TEXT)"})
	applyStatements(t, app.engine, "shop", []string{"INSERT INTO items (id, sku) VALUES (1, 'widget')"})

	waitForSignedDigest(t, app)

	raw, err := app.AttestQuery(attestQueryRequest{SQL: "SELECT id, sku FROM items", Domains: []string{"shop"}})
	if err != nil {
		t.Fatalf("AttestQuery: %v", err)
	}
	var attested api.AttestedResult
	remarshal(t, raw, &attested)

	if attested.Attestation.SQL == "" {
		t.Error("the attestation does not carry the SQL it signs")
	}
	if attested.Attestation.Sig == "" {
		t.Error("the attestation carries no signature")
	}
	if attested.Attestation.RowCount != 1 {
		t.Errorf("the attestation signs %d rows, want 1", attested.Attestation.RowCount)
	}
	if got := attested.Attestation.Columns; len(got) != 2 {
		t.Errorf("the attestation names %v as its columns, want 2", got)
	}
	if got := attested.Attestation.Domains; len(got) != 1 || got[0] != "shop" {
		t.Errorf("the attestation names %v as its domains, want [shop]", got)
	}
	if len(attested.Rows) != 1 {
		t.Fatalf("the attested result carries %d rows, want 1", len(attested.Rows))
	}
	if len(attested.Rows[0]) != 2 {
		t.Errorf("the attested row has %d cells, want 2", len(attested.Rows[0]))
	}
}
