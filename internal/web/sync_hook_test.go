package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// A write through the web UI must run the remote push hook, so the edit reaches
// the remote (and is protected from a later clobbering pull) instead of living
// only in the local working copy. Regression: web saves used to bypass the sync
// layer entirely, so on the next restart the startup pull silently overwrote
// them (state stayed Dirty=false because nothing ever pushed or marked dirty).
func TestWebWriteTriggersPush(t *testing.T) {
	srv, f := testServer(t)
	var pushes []string
	srv.push = func(_ context.Context, msg string) error {
		pushes = append(pushes, msg)
		return nil
	}
	before := len(f.LogEntries())

	code, _, _ := postForm(t, srv, "/assets", url.Values{"name": {"New Co"}})
	if code != 303 {
		t.Fatalf("create asset: status %d", code)
	}
	if len(pushes) != 1 {
		t.Fatalf("expected exactly one push after a web write, got %d", len(pushes))
	}
	if pushes[0] == "" {
		t.Fatal("push message is empty")
	}
	if got := len(f.LogEntries()); got <= before {
		t.Fatalf("save did not append a log entry: before=%d after=%d", before, got)
	}
}

// A failing push is surfaced to the caller (not swallowed): the local working
// copy is saved, but the user must know it did not reach the remote.
func TestWebWritePushErrorSurfaces(t *testing.T) {
	srv, _ := testServer(t)
	srv.push = func(_ context.Context, _ string) error {
		return errors.New("boom")
	}
	code, body, _ := postForm(t, srv, "/assets", url.Values{"name": {"New Co"}})
	if code == 303 {
		t.Fatal("expected the push failure to surface, got a normal redirect")
	}
	if !strings.Contains(body, "boom") {
		t.Fatalf("push error not surfaced in response: %q", body)
	}
}
