package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBackend is an in-memory Backend for tests. It tracks a content blob and a
// monotonic SHA counter, can be put offline, and can simulate a concurrent
// remote change (advancing the SHA so the next Push sees a stale base).
type fakeBackend struct {
	mu        sync.Mutex
	data      []byte
	sha       Version // "" means the remote has no file yet
	counter   int
	offline   bool
	fetches   int
	pushes    int
	authError bool
}

func (f *fakeBackend) Describe() string { return "fake:remote" }

func (f *fakeBackend) CheckAccess(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offline {
		return ErrOffline
	}
	if f.authError {
		return ErrRemoteAuth
	}
	return nil
}

func (f *fakeBackend) nextSHA() Version {
	f.counter++
	return Version(fmt.Sprintf("sha%d", f.counter))
}

func (f *fakeBackend) Fetch(ctx context.Context) ([]byte, Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches++
	if f.offline {
		return nil, "", ErrOffline
	}
	if f.authError {
		return nil, "", ErrRemoteAuth
	}
	if f.sha == "" {
		return nil, "", ErrRemoteMissing
	}
	out := make([]byte, len(f.data))
	copy(out, f.data)
	return out, f.sha, nil
}

func (f *fakeBackend) Push(ctx context.Context, data []byte, base Version, message string) (Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes++
	if f.offline {
		return "", ErrOffline
	}
	if f.authError {
		return "", ErrRemoteAuth
	}
	if base != f.sha {
		return "", ErrRemoteConflict
	}
	f.data = make([]byte, len(data))
	copy(f.data, data)
	f.sha = f.nextSHA()
	return f.sha, nil
}

// concurrentChange simulates another machine pushing to the remote: it sets new
// content and advances the SHA, so a Push with the old base will conflict.
func (f *fakeBackend) concurrentChange(content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = []byte(content)
	f.sha = f.nextSHA()
}

// mergeMarker is appended by the stub MergeFunc so tests can assert it ran.
const mergeMarker = "\n<merged>"

// stubMerge reads the remote temp file and the local copy, then writes a
// deterministic merged result (local + remote + marker) back to the local copy.
// It records each invocation.
type stubMerge struct {
	calls int
}

func (m *stubMerge) fn(localPath, remotePath string) error {
	m.calls++
	local, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	remote, err := os.ReadFile(remotePath)
	if err != nil {
		return err
	}
	merged := string(local) + "|" + string(remote) + mergeMarker
	return os.WriteFile(localPath, []byte(merged), 0o600)
}

// failMerge always errors, to prove conflict paths surface merge failures.
func failMerge(localPath, remotePath string) error {
	return errors.New("boom")
}

// newSyncer builds a Syncer with a controllable clock for a test.
func newSyncer(t *testing.T, b Backend, clk *time.Time) *Syncer {
	t.Helper()
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	gh := GitHub{Owner: "alice", Repo: "data", Path: "portfolio.fin", Branch: "main"}
	s, err := NewSyncer(b, gh, time.Hour)
	if err != nil {
		t.Fatalf("NewSyncer: %v", err)
	}
	s.clock = func() time.Time { return *clk }
	return s
}

func readCopy(t *testing.T, s *Syncer) string {
	t.Helper()
	data, err := os.ReadFile(s.WorkingCopy())
	if err != nil {
		t.Fatalf("read working copy: %v", err)
	}
	return string(data)
}

func writeCopy(t *testing.T, s *Syncer, content string) {
	t.Helper()
	if err := atomicWrite(s.WorkingCopy(), []byte(content)); err != nil {
		t.Fatalf("write working copy: %v", err)
	}
}

// --- hashKey ---

func TestHashKeyStableAndDistinct(t *testing.T) {
	a := GitHub{Owner: "alice", Repo: "data", Path: "portfolio.fin"}
	b := GitHub{Owner: "alice", Repo: "data", Path: "other.fin"}
	aCopy := GitHub{Owner: "alice", Repo: "data", Path: "portfolio.fin"}
	if hashKey(a) != hashKey(aCopy) {
		t.Fatal("hashKey not stable")
	}
	if hashKey(a) == hashKey(b) {
		t.Fatal("hashKey not distinct for different paths")
	}
	if len(hashKey(a)) != 32 { // first 16 bytes, hex-encoded
		t.Fatalf("hashKey length = %d, want 32", len(hashKey(a)))
	}
}

// --- Fresh read uses cache ---

func TestForReadFreshUsesCache(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{}
	s := newSyncer(t, b, &now)

	// Seed a clean copy with a recent LastPull → not stale.
	writeCopy(t, s, "local content")
	if err := s.saveState(state{SHA: "sha1", LastPull: now, Dirty: false}); err != nil {
		t.Fatal(err)
	}

	warnings, err := s.ForRead(context.Background())
	if err != nil {
		t.Fatalf("ForRead: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if b.fetches != 0 {
		t.Fatalf("expected no Fetch, got %d", b.fetches)
	}
	if got := readCopy(t, s); got != "local content" {
		t.Fatalf("copy changed: %q", got)
	}
}

// --- Stale read pulls ---

func TestForReadStalePulls(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{data: []byte("remote content"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	writeCopy(t, s, "old local")
	old := now.Add(-2 * time.Hour) // older than readPullAfter (1h)
	if err := s.saveState(state{SHA: "shaOld", LastPull: old, Dirty: false}); err != nil {
		t.Fatal(err)
	}

	warnings, err := s.ForRead(context.Background())
	if err != nil {
		t.Fatalf("ForRead: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if b.fetches != 1 {
		t.Fatalf("expected 1 Fetch, got %d", b.fetches)
	}
	if got := readCopy(t, s); got != "remote content" {
		t.Fatalf("copy not refreshed: %q", got)
	}
	st, _ := s.loadState()
	if st.SHA != "shaR" {
		t.Fatalf("sha not updated: %q", st.SHA)
	}
	if !st.LastPull.Equal(now) {
		t.Fatalf("lastPull not updated: %v", st.LastPull)
	}
}

// --- Write: pull-before then push-after ---

func TestForWriteThenAfterWrite(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{data: []byte("remote base"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	// ForWrite on a clean state pulls a fresh base.
	if _, err := s.ForWrite(context.Background(), (&stubMerge{}).fn); err != nil {
		t.Fatalf("ForWrite: %v", err)
	}
	if got := readCopy(t, s); got != "remote base" {
		t.Fatalf("copy not pulled before write: %q", got)
	}

	// Caller mutates the working copy.
	writeCopy(t, s, "mutated content")

	warnings, err := s.AfterWrite(context.Background(), "finador: add (now)", (&stubMerge{}).fn)
	if err != nil {
		t.Fatalf("AfterWrite: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	b.mu.Lock()
	remote := string(b.data)
	b.mu.Unlock()
	if remote != "mutated content" {
		t.Fatalf("remote not updated: %q", remote)
	}
	st, _ := s.loadState()
	if st.Dirty {
		t.Fatal("expected dirty=false after push")
	}
}

// --- Conflict on push routes through merge then re-pushes ---

func TestAfterWriteConflictMergesAndRepushes(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{data: []byte("remote base"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	if _, err := s.ForWrite(context.Background(), (&stubMerge{}).fn); err != nil {
		t.Fatalf("ForWrite: %v", err)
	}
	writeCopy(t, s, "my local edit")

	// Another machine pushes between ForWrite and AfterWrite.
	b.concurrentChange("their remote edit")

	m := &stubMerge{}
	warnings, err := s.AfterWrite(context.Background(), "finador: add", m.fn)
	if err != nil {
		t.Fatalf("AfterWrite: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if m.calls != 1 {
		t.Fatalf("expected merge to run once, got %d", m.calls)
	}

	b.mu.Lock()
	remote := string(b.data)
	b.mu.Unlock()
	want := "my local edit|their remote edit" + mergeMarker
	if remote != want {
		t.Fatalf("merged content not landed.\n got: %q\nwant: %q", remote, want)
	}
	st, _ := s.loadState()
	if st.Dirty {
		t.Fatal("expected dirty=false after merged re-push")
	}
}

// --- Conflict with no merge func is an error (data-safety) ---

func TestAfterWriteConflictNoMergeErrors(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{data: []byte("base"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	if _, err := s.ForWrite(context.Background(), nil); err != nil {
		t.Fatalf("ForWrite: %v", err)
	}
	writeCopy(t, s, "local")
	b.concurrentChange("remote moved")

	if _, err := s.AfterWrite(context.Background(), "msg", nil); err == nil {
		t.Fatal("expected an error when conflicting with no merge func")
	}
}

// --- Offline read uses the copy with a warning ---

func TestForReadOfflineUsesCopy(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{offline: true}
	s := newSyncer(t, b, &now)

	writeCopy(t, s, "stale local")
	old := now.Add(-2 * time.Hour)
	if err := s.saveState(state{SHA: "shaOld", LastPull: old, Dirty: false}); err != nil {
		t.Fatal(err)
	}

	warnings, err := s.ForRead(context.Background())
	if err != nil {
		t.Fatalf("ForRead offline: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "local copy") {
		t.Fatalf("expected an offline warning, got %v", warnings)
	}
	if got := readCopy(t, s); got != "stale local" {
		t.Fatalf("offline read clobbered the copy: %q", got)
	}
}

// --- Offline read with no copy is an error ---

func TestForReadOfflineNoCopyErrors(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{offline: true}
	s := newSyncer(t, b, &now)
	// No copy, no state → stale by definition, offline → error.

	_, err := s.ForRead(context.Background())
	if err == nil {
		t.Fatal("expected an error: offline with nothing to read")
	}
	if !errors.Is(err, ErrOffline) {
		t.Fatalf("expected ErrOffline, got %v", err)
	}
}

// --- Offline write is lossless: dirty + later push ---

func TestOfflineWriteThenOnlineSync(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{data: []byte("remote base"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	// Establish a clean fresh base first.
	if _, err := s.ForWrite(context.Background(), (&stubMerge{}).fn); err != nil {
		t.Fatalf("ForWrite: %v", err)
	}
	writeCopy(t, s, "important local edit")

	// Go offline before pushing.
	b.mu.Lock()
	b.offline = true
	b.mu.Unlock()

	warnings, err := s.AfterWrite(context.Background(), "finador: add", (&stubMerge{}).fn)
	if err != nil {
		t.Fatalf("offline AfterWrite must not error: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not pushed") {
		t.Fatalf("expected a 'not pushed' warning, got %v", warnings)
	}
	st, _ := s.loadState()
	if !st.Dirty {
		t.Fatal("expected dirty=true after offline write")
	}
	if got := readCopy(t, s); got != "important local edit" {
		t.Fatalf("offline write lost the edit: %q", got)
	}

	// Back online: a Sync pushes the dirty copy.
	b.mu.Lock()
	b.offline = false
	b.mu.Unlock()

	if _, err = s.Sync(context.Background(), (&stubMerge{}).fn); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	b.mu.Lock()
	remote := string(b.data)
	b.mu.Unlock()
	if remote != "important local edit" {
		t.Fatalf("dirty copy not pushed on reconnect: %q", remote)
	}
	st, _ = s.loadState()
	if st.Dirty {
		t.Fatal("expected dirty=false after a successful Sync")
	}
}

// --- DIRTY IS NOT CLOBBERED on read ---

func TestForReadDirtyNotClobbered(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{data: []byte("newer remote"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	writeCopy(t, s, "unpushed local edit")
	old := now.Add(-2 * time.Hour) // very stale...
	if err := s.saveState(state{SHA: "shaOld", LastPull: old, Dirty: true}); err != nil {
		t.Fatal(err)
	}

	warnings, err := s.ForRead(context.Background())
	if err != nil {
		t.Fatalf("ForRead: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	// ...but dirty must short-circuit: no Fetch, copy intact.
	if b.fetches != 0 {
		t.Fatalf("dirty read must not Fetch, got %d", b.fetches)
	}
	if got := readCopy(t, s); got != "unpushed local edit" {
		t.Fatalf("dirty copy was clobbered: %q", got)
	}
}

// --- DIRTY IS NOT CLOBBERED on write: must go through merge ---

func TestForWriteDirtyRoutesThroughMerge(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	// Remote has advanced since the dirty copy's base, so the reconcile push
	// will conflict and must merge — never overwrite the local edit blindly.
	b := &fakeBackend{data: []byte("remote moved on"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	writeCopy(t, s, "dirty local edit")
	if err := s.saveState(state{SHA: "shaOld", LastPull: now.Add(-2 * time.Hour), Dirty: true}); err != nil {
		t.Fatal(err)
	}

	m := &stubMerge{}
	warnings, err := s.ForWrite(context.Background(), m.fn)
	if err != nil {
		t.Fatalf("ForWrite: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if m.calls != 1 {
		t.Fatalf("dirty ForWrite must reconcile via merge, calls=%d", m.calls)
	}
	// The merged content must include the local edit (no clobber).
	got := readCopy(t, s)
	if !strings.Contains(got, "dirty local edit") {
		t.Fatalf("local edit lost during reconcile: %q", got)
	}
	st, _ := s.loadState()
	if st.Dirty {
		t.Fatal("expected dirty=false after reconcile")
	}
	b.mu.Lock()
	remote := string(b.data)
	b.mu.Unlock()
	if !strings.Contains(remote, "dirty local edit") {
		t.Fatalf("reconciled remote missing local edit: %q", remote)
	}
}

// --- Dirty ForWrite offline: stays dirty, proceeds, warns ---

func TestForWriteDirtyOfflineStaysDirty(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{offline: true}
	s := newSyncer(t, b, &now)

	writeCopy(t, s, "dirty offline edit")
	if err := s.saveState(state{SHA: "shaOld", LastPull: now.Add(-2 * time.Hour), Dirty: true}); err != nil {
		t.Fatal(err)
	}

	warnings, err := s.ForWrite(context.Background(), (&stubMerge{}).fn)
	if err != nil {
		t.Fatalf("ForWrite offline must not error: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "push later") {
		t.Fatalf("expected a 'push later' warning, got %v", warnings)
	}
	st, _ := s.loadState()
	if !st.Dirty {
		t.Fatal("expected to stay dirty while offline")
	}
	if got := readCopy(t, s); got != "dirty offline edit" {
		t.Fatalf("offline dirty ForWrite clobbered the copy: %q", got)
	}
}

// --- First push: remote missing, copy created by caller, push with base "" ---

func TestFirstPushCreatesRemote(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{} // empty: sha == "" → ErrRemoteMissing
	s := newSyncer(t, b, &now)

	// ForWrite with nothing anywhere: first-init, must proceed without error.
	warnings, err := s.ForWrite(context.Background(), (&stubMerge{}).fn)
	if err != nil {
		t.Fatalf("ForWrite first-init: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings on first init: %v", warnings)
	}
	st, _ := s.loadState()
	if st.SHA != "" {
		t.Fatalf("expected empty sha on first init, got %q", st.SHA)
	}

	// Caller creates the working copy and saves it.
	writeCopy(t, s, "first content")

	if _, err := s.AfterWrite(context.Background(), "finador: init", (&stubMerge{}).fn); err != nil {
		t.Fatalf("AfterWrite first push: %v", err)
	}
	b.mu.Lock()
	remote := string(b.data)
	sha := b.sha
	b.mu.Unlock()
	if remote != "first content" {
		t.Fatalf("first push didn't create the remote content: %q", remote)
	}
	if sha == "" {
		t.Fatal("expected the remote to have a sha after first push")
	}
	st, _ = s.loadState()
	if st.SHA != sha || st.Dirty {
		t.Fatalf("state after first push: sha=%q dirty=%v", st.SHA, st.Dirty)
	}
}

// --- Auth error surfaces (distinct from offline) ---

func TestAfterWriteAuthErrorSurfaces(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{authError: true}
	s := newSyncer(t, b, &now)

	writeCopy(t, s, "local")
	if err := s.saveState(state{SHA: "", LastPull: now, Dirty: false}); err != nil {
		t.Fatal(err)
	}

	_, err := s.AfterWrite(context.Background(), "msg", (&stubMerge{}).fn)
	if !errors.Is(err, ErrRemoteAuth) {
		t.Fatalf("expected ErrRemoteAuth, got %v", err)
	}

	// DATA SAFETY: even though the push failed with an error (not offline),
	// the state must be persisted dirty so a later read does not clobber the
	// just-mutated working copy.
	st, _ := s.loadState()
	if !st.Dirty {
		t.Fatal("expected dirty=true persisted after a failed push")
	}
	// A subsequent read (online again) must NOT overwrite the dirty copy.
	b.mu.Lock()
	b.authError = false
	b.data = []byte("remote content")
	b.sha = "shaServer"
	b.mu.Unlock()
	if _, err := s.ForRead(context.Background()); err != nil {
		t.Fatalf("ForRead after failed push: %v", err)
	}
	if got := readCopy(t, s); got != "local" {
		t.Fatalf("dirty copy clobbered by read after a failed push: %q", got)
	}
}

// --- Conflict with a failing merge surfaces the merge error ---

func TestAfterWriteConflictMergeFailureSurfaces(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{data: []byte("base"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	if _, err := s.ForWrite(context.Background(), (&stubMerge{}).fn); err != nil {
		t.Fatalf("ForWrite: %v", err)
	}
	writeCopy(t, s, "local")
	b.concurrentChange("remote moved")

	if _, err := s.AfterWrite(context.Background(), "msg", failMerge); err == nil {
		t.Fatal("expected a merge failure to surface")
	}
}

// --- Sync with a clean state just refreshes ---

func TestSyncCleanRefreshes(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	b := &fakeBackend{data: []byte("remote fresh"), sha: "shaR", counter: 9}
	s := newSyncer(t, b, &now)

	writeCopy(t, s, "old")
	if err := s.saveState(state{SHA: "shaOld", LastPull: now.Add(-2 * time.Hour), Dirty: false}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Sync(context.Background(), (&stubMerge{}).fn); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := readCopy(t, s); got != "remote fresh" {
		t.Fatalf("Sync didn't refresh the copy: %q", got)
	}
	if b.pushes != 0 {
		t.Fatalf("clean Sync should not Push, got %d", b.pushes)
	}
}

func TestForReadOnEmptyRemoteErrs(t *testing.T) {
	now := time.Now()
	b := &fakeBackend{} // empty: Fetch returns ErrRemoteMissing
	s := newSyncer(t, b, &now)
	if _, err := s.ForRead(context.Background()); !errors.Is(err, ErrRemoteMissing) {
		t.Fatalf("ForRead on an empty remote = %v, want ErrRemoteMissing", err)
	}
}

func TestAdoptSeedsEmptyRemote(t *testing.T) {
	now := time.Now()
	b := &fakeBackend{}
	s := newSyncer(t, b, &now)

	if err := s.Adopt(context.Background(), []byte("ENCRYPTED"), "adopt", false); err != nil {
		t.Fatal(err)
	}
	// pushed to the backend and installed as the working copy
	if got := readCopy(t, s); got != "ENCRYPTED" {
		t.Fatalf("working copy = %q, want ENCRYPTED", got)
	}
	data, _, err := b.Fetch(context.Background())
	if err != nil || string(data) != "ENCRYPTED" {
		t.Fatalf("remote = %q (err %v), want ENCRYPTED", data, err)
	}
	// state reflects a clean, freshly-pulled copy → next read won't re-pull
	if sha, _, dirty := s.Status(); sha == "" || dirty {
		t.Fatalf("state after adopt: sha=%q dirty=%v", sha, dirty)
	}
}

func TestAdoptRefusesExistingUnlessForce(t *testing.T) {
	now := time.Now()
	b := &fakeBackend{}
	s := newSyncer(t, b, &now)
	if _, err := b.Push(context.Background(), []byte("OLD"), "", "seed"); err != nil {
		t.Fatal(err)
	}
	if err := s.Adopt(context.Background(), []byte("NEW"), "adopt", false); err == nil {
		t.Fatal("adopt should refuse to overwrite an existing remote file")
	}
	if err := s.Adopt(context.Background(), []byte("NEW"), "adopt", true); err != nil {
		t.Fatalf("forced adopt: %v", err)
	}
	if data, _, _ := b.Fetch(context.Background()); string(data) != "NEW" {
		t.Fatalf("remote = %q, want NEW after forced adopt", data)
	}
}
