package studioapp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

type ledgerDigestInfo struct {
	// Version is the digest version: 1 is a chain digest, 2 also signs the log
	// tree root, 3 also signs the state root of the VERIFIABLE tables.
	Version uint64 `json:"version"`
	LSN     uint64 `json:"lsn"`
	Chain   string `json:"chain"`
	// TreeRoot and StateRoot are empty when the digest's version does not
	// cover them.
	TreeRoot  string `json:"tree_root,omitempty"`
	StateRoot string `json:"state_root,omitempty"`
	Term      uint64 `json:"term"`
	Timestamp uint64 `json:"timestamp"`
	NodeID    string `json:"node_id,omitempty"`
	KeyID     string `json:"key_id,omitempty"`
	SignedAt  string `json:"signed_at,omitempty"`
	Signature string `json:"signature,omitempty"`
}

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
	Digest       *ledgerDigestInfo `json:"digest,omitempty"`
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
	// FromDigest, ToDigest and Proof are the proof's JSON as the node returned
	// it, so `asqlctl verify-consistency` can check the same bytes.
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

type attestQueryResponse struct {
	// Attestation is the signed envelope: the SQL, the LSN it ran at, the hash
	// of the rows and the chain value, with the node's signature over all of it.
	Attestation map[string]interface{} `json:"attestation"`
	// Rows are the rows the attestation binds, as the node returned them.
	Rows json.RawMessage `json:"rows"`
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

	attestation := make(map[string]interface{}, len(row))
	for key, value := range row {
		if key == "rows" {
			continue
		}
		attestation[key] = value
	}
	return structToMap(attestQueryResponse{
		Attestation: attestation,
		Rows:        rawJSONField(row, "rows"),
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
	case strings.TrimSpace(digest.Signature) == "":
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

func ledgerDigestFromRow(row map[string]interface{}) *ledgerDigestInfo {
	if len(row) == 0 {
		return nil
	}
	// A node without digests answers with the columns and no row; a row with no
	// chain value is the same thing reaching us through a different path.
	chain := toString(row["chain"])
	if strings.TrimSpace(chain) == "" {
		return nil
	}
	return &ledgerDigestInfo{
		Version:   toUint64(row["version"]),
		LSN:       toUint64(row["lsn"]),
		Chain:     chain,
		TreeRoot:  toString(row["tree_root"]),
		StateRoot: toString(row["state_root"]),
		Term:      toUint64(row["term"]),
		Timestamp: toUint64(row["timestamp"]),
		NodeID:    toString(row["node_id"]),
		KeyID:     toString(row["key_id"]),
		SignedAt:  toString(row["signed_at"]),
		Signature: toString(row["sig"]),
	}
}

// rawJSONField takes a column that holds JSON and passes it through unchanged,
// so a verifier checks the same bytes the node signed. A column that is not
// valid JSON goes over as a JSON string rather than breaking the response.
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
