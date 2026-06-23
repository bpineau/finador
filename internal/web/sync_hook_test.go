package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"finador/internal/store"
)

// A write through the web UI must run the remote push hook, so the edit reaches
// the remote (and is protected from a later clobbering pull) instead of living
// only in the local working copy. Regression: web saves used to bypass the sync
// layer entirely, so on the next restart the startup pull silently overwrote
// them (state stayed Dirty=false because nothing ever pushed or marked dirty).
func TestWebWriteTriggersPush(t *testing.T) {
	srv, f := testServer(t)
	var pushes []string
	srv.sync = &Sync{Push: func(_ context.Context, msg string) (bool, error) {
		pushes = append(pushes, msg)
		return false, nil
	}}
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

// The push message must not leak entity names (asset/account labels) into the
// remote's commit history.
func TestWebPushMessageHasNoNames(t *testing.T) {
	srv, _ := testServer(t)
	var msg string
	srv.sync = &Sync{Push: func(_ context.Context, m string) (bool, error) {
		msg = m
		return false, nil
	}}
	if code, _, _ := postForm(t, srv, "/assets", url.Values{"name": {"Secret Holding SA"}}); code != 303 {
		t.Fatalf("create asset: status %d", code)
	}
	if strings.Contains(msg, "Secret Holding SA") {
		t.Fatalf("push message leaks the asset name: %q", msg)
	}
}

// When the push reports that a merge rewrote the working copy (e.g. a conflict
// with a concurrent Android write), the server must Reload its File so its
// in-memory book reflects the merged remote records.
func TestWebPushReloadsOnMerge(t *testing.T) {
	srv, _ := testServer(t)
	reloaded := false
	srv.sync = &Sync{
		Push: func(_ context.Context, _ string) (bool, error) {
			return true, nil // signal: a merge rewrote the working copy
		},
		Reload: func() (*store.File, error) {
			reloaded = true
			return srv.file, nil // keep the same file; we only assert Reload ran
		},
	}
	if code, _, _ := postForm(t, srv, "/assets", url.Values{"name": {"New Co"}}); code != 303 {
		t.Fatalf("create asset: status %d", code)
	}
	if !reloaded {
		t.Fatal("server did not reload its File after a merge-on-push")
	}
}

// A failing push is surfaced to the caller (not swallowed): the local working
// copy is saved, but the user must know it did not reach the remote.
func TestWebWritePushErrorSurfaces(t *testing.T) {
	srv, _ := testServer(t)
	srv.sync = &Sync{Push: func(_ context.Context, _ string) (bool, error) {
		return false, errors.New("boom")
	}}
	code, body, _ := postForm(t, srv, "/assets", url.Values{"name": {"New Co"}})
	if code == 303 {
		t.Fatal("expected the push failure to surface, got a normal redirect")
	}
	if !strings.Contains(body, "boom") {
		t.Fatalf("push error not surfaced in response: %q", body)
	}
}
