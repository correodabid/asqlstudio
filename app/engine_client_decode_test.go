package studioapp

import (
	"context"
	"testing"

	api "github.com/correodabid/asql/pkg/adminapi"
	"github.com/jackc/pgx/v5"
)

// TestRowDecodingMatchesTextFormat is the regression test for the row decoder.
// pgx asks for binary result formats over the extended query protocol, so every
// INT, FLOAT, BOOL and TIMESTAMP column arrives as raw bytes; reading those
// bytes as a string displayed 42 as "\x00\x00\x00\x00\x00\x00\x00*" and made
// toUint64 read every metric as 0. Both protocols must now agree.
func TestRowDecodingMatchesTextFormat(t *testing.T) {
	ctx := context.Background()
	addr := startStudioPGWireServer(t, "")
	client := newEngineClient(addr, "")
	t.Cleanup(client.Close)

	begin, err := client.BeginTx(ctx, &api.BeginTxRequest{Mode: "domain", Domains: []string{"shop"}})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, sql := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, name TEXT, price FLOAT, active BOOL, seen TIMESTAMP)",
		"INSERT INTO items (id, name, price, active, seen) VALUES (42, 'widget', 9.5, true, '2026-10-22 05:44:25')",
	} {
		if _, err := client.Execute(ctx, &api.ExecuteRequest{TxID: begin.TxID, SQL: sql}); err != nil {
			t.Fatalf("execute %q: %v", sql, err)
		}
	}
	if _, err := client.CommitTx(ctx, &api.CommitTxRequest{TxID: begin.TxID}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	const query = "SELECT id, name, price, active, seen FROM items"

	begin, err = client.BeginTx(ctx, &api.BeginTxRequest{Mode: "domain", Domains: []string{"shop"}})
	if err != nil {
		t.Fatalf("begin read: %v", err)
	}
	t.Cleanup(func() { _, _ = client.RollbackTx(ctx, &api.RollbackTxRequest{TxID: begin.TxID}) })
	binaryResult, err := client.Execute(ctx, &api.ExecuteRequest{TxID: begin.TxID, SQL: query})
	if err != nil {
		t.Fatalf("read over the extended protocol: %v", err)
	}
	if len(binaryResult.Rows) != 1 {
		t.Fatalf("read %d rows, want 1", len(binaryResult.Rows))
	}

	want := map[string]string{
		"id":     "42",
		"name":   "widget",
		"price":  "9.5",
		"active": "t",
		"seen":   "2026-10-22 05:44:25",
	}
	for column, expected := range want {
		if got := toString(binaryResult.Rows[0][column]); got != expected {
			t.Errorf("%s = %q, want %q", column, got, expected)
		}
	}

	// The simple protocol asks for text, so it is the reference the decoded
	// binary values have to match.
	textRows, err := client.queryWithDomainsMode(ctx, []string{"shop"}, query, pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		t.Fatalf("read over the simple protocol: %v", err)
	}
	if len(textRows) != 1 {
		t.Fatalf("read %d rows over the simple protocol, want 1", len(textRows))
	}
	for column := range want {
		binary := toString(binaryResult.Rows[0][column])
		text := toString(textRows[0][column])
		if binary != text {
			t.Errorf("%s: binary %q != text %q", column, binary, text)
		}
	}
}

// TestNumericAdminColumnsParse covers what the decoding bug broke everywhere
// else: the admin tables the dashboard reads hold their numbers in binary
// columns, and toUint64 has to get a number out of them.
func TestNumericAdminColumnsParse(t *testing.T) {
	ctx := context.Background()
	addr := startStudioPGWireServer(t, "")
	client := newEngineClient(addr, "")
	t.Cleanup(client.Close)

	begin, err := client.BeginTx(ctx, &api.BeginTxRequest{Mode: "domain", Domains: []string{"shop"}})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := client.Execute(ctx, &api.ExecuteRequest{TxID: begin.TxID, SQL: "CREATE TABLE items (id INT PRIMARY KEY)"}); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := client.CommitTx(ctx, &api.CommitTxRequest{TxID: begin.TxID}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	commits, err := client.TimelineCommits(ctx, &timelineCommitsRequest{})
	if err != nil {
		t.Fatalf("TimelineCommits: %v", err)
	}
	if len(commits.Commits) == 0 {
		t.Fatal("the timeline reported no commits")
	}
	for _, commit := range commits.Commits {
		if commit.LSN == 0 {
			t.Fatalf("commit %+v decoded its LSN as 0", commit)
		}
		if commit.MutationCount == 0 {
			t.Fatalf("commit %d decoded its mutation count as 0", commit.LSN)
		}
		// The commit's wall-clock time arrives as a binary timestamp.
		if commit.CommitTime == "" || commit.CommitTimeMicros == 0 {
			t.Fatalf("commit %d decoded no wall-clock time", commit.LSN)
		}
	}

	stats, err := client.EngineStats(ctx)
	if err != nil {
		t.Fatalf("EngineStats: %v", err)
	}
	if stats.TotalCommits == 0 {
		t.Fatal("engine stats decoded total_commits as 0 after a commit")
	}
}
