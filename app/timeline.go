package studioapp

import (
	"fmt"
	"strings"
	"time"
)

// Commits carry their wall-clock time from ADR 0007 on: the time the COMMIT
// record holds, the same for every row of a transaction, and the one
// `AS OF TIMESTAMP` resolves against. The studio reports it next to the LSN so
// the timeline reads as dates rather than as log positions.

// commitTimeLayouts are the text forms the engine sends a timestamp column in.
// Microseconds are trimmed when they are zero.
var commitTimeLayouts = []string{
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
	time.RFC3339,
}

// timelineCommit mirrors adminapi.TimelineCommitEntry and adds the commit's
// wall-clock time, which the adminapi release the studio builds against does
// not carry, and the mutation count the timeline already reported.
type timelineCommit struct {
	LSN       uint64 `json:"lsn"`
	TxID      string `json:"tx_id"`
	Timestamp uint64 `json:"timestamp"`
	// CommitTime is the commit's wall-clock time as RFC 3339 UTC, empty for a
	// commit written before the engine recorded commit times.
	CommitTime string `json:"commit_time,omitempty"`
	// CommitTimeMicros is the same instant in Unix microseconds, 0 when unknown.
	CommitTimeMicros int64  `json:"commit_time_micros,omitempty"`
	MutationCount    uint64 `json:"mutation_count"`
}

type timelineCommitsResponse struct {
	Commits []timelineCommit `json:"commits"`
}

// timelineCommitsRequest mirrors adminapi.TimelineCommitsRequest, so the
// studio's own timeline types stay in one place.
type timelineCommitsRequest struct {
	FromLSN uint64 `json:"from_lsn,omitempty"`
	ToLSN   uint64 `json:"to_lsn,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Domain  string `json:"domain,omitempty"`
}

// parseCommitTime reads the engine's text form of a timestamp column. It
// returns the zero time for an empty or unparseable value: a commit from before
// ADR 0007 has no recorded time, which is not an error.
func parseCommitTime(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	for _, layout := range commitTimeLayouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

type timestampResolutionResponse struct {
	// Timestamp is the normalized instant that was asked for.
	Timestamp string `json:"timestamp"`
	// LSN is the newest commit at or before Timestamp, which is what
	// `AS OF TIMESTAMP` reads at.
	LSN        uint64 `json:"lsn"`
	TxID       string `json:"tx_id,omitempty"`
	CommitTime string `json:"commit_time,omitempty"`
	// Exact reports a commit whose time is the requested instant.
	Exact bool `json:"exact"`
}

// ResolveTimestamp finds the commit a wall-clock instant reads at: the newest
// commit at or before it. The time explorer uses it to turn a date the operator
// picked into the LSN the rest of the studio works with, and to say which
// commit that date lands on.
func (a *App) ResolveTimestamp(timestamp string) (map[string]interface{}, error) {
	instant, normalized, err := parseRequestedTimestamp(timestamp)
	if err != nil {
		return nil, err
	}

	ctx, cancel := a.reqCtx()
	defer cancel()

	// The timeline filters by LSN, not by time, so the resolution happens here
	// over the commits the node reports.
	commits, err := a.getLeaderClient().TimelineCommits(ctx, &timelineCommitsRequest{})
	if err != nil {
		return nil, err
	}

	var best *timelineCommit
	withTime := 0
	for i := range commits.Commits {
		commit := &commits.Commits[i]
		if commit.CommitTimeMicros == 0 {
			continue
		}
		withTime++
		if commit.CommitTimeMicros > instant.UnixMicro() {
			continue
		}
		if best == nil || commit.CommitTimeMicros > best.CommitTimeMicros || (commit.CommitTimeMicros == best.CommitTimeMicros && commit.LSN > best.LSN) {
			best = commit
		}
	}

	if withTime == 0 {
		return nil, fmt.Errorf("no commit on this node records a wall-clock time, so %s cannot be resolved: read by LSN instead", normalized)
	}
	if best == nil {
		return nil, fmt.Errorf("no commit was made at or before %s: the oldest retained commit is newer", normalized)
	}

	return structToMap(timestampResolutionResponse{
		Timestamp:  normalized,
		LSN:        best.LSN,
		TxID:       best.TxID,
		CommitTime: best.CommitTime,
		Exact:      best.CommitTimeMicros == instant.UnixMicro(),
	})
}

// requestedTimestampLayouts are the forms the studio accepts from the UI, from
// a full instant down to a bare date.
var requestedTimestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// parseRequestedTimestamp reads an instant the operator asked for and returns it
// with its RFC 3339 UTC form. A value without a zone is read as UTC, the zone
// the engine records commit times in.
func parseRequestedTimestamp(value string) (time.Time, string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, "", fmt.Errorf("timestamp is required")
	}
	for _, layout := range requestedTimestampLayouts {
		parsed, err := time.Parse(layout, trimmed)
		if err != nil {
			continue
		}
		if parsed.Location() == time.UTC || strings.Contains(layout, "Z07") {
			parsed = parsed.UTC()
		}
		return parsed, parsed.UTC().Format(time.RFC3339Nano), nil
	}
	return time.Time{}, "", fmt.Errorf("could not read %q as a timestamp: use an ISO-8601 instant such as 2026-10-22T05:44:25Z", trimmed)
}

// asOfTimestampClause is the clause the engine reads a whole query at. It
// trails the statement and may appear only once.
func asOfTimestampClause(instant time.Time) string {
	return fmt.Sprintf("AS OF TIMESTAMP '%s'", instant.UTC().Format(time.RFC3339Nano))
}

// applyAsOfTimestamp appends the AS OF TIMESTAMP clause to a SELECT. The engine
// applies one AS OF to the whole query — in joins, CTEs, subqueries and both
// sides of a set operation — and fails a query that carries more than one, so a
// statement that already has an AS OF is rejected here with a clearer message.
func applyAsOfTimestamp(sql, timestamp string) (string, error) {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return "", fmt.Errorf("sql is required")
	}
	instant, _, err := parseRequestedTimestamp(timestamp)
	if err != nil {
		return "", err
	}
	if containsAsOfClause(trimmed) {
		return "", fmt.Errorf("the query already carries an AS OF clause: a query takes one, which applies to all of it")
	}
	return strings.TrimSuffix(trimmed, ";") + " " + asOfTimestampClause(instant), nil
}

// containsAsOfClause looks for an AS OF clause outside quoted text, so a string
// literal that happens to read "as of" does not count as one.
func containsAsOfClause(sql string) bool {
	inQuote := false
	upper := strings.ToUpper(sql)
	for i := 0; i < len(upper); i++ {
		if upper[i] == '\'' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		if strings.HasPrefix(upper[i:], "AS OF ") && (i == 0 || isSQLBoundary(upper[i-1])) {
			return true
		}
	}
	return false
}

func isSQLBoundary(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', '(', ',', ')':
		return true
	}
	return false
}
