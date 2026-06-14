package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"finador/internal/cli"
	"finador/internal/domain"
	"finador/internal/remote"
	"finador/internal/store"
)

// fakeBackend is an in-memory remote.Backend for the remote-mode tests. It never
// touches the network; it just stores the last-pushed bytes and a monotonically
// growing version token so conflict logic is exercisable.
type fakeBackend struct {
	mu      sync.Mutex
	data    []byte
	version int  // 0 means "no file yet"
	present bool // a file has been pushed
}

func (b *fakeBackend) Fetch(_ context.Context) ([]byte, remote.Version, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.present {
		return nil, "", remote.ErrRemoteMissing
	}
	return append([]byte(nil), b.data...), remote.Version(verStr(b.version)), nil
}

func (b *fakeBackend) Push(_ context.Context, data []byte, base remote.Version, _ string) (remote.Version, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cur := remote.Version("")
	if b.present {
		cur = remote.Version(verStr(b.version))
	}
	if base != cur {
		return "", remote.ErrRemoteConflict
	}
	b.data = append([]byte(nil), data...)
	b.version++
	b.present = true
	return remote.Version(verStr(b.version)), nil
}

func (b *fakeBackend) Describe() string { return "fake:owner/repo/portfolio.fin@main" }

func (b *fakeBackend) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...), b.present
}

func verStr(n int) string {
	return "v" + string(rune('0'+n%10)) + "-sha-" + strings.Repeat("a", 7)
}

// remoteEnv sets up the env so finador resolves to GitHub mode against a fresh
// config/cache dir, with a password and token from the environment (no prompts).
func remoteEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FINADOR_CONFIG_DIR", t.TempDir())
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	t.Setenv("FINADOR_PASSWORD", "secret-de-test")
	t.Setenv("GITHUB_TOKEN", "x")
	t.Setenv("FINADOR_DB", "") // never force local mode
}

// runRemote executes finador in GitHub mode against the fake backend. It does
// NOT pass --db (which would force local mode). --no-keychain keeps the real
// keychain untouched; --offline is NOT passed (the fake backend has no network).
func runRemote(t *testing.T, fake *fakeBackend, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := cli.New(cli.WithRemoteBackend(fake))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--no-keychain"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func mustRunRemote(t *testing.T, fake *fakeBackend, args ...string) string {
	t.Helper()
	out, err := runRemote(t, fake, args...)
	if err != nil {
		t.Fatalf("finador %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// configureRemote points finador at the fake backend via `remote set`.
func configureRemote(t *testing.T, fake *fakeBackend) {
	t.Helper()
	mustRunRemote(t, fake, "remote", "set", "owner/repo")
}

func TestRemoteInitPushesToBackend(t *testing.T) {
	remoteEnv(t)
	fake := &fakeBackend{}
	configureRemote(t, fake)

	if _, present := fake.snapshot(); present {
		t.Fatal("backend should be empty before init")
	}

	out := mustRunRemote(t, fake, "init")
	if !strings.Contains(out, "Created") {
		t.Errorf("init output missing 'Created': %q", out)
	}
	data, present := fake.snapshot()
	if !present || len(data) == 0 {
		t.Fatal("init should have pushed the ledger to the backend")
	}
}

func TestRemoteReadGoesThroughSync(t *testing.T) {
	remoteEnv(t)
	fake := &fakeBackend{}
	configureRemote(t, fake)
	mustRunRemote(t, fake, "init")

	mustRunRemote(t, fake, "account", "add", "PEA Zephyr")

	// A read should see the account that was pushed.
	out := mustRunRemote(t, fake, "account", "list")
	if !strings.Contains(out, "PEA Zephyr") {
		t.Errorf("remote read did not see the account:\n%s", out)
	}
}

func TestRemoteWritePushesUpdatedContent(t *testing.T) {
	remoteEnv(t)
	fake := &fakeBackend{}
	configureRemote(t, fake)
	mustRunRemote(t, fake, "init")

	before, _ := fake.snapshot()

	mustRunRemote(t, fake, "account", "add", "CTO IBKR")
	after, present := fake.snapshot()
	if !present {
		t.Fatal("backend lost its file after a write")
	}
	if bytes.Equal(before, after) {
		t.Fatal("write did not change the pushed bytes")
	}

	// A buy on a fresh asset also lands on the backend and is visible on re-read.
	mustRunRemote(t, fake, "asset", "add", "CW8.PA", "--alias", "cw8")
	mustRunRemote(t, fake, "asset", "buy", "cw8", "10", "@550", "2026-06-01")
	out := mustRunRemote(t, fake, "account", "list")
	if !strings.Contains(out, "CTO IBKR") {
		t.Errorf("second read missing the new account:\n%s", out)
	}
}

func TestRemoteShowHidesToken(t *testing.T) {
	remoteEnv(t)
	fake := &fakeBackend{}
	configureRemote(t, fake)

	out := mustRunRemote(t, fake, "remote", "show")
	for _, want := range []string{"github", "owner/repo", "portfolio.fin", "main"} {
		if !strings.Contains(out, want) {
			t.Errorf("remote show missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "x") && strings.Contains(strings.ToLower(out), "token") {
		// The literal token value "x" must never be printed as a token line.
		t.Errorf("remote show appears to leak the token:\n%s", out)
	}
	if strings.Contains(out, "GITHUB_TOKEN") {
		t.Errorf("remote show leaked the token env name:\n%s", out)
	}
}

func TestRemoteSyncRuns(t *testing.T) {
	remoteEnv(t)
	fake := &fakeBackend{}
	configureRemote(t, fake)
	mustRunRemote(t, fake, "init")

	out := mustRunRemote(t, fake, "sync")
	if !strings.Contains(out, "Synced") {
		t.Errorf("sync output missing 'Synced':\n%s", out)
	}
}

func TestSyncWithoutRemoteErrors(t *testing.T) {
	remoteEnv(t)
	fake := &fakeBackend{}
	// No `remote set`: config stays local.
	out, err := runRemote(t, fake, "sync")
	if err == nil {
		t.Fatalf("sync without a remote should error; got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "no remote configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRemoteAdoptMigratesLocalFile(t *testing.T) {
	remoteEnv(t)
	fake := &fakeBackend{}
	configureRemote(t, fake)

	// a populated local file to migrate (same password as remoteEnv)
	local := filepath.Join(t.TempDir(), "local.fin")
	f, err := store.Create(local, "secret-de-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Book.AddAccount(&domain.Account{ID: "pea", Name: "PEA Zephyr", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	if _, present := fake.snapshot(); present {
		t.Fatal("backend should be empty before adopt")
	}
	out := mustRunRemote(t, fake, "remote", "adopt", "--from", local)
	if !strings.Contains(out, "Uploaded") {
		t.Errorf("adopt output: %q", out)
	}
	if _, present := fake.snapshot(); !present {
		t.Fatal("adopt should have pushed the local file to the backend")
	}
	// the migrated data is now readable through GitHub mode
	if out := mustRunRemote(t, fake, "account", "list"); !strings.Contains(out, "PEA Zephyr") {
		t.Errorf("adopted account not visible after migration:\n%s", out)
	}
}

func TestSyncOnEmptyRemoteGuidesToInitOrAdopt(t *testing.T) {
	remoteEnv(t)
	fake := &fakeBackend{} // empty remote
	configureRemote(t, fake)

	out := mustRunRemote(t, fake, "sync")
	if !strings.Contains(out, "has no file yet") || !strings.Contains(out, "remote adopt") {
		t.Errorf("sync on an empty remote should guide to init/adopt:\n%s", out)
	}
	if strings.Contains(out, "Synced with") {
		t.Errorf("sync must not claim success on an empty remote:\n%s", out)
	}
}
