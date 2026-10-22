package studioapp

import (
	"strings"
	"testing"
	"time"
)

func TestParseCommitTimeReadsEngineTextForms(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{value: "2026-10-22 05:44:25.123456", want: "2026-10-22T05:44:25.123456Z"},
		{value: "2026-10-22 05:44:25", want: "2026-10-22T05:44:25Z"},
		{value: "2026-10-22T05:44:25Z", want: "2026-10-22T05:44:25Z"},
		// A commit from before the engine recorded commit times has none, which
		// is not an error.
		{value: "", want: ""},
		{value: "not a time", want: ""},
	}
	for _, test := range tests {
		got := parseCommitTime(test.value)
		if test.want == "" {
			if !got.IsZero() {
				t.Fatalf("parseCommitTime(%q) = %v, want the zero time", test.value, got)
			}
			continue
		}
		if formatted := got.Format(time.RFC3339Nano); formatted != test.want {
			t.Fatalf("parseCommitTime(%q) = %s, want %s", test.value, formatted, test.want)
		}
	}
}

func TestParseRequestedTimestampAcceptsTheFormsTheUISends(t *testing.T) {
	for _, value := range []string{
		"2026-10-22T05:44:25.5Z",
		"2026-10-22T05:44:25Z",
		"2026-10-22T05:44:25+02:00",
		"2026-10-22T05:44:25",
		"2026-10-22T05:44",
		"2026-10-22 05:44:25",
		"2026-10-22",
	} {
		if _, _, err := parseRequestedTimestamp(value); err != nil {
			t.Fatalf("parseRequestedTimestamp(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "   ", "yesterday", "22/10/2026"} {
		if _, _, err := parseRequestedTimestamp(value); err == nil {
			t.Fatalf("expected an error for %q", value)
		}
	}
}

func TestParseRequestedTimestampNormalizesToUTC(t *testing.T) {
	_, normalized, err := parseRequestedTimestamp("2026-10-22T07:44:25+02:00")
	if err != nil {
		t.Fatalf("parseRequestedTimestamp: %v", err)
	}
	if normalized != "2026-10-22T05:44:25Z" {
		t.Fatalf("normalized = %s, want 2026-10-22T05:44:25Z", normalized)
	}
}

func TestApplyAsOfTimestampAppendsOneClause(t *testing.T) {
	got, err := applyAsOfTimestamp("SELECT id FROM users ORDER BY id LIMIT 10;", "2026-10-22T05:44:25Z")
	if err != nil {
		t.Fatalf("applyAsOfTimestamp: %v", err)
	}
	want := "SELECT id FROM users ORDER BY id LIMIT 10 AS OF TIMESTAMP '2026-10-22T05:44:25Z'"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestApplyAsOfTimestampRejectsASecondClause(t *testing.T) {
	// The engine applies one AS OF to the whole query and fails a query with
	// more than one, so the studio has to catch it first.
	for _, sql := range []string{
		"SELECT id FROM users AS OF LSN 7",
		"SELECT id FROM users AS OF TIMESTAMP '2026-10-01T00:00:00Z'",
		"SELECT id FROM users as of lsn 7",
	} {
		if _, err := applyAsOfTimestamp(sql, "2026-10-22T05:44:25Z"); err == nil {
			t.Fatalf("expected an error for %q", sql)
		}
	}
}

func TestApplyAsOfTimestampIgnoresQuotedText(t *testing.T) {
	// "as of" inside a string literal is not a clause.
	got, err := applyAsOfTimestamp("SELECT id FROM notes WHERE body = 'as of yesterday'", "2026-10-22T05:44:25Z")
	if err != nil {
		t.Fatalf("applyAsOfTimestamp: %v", err)
	}
	if !strings.HasSuffix(got, "AS OF TIMESTAMP '2026-10-22T05:44:25Z'") {
		t.Fatalf("clause was not appended: %q", got)
	}
}

func TestApplyAsOfTimestampValidatesItsInput(t *testing.T) {
	if _, err := applyAsOfTimestamp("", "2026-10-22T05:44:25Z"); err == nil {
		t.Fatal("expected an error for empty SQL")
	}
	if _, err := applyAsOfTimestamp("SELECT 1", "whenever"); err == nil {
		t.Fatal("expected an error for an unreadable timestamp")
	}
}

func TestTimeTravelRejectsMoreThanOneBoundary(t *testing.T) {
	app := &App{}
	if _, err := app.TimeTravel(timeTravelRequest{SQL: "SELECT 1"}); err == nil {
		t.Fatal("expected an error without a boundary")
	}
	_, err := app.TimeTravel(timeTravelRequest{SQL: "SELECT 1", LSN: 7, Timestamp: "2026-10-22T05:44:25Z"})
	if err == nil {
		t.Fatal("expected an error for two boundaries")
	}
	if !strings.Contains(err.Error(), "one boundary") {
		t.Fatalf("unexpected error %v", err)
	}
}
