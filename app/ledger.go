package studioapp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	api "github.com/correodabid/asql/pkg/adminapi"
)

// The ledger surface: the node's tamper-evident WAL ledger (ADR 0005), the
// proofs built on it (ADR 0008) and signed query results (ADR 0006).
//
// A node started without `-ledger-key` keeps a hash chain but signs nothing, so
// digests have no signature and the proof tables refuse to answer. The studio
// reports that as a mode rather than an error: the panel tells the operator what
// the node can and cannot prove.

const (
	ledgerModeOff    = "off"
	ledgerModeChain  = "chain"
	ledgerModeSigned = "signed"
)

type ledgerStatusResponse struct {
	// Mode is "signed" when the node signs digests, "chain" when it keeps the
	// hash chain without signing, and "off" when it keeps no digests at all.
	Mode string `json:"mode"`
	// Signed, Provable and RowsProvable say which panels can do anything: a
	// consistency proof needs a signed digest, a row proof needs a version 3
	// digest, which in turn needs a state key.
	Signed       bool              `json:"signed"`
	Provable     bool              `json:"provable"`
	RowsProvable bool              `json:"rows_provable"`
	Digest       *api.LedgerDigest `json:"digest,omitempty"`
	// Explanation is one sentence for the panel to show when the node cannot
	// prove what the operator asked for.
	Explanation string `json:"explanation,omitempty"`
}

// LedgerStatus reports what the connected node's ledger can prove, with its
// latest digest.
func (a *App) LedgerStatus() (map[string]interface{}, error) {
	ctx, cancel := a.reqCtx()
	defer cancel()

	row, err := a.getLeaderClient().LedgerDigest(ctx, 0)
	if err != nil {
		return nil, err
	}
	return structToMap(ledgerStatusFromDigest(row))
}

// LedgerDigest returns the digest covering lsn, or the latest one when lsn is 0.
func (a *App) LedgerDigest(lsn uint64) (map[string]interface{}, error) {
	ctx, cancel := a.reqCtx()
	defer cancel()

	row, err := a.getLeaderClient().LedgerDigest(ctx, lsn)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("the node has no ledger digest covering LSN %d", lsn)
	}
	digest := ledgerDigestFromRow(row)
	if digest == nil {
		return nil, fmt.Errorf("the node has no ledger digest covering LSN %d", lsn)
	}
	return structToMap(digest)
}

type ledgerConsistencyProofResponse struct {
	FromLSN uint64 `json:"from_lsn"`
	ToLSN   uint64 `json:"to_lsn"`
	// FromDigest, ToDigest and Proof are the proof's own JSON, passed through
	// without being rebuilt, for `asqlctl verify-consistency` to check.
	FromDigest json.RawMessage `json:"from_digest"`
	ToDigest   json.RawMessage `json:"to_digest"`
	Proof      json.RawMessage `json:"proof"`
}

// LedgerProveConsistency proves that the log signed at toLSN extends the log
// signed at fromLSN. toLSN 0 means the latest signed digest.
func (a *App) LedgerProveConsistency(fromLSN, toLSN uint64) (map[string]interface{}, error) {
	if fromLSN == 0 {
		return nil, fmt.Errorf("from_lsn is required: it must be the LSN of a signed digest")
	}
	if toLSN != 0 && toLSN < fromLSN {
		return nil, fmt.Errorf("to_lsn %d is before from_lsn %d", toLSN, fromLSN)
	}

	ctx, cancel := a.reqCtx()
	defer cancel()

	row, err := a.getLeaderClient().ProveConsistency(ctx, fromLSN, toLSN)
	if err != nil {
		return nil, err
	}
	return structToMap(ledgerConsistencyProofResponse{
		FromLSN:    fromLSN,
		ToLSN:      toLSN,
		FromDigest: rawJSONField(row, "from_digest"),
		ToDigest:   rawJSONField(row, "to_digest"),
		Proof:      rawJSONField(row, "proof"),
	})
}

// ledgerRowKey asks for either one row, by its primary key values in key
// order, or one whole verifiable group, by the group's key values. A group
// entry adds every row of that group and the group leaf itself, which is what
// proves a complete set — and proves an empty one for a row that has none.
type ledgerRowKey struct {
	Domain string   `json:"domain"`
	Table  string   `json:"table"`
	PK     []string `json:"pk,omitempty"`
	Group  []string `json:"group,omitempty"`
	// GroupColumns picks the group when the table declares more than one.
	GroupColumns []string `json:"group_columns,omitempty"`
}

type ledgerRowProofRequest struct {
	Keys []ledgerRowKey `json:"keys"`
	LSN  uint64         `json:"lsn,omitempty"`
}

type ledgerRowProofResponse struct {
	LSN uint64 `json:"lsn,omitempty"`
	// Digest, Rows, Proof and Groups are the proof's JSON as the node returned
	// it, for `asqlctl verify-rows` to check. Groups is an empty list when no
	// group was asked for.
	Digest json.RawMessage `json:"digest"`
	Rows   json.RawMessage `json:"rows"`
	Proof  json.RawMessage `json:"proof"`
	Groups json.RawMessage `json:"groups"`
}

// LedgerProveRows proves that the rows cited by their primary keys were in the
// state signed at lsn (the latest signed digest when 0), and that each group
// asked for is shown complete. It proves membership and completeness within a
// group; it cannot show that an arbitrary row is absent.
func (a *App) LedgerProveRows(req ledgerRowProofRequest) (map[string]interface{}, error) {
	keys, err := encodeLedgerRowKeys(req.Keys)
	if err != nil {
		return nil, err
	}

	ctx, cancel := a.reqCtx()
	defer cancel()

	row, err := a.getLeaderClient().ProveRows(ctx, keys, req.LSN)
	if err != nil {
		return nil, err
	}
	return structToMap(ledgerRowProofResponse{
		LSN:    req.LSN,
		Digest: rawJSONField(row, "digest"),
		Rows:   rawJSONField(row, "rows"),
		Proof:  rawJSONField(row, "proof"),
		Groups: rawJSONField(row, "groups"),
	})
}

type attestQueryRequest struct {
	SQL     string   `json:"sql"`
	Domains []string `json:"domains,omitempty"`
	LSN     uint64   `json:"lsn,omitempty"`
}

// attestQueryResponse is adminapi.AttestedResult: the shape
// `asqlctl attest-query` writes and `asqlctl verify-result` reads, so the file
// the panel saves is one a verifier accepts. Rows stays raw instead of
// [][]json.RawMessage only to pass the node's cells through untouched; it
// encodes to the same JSON.
type attestQueryResponse struct {
	Attestation api.ResultAttestation `json:"attestation"`
	Rows        json.RawMessage       `json:"rows"`
}

// AttestQuery runs a SELECT at a committed LSN and returns its rows with a
// signed attestation that binds them, the SQL and the ledger chain at that LSN.
func (a *App) AttestQuery(req attestQueryRequest) (map[string]interface{}, error) {
	sql := strings.TrimSpace(req.SQL)
	if sql == "" {
		return nil, fmt.Errorf("sql is required")
	}
	if !strings.HasPrefix(strings.ToUpper(sql), "SELECT") {
		return nil, fmt.Errorf("only a SELECT can be attested")
	}

	ctx, cancel := a.reqCtx()
	defer cancel()

	row, err := a.getLeaderClient().AttestQuery(ctx, sql, req.Domains, req.LSN)
	if err != nil {
		return nil, err
	}

	// Every column arrives as text, and domains and columns arrive as JSON
	// array literals. Copying the row as it came left the attestation with
	// "lsn": "9" and "columns": "[\"id\"]" -- strings where a verifier reads a
	// number and a list -- so the saved file did not decode as an attestation
	// at all.
	return structToMap(attestQueryResponse{
		Attestation: api.ResultAttestation{
			Version:       uint8(toUint64(row["version"])),
			LSN:           toUint64(row["lsn"]),
			Chain:         toString(row["chain"]),
			Term:          toUint64(row["term"]),
			Timestamp:     toUint64(row["timestamp"]),
			Domains:       jsonStringList(row["domains"]),
			SQL:           toString(row["sql"]),
			ResultHash:    toString(row["result_hash"]),
			RowCount:      toUint64(row["row_count"]),
			Columns:       jsonStringList(row["columns"]),
			EngineVersion: toString(row["engine_version"]),
			NodeID:        toString(row["node_id"]),
			KeyID:         toString(row["key_id"]),
			SignedAt:      parseLedgerTimestamp(toString(row["signed_at"])),
			Sig:           toString(row["sig"]),
		},
		Rows: rawJSONField(row, "rows"),
	})
}

func encodeLedgerRowKeys(keys []ledgerRowKey) (string, error) {
	if len(keys) == 0 {
		return "", fmt.Errorf("at least one row key is required")
	}
	// The node reads each key value as a JSON scalar, so a numeric key has to go
	// over as a number and anything else as a string.
	encoded := make([]map[string]interface{}, 0, len(keys))
	for _, key := range keys {
		table := strings.TrimSpace(key.Table)
		if table == "" {
			return "", fmt.Errorf("every row key needs a table")
		}
		if len(key.PK) > 0 && len(key.Group) > 0 {
			return "", fmt.Errorf("key for %s asks for both a row and a group: use one entry for each", table)
		}
		entry := map[string]interface{}{"table": table}
		switch {
		case len(key.Group) > 0:
			entry["group"] = jsonScalars(key.Group)
			if columns := trimmedNonEmpty(key.GroupColumns); len(columns) > 0 {
				entry["group_columns"] = columns
			}
		case len(key.PK) > 0:
			entry["pk"] = jsonScalars(key.PK)
		default:
			return "", fmt.Errorf("key for %s needs either its primary key values or a group", table)
		}
		if domain := strings.TrimSpace(key.Domain); domain != "" {
			entry["domain"] = domain
		}
		encoded = append(encoded, entry)
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return "", fmt.Errorf("encode row keys: %w", err)
	}
	return string(raw), nil
}

func jsonScalars(values []string) []interface{} {
	out := make([]interface{}, 0, len(values))
	for _, value := range values {
		out = append(out, jsonScalar(value))
	}
	return out
}

func trimmedNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// jsonScalar keeps a primary key value's type: the state tree hashes the JSON
// scalar, so 7 and "7" are different keys.
func jsonScalar(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}
	if number, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return number
	}
	if trimmed == "true" || trimmed == "false" {
		return trimmed == "true"
	}
	return value
}

func ledgerStatusFromDigest(row map[string]interface{}) ledgerStatusResponse {
	digest := ledgerDigestFromRow(row)
	if digest == nil {
		return ledgerStatusResponse{
			Mode:        ledgerModeOff,
			Explanation: "the node keeps no ledger digests: start it with -ledger-key to sign them, or with ledger mode chain for a hash chain without signatures",
		}
	}

	status := ledgerStatusResponse{Digest: digest}
	switch {
	case strings.TrimSpace(digest.Sig) == "":
		status.Mode = ledgerModeChain
		status.Explanation = "the node keeps the hash chain but does not sign it: consistency and row proofs need ledger mode signed (-ledger-key)"
	case digest.Version >= 3:
		status.Mode = ledgerModeSigned
		status.Signed = true
		status.Provable = true
		status.RowsProvable = true
	case digest.Version == 2:
		status.Mode = ledgerModeSigned
		status.Signed = true
		status.Provable = true
		status.Explanation = "the node signs version 2 digests: row proofs need a state key, which makes it sign version 3"
	default:
		status.Mode = ledgerModeSigned
		status.Signed = true
		status.Explanation = "the node signs version 1 digests, which do not cover the log tree: consistency and row proofs need a node whose WAL starts at LSN 1"
	}
	return status
}

// ledgerDigestFromRow reads asql_admin.ledger_digest into the engine's own
// digest type. The view's columns are named exactly as adminapi.LedgerDigest's
// JSON fields, so what the panel shows and the operator saves is the digest
// asqlctl reads -- the studio's own copy of the type called the signature
// "signature", which no verifier looks for.
func ledgerDigestFromRow(row map[string]interface{}) *api.LedgerDigest {
	if len(row) == 0 {
		return nil
	}
	// A node without digests answers with the columns and no row; a row with no
	// chain value is the same thing reaching us through a different path.
	chain := toString(row["chain"])
	if strings.TrimSpace(chain) == "" {
		return nil
	}
	return &api.LedgerDigest{
		Version:   uint8(toUint64(row["version"])),
		LSN:       toUint64(row["lsn"]),
		Chain:     chain,
		TreeRoot:  toString(row["tree_root"]),
		StateRoot: toString(row["state_root"]),
		Term:      toUint64(row["term"]),
		Timestamp: toUint64(row["timestamp"]),
		NodeID:    toString(row["node_id"]),
		KeyID:     toString(row["key_id"]),
		SignedAt:  parseLedgerTimestamp(toString(row["signed_at"])),
		Sig:       toString(row["sig"]),
	}
}

// parseLedgerTimestamp reads the signing time the engine reports. A value it
// cannot read leaves the zero time rather than failing the whole digest: the
// panel still has the chain, the roots and the signature to show.
func parseLedgerTimestamp(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// jsonStringList reads a column holding a JSON array of strings, which is how
// the engine reports an attestation's domains and columns.
func jsonStringList(value interface{}) []string {
	raw := strings.TrimSpace(toString(value))
	if raw == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		// Not a JSON array: report the single value rather than nothing, so a
		// one-domain attestation from an engine that writes it plainly still
		// names its domain.
		return []string{raw}
	}
	return list
}

// rawJSONField takes a column that holds JSON and passes it through without
// rebuilding it, so no value is reinterpreted on the way to a verifier. The
// response is still re-encoded once as it crosses to the frontend, which is
// why proofs carry their values as typed cells -- ["i","42"] rather than 42 --
// and survive it exactly. A column that is not valid JSON goes over as a JSON
// string rather than breaking the response.
func rawJSONField(row map[string]interface{}, name string) json.RawMessage {
	value := strings.TrimSpace(toString(row[name]))
	if value == "" {
		return json.RawMessage("null")
	}
	if json.Valid([]byte(value)) {
		return json.RawMessage(value)
	}
	quoted, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(quoted)
}
