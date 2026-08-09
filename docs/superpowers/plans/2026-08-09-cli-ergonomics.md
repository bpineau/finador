# CLI ergonomics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** XDG paths with a one-time migration, a `config get` that shows the whole truth, an `--asset` filter on the read commands, and plural command names.

**Architecture:** A new leaf package `internal/paths` becomes the single answer to "where does finador keep things"; `store`, `remote` and `cli` stop resolving directories themselves. `--asset` is a filter field on the existing `portfolio.Scope` (twin of `Excluded`), not a new `ScopeKind`, so it reaches value, series, trees and web through the one predicate they all share. Plurals are cobra aliases plus a flag normalizer.

**Tech Stack:** Go 1.26, cobra, stdlib tests (table-driven, colocated, no framework).

**Spec:** `docs/superpowers/specs/2026-08-09-cli-ergonomics-design.md`

## Global Constraints

- Pure Go, no CGo, no new dependency. Dependency budget: cobra, shopspring/decimal, samber/lo, x/crypto, x/term, `github.com/bpineau/pofo`.
- Everything user-visible, in code and in docs, is **English**. Plain hyphens only, **never an em-dash**, anywhere.
- Dependency direction: never import upward. `internal/paths` imports nothing internal; `store`, `remote` and `cli` may import it.
- Never hit the network in tests. Point the sidecar cache at a temp dir with `FINADOR_CACHE_DIR`.
- `make check` (fmt-check + vet + lint + test + race) must pass before every commit; the pre-commit hook runs the same gate.
- Commit to **master**. Push at the end of the session.
- Public examples and fixtures use fictitious brokers/tickers (PEA Zephyr, CTO Meridia, CW8.PA), never real personal data.
- Spec amendment already folded in below: the three `FINADOR_*_DIR` variables all name **finador's own directory** (not a base directory). This changes `FINADOR_CACHE_DIR`, which today names a base and gets `finador/` appended; no test asserts that subdirectory, so the change is invisible to them.
- Second spec amendment: migrations are skipped only by the `FINADOR_*_DIR` / `FINADOR_DB` environment overrides, not by an explicit `--db`. `Migrate` is called from `cmd/finador/main.go`, so a test binary (which calls `cli.New()` directly) never migrates a developer's real data.

---

### Task 1: `internal/paths` - resolution

**Files:**
- Create: `internal/paths/paths.go`
- Create: `internal/paths/paths_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `paths.Config() (string, error)`, `paths.Cache() (string, error)`, `paths.Data() (string, error)`, `paths.Ledger() (string, error)`, `const paths.LedgerName = "finador.fin"`. Each of Config/Cache/Data returns finador's own directory (the `finador` leaf included), never a base directory.

- [ ] **Step 1: Write the failing test**

Create `internal/paths/paths_test.go`:

```go
package paths

import (
	"path/filepath"
	"testing"
)

// setHome points every resolver at a private home and clears the overrides a
// developer may have exported.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{
		"FINADOR_CONFIG_DIR", "FINADOR_CACHE_DIR", "FINADOR_DATA_DIR",
		"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
	} {
		t.Setenv(k, "")
	}
	return home
}

func TestResolveDefaults(t *testing.T) {
	home := setHome(t)
	cases := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"config", Config, filepath.Join(home, ".config", "finador")},
		{"cache", Cache, filepath.Join(home, ".cache", "finador")},
		{"data", Data, filepath.Join(home, ".local", "share", "finador")},
		{"ledger", Ledger, filepath.Join(home, ".local", "share", "finador", "finador.fin")},
	}
	for _, c := range cases {
		got, err := c.fn()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestResolveOverrides(t *testing.T) {
	home := setHome(t)
	// XDG_* names a base directory: "finador" is appended.
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	if got, _ := Cache(); got != filepath.Join("/xdg", "cache", "finador") {
		t.Errorf("XDG_CACHE_HOME ignored: %q", got)
	}
	// A relative XDG base is invalid per spec and must be ignored.
	t.Setenv("XDG_CONFIG_HOME", "relative/path")
	if got, _ := Config(); got != filepath.Join(home, ".config", "finador") {
		t.Errorf("relative XDG_CONFIG_HOME should be ignored: %q", got)
	}
	// FINADOR_*_DIR names finador's own directory: nothing is appended.
	t.Setenv("FINADOR_CONFIG_DIR", "/tmp/cfg")
	if got, _ := Config(); got != "/tmp/cfg" {
		t.Errorf("FINADOR_CONFIG_DIR = %q, want /tmp/cfg", got)
	}
	t.Setenv("FINADOR_DATA_DIR", "/tmp/data")
	if got, _ := Ledger(); got != filepath.Join("/tmp/data", LedgerName) {
		t.Errorf("Ledger = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/paths/ -count=1`
Expected: FAIL, the package does not compile ("undefined: Config").

- [ ] **Step 3: Write minimal implementation**

Create `internal/paths/paths.go`:

```go
// Package paths tells finador where its files live.
//
// One layout on every platform - the XDG one - so a path printed in the
// documentation is the path on disk:
//
//	~/.config/finador/config.json       remote configuration
//	~/.cache/finador/<id>.cache         market quote sidecar
//	~/.cache/finador/checkout/          GitHub working copy and its state
//	~/.local/share/finador/finador.fin  the default ledger
//
// Two environment variables move each directory: FINADOR_CONFIG_DIR,
// FINADOR_CACHE_DIR and FINADOR_DATA_DIR name finador's own directory, while
// XDG_CONFIG_HOME, XDG_CACHE_HOME and XDG_DATA_HOME name the base directory it
// sits in. Migrate moves data left at pre-XDG locations.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// LedgerName is the default ledger's file name inside the data directory.
const LedgerName = "finador.fin"

// dir describes how one of finador's directories is resolved, and where it
// used to live before the XDG layout.
type dir struct {
	env      string // FINADOR_*_DIR: finador's directory itself
	xdg      string // XDG_*_HOME: the base directory
	fallback string // base directory relative to $HOME, when XDG_*_HOME is unset
	legacy   string // pre-XDG location of finador's directory, relative to $HOME
}

var (
	configDir = dir{"FINADOR_CONFIG_DIR", "XDG_CONFIG_HOME", ".config", "Library/Application Support/finador"}
	cacheDir  = dir{"FINADOR_CACHE_DIR", "XDG_CACHE_HOME", ".cache", "Library/Caches/finador"}
	dataDir   = dir{"FINADOR_DATA_DIR", "XDG_DATA_HOME", ".local/share", ""}
)

// resolve returns finador's directory: the FINADOR_* override, then the XDG
// base, then $HOME and the conventional fallback.
func (d dir) resolve() (string, error) {
	if p := os.Getenv(d.env); p != "" {
		return p, nil
	}
	if p := stuck[d.env]; p != "" {
		return p, nil // a migration failed: keep reading where the data still is
	}
	if base := os.Getenv(d.xdg); filepath.IsAbs(base) {
		return filepath.Join(base, "finador"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	return filepath.Join(home, filepath.FromSlash(d.fallback), "finador"), nil
}

// Config is where config.json lives.
func Config() (string, error) { return configDir.resolve() }

// Cache is where the quote sidecar and the GitHub working copy live.
func Cache() (string, error) { return cacheDir.resolve() }

// Data is where the default ledger lives.
func Data() (string, error) { return dataDir.resolve() }

// Ledger is the default ledger path, used when neither --db nor FINADOR_DB
// names one.
func Ledger() (string, error) {
	if stuckLedger != "" {
		return stuckLedger, nil
	}
	d, err := Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, LedgerName), nil
}

// stuck records, per FINADOR_*_DIR name, a legacy directory that keeps being
// used because Migrate could not move it; stuckLedger does the same for the
// legacy ledger file. Both are empty in the normal case, and only Migrate
// writes them, before any command runs.
var (
	stuck       = map[string]string{}
	stuckLedger string
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/paths/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/paths
git commit -m "feat(paths): one resolver for finador's directories, XDG on every platform"
```

---

### Task 2: `internal/paths` - migration

**Files:**
- Modify: `internal/paths/paths.go`
- Modify: `internal/paths/paths_test.go`

**Interfaces:**
- Consumes: `dir.resolve`, `stuck`, `stuckLedger` from Task 1.
- Produces: `paths.Migrate(w io.Writer)` - moves pre-XDG data, writes one `migrated <old> -> <new>` line per move on w, and on failure keeps the legacy location for the rest of the process.

- [ ] **Step 1: Write the failing test**

Append to `internal/paths/paths_test.go`:

```go
import (
	"bytes"
	"os"
	"strings"
)

// resetStuck isolates the package state Migrate writes.
func resetStuck(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { stuck, stuckLedger = map[string]string{}, "" })
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateMovesLegacyData(t *testing.T) {
	home := setHome(t)
	resetStuck(t)
	writeFile(t, filepath.Join(home, "Library", "Application Support", "finador", "config.json"), `{"source":"local"}`)
	writeFile(t, filepath.Join(home, "Library", "Caches", "finador", "checkout", "ab.fin"), "cache")
	writeFile(t, filepath.Join(home, ".finador.fin"), "ledger")
	writeFile(t, filepath.Join(home, ".finador.fin.bak"), "backup")

	var log bytes.Buffer
	Migrate(&log)

	for _, want := range []string{
		filepath.Join(home, ".config", "finador", "config.json"),
		filepath.Join(home, ".cache", "finador", "checkout", "ab.fin"),
		filepath.Join(home, ".local", "share", "finador", "finador.fin"),
		filepath.Join(home, ".local", "share", "finador", "finador.fin.bak"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("not migrated: %s (log: %s)", want, log.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".finador.fin")); !os.IsNotExist(err) {
		t.Error("the legacy ledger should have been moved, not copied")
	}
	if n := strings.Count(log.String(), "migrated "); n != 3 {
		t.Errorf("want 3 migration lines, got %d: %s", n, log.String())
	}
}

func TestMigrateLeavesLegacyWhenDestinationExists(t *testing.T) {
	home := setHome(t)
	resetStuck(t)
	writeFile(t, filepath.Join(home, ".finador.fin"), "legacy")
	writeFile(t, filepath.Join(home, ".local", "share", "finador", "finador.fin"), "current")

	var log bytes.Buffer
	Migrate(&log)

	if b, _ := os.ReadFile(filepath.Join(home, ".local", "share", "finador", "finador.fin")); string(b) != "current" {
		t.Error("an existing destination must win")
	}
	if _, err := os.Stat(filepath.Join(home, ".finador.fin")); err != nil {
		t.Error("the legacy file must be left untouched")
	}
	if strings.Contains(log.String(), "migrated ") {
		t.Errorf("nothing should have been migrated: %s", log.String())
	}
}

func TestMigrateSkippedByEnvOverride(t *testing.T) {
	home := setHome(t)
	resetStuck(t)
	t.Setenv("FINADOR_DB", filepath.Join(home, "elsewhere.fin"))
	writeFile(t, filepath.Join(home, ".finador.fin"), "legacy")

	Migrate(&bytes.Buffer{})

	if _, err := os.Stat(filepath.Join(home, ".finador.fin")); err != nil {
		t.Error("FINADOR_DB must skip the ledger migration")
	}
}

func TestMigrateFailureKeepsUsingLegacy(t *testing.T) {
	home := setHome(t)
	resetStuck(t)
	writeFile(t, filepath.Join(home, ".finador.fin"), "legacy")
	// A regular file where the data directory should go: MkdirAll cannot win.
	writeFile(t, filepath.Join(home, ".local"), "not a directory")

	var log bytes.Buffer
	Migrate(&log)

	if !strings.Contains(log.String(), "warning:") {
		t.Errorf("a failed migration must warn: %q", log.String())
	}
	got, err := Ledger()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, ".finador.fin") {
		t.Errorf("Ledger() = %q, want the legacy path that still holds the data", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/paths/ -count=1`
Expected: FAIL, "undefined: Migrate".

- [ ] **Step 3: Write minimal implementation**

Append to `internal/paths/paths.go` (and add `"io"` to the imports):

```go
// Migrate moves data left at pre-XDG locations into the layout the resolvers
// return, and reports each move on w. It runs once, from main, before any
// command opens a file.
//
// Nothing is ever copied: a directory is renamed whole or not at all, so there
// is never a second, divergent ledger. A destination that already exists wins
// and the legacy copy is left untouched, for the user to sort out. When a
// rename fails, the legacy location keeps being used for the rest of the
// process and w gets a warning - data stays reachable, always.
//
// An explicit FINADOR_*_DIR or FINADOR_DB skips the migration it names: the
// user pointed at a location, finador does not tidy it.
func Migrate(w io.Writer) {
	for _, d := range []dir{configDir, cacheDir, dataDir} {
		if d.legacy == "" || os.Getenv(d.env) != "" {
			continue
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		to, err := d.resolve()
		if err != nil {
			continue
		}
		if !move(w, filepath.Join(home, filepath.FromSlash(d.legacy)), to) {
			stuck[d.env] = filepath.Join(home, filepath.FromSlash(d.legacy))
		}
	}
	migrateLedger(w)
}

// migrateLedger moves ~/.finador.fin, and its .bak sidecar, into the data
// directory. The keychain entry is keyed by path, so the wallet password is
// asked once more after the move - said plainly rather than left as a surprise.
func migrateLedger(w io.Writer) {
	if os.Getenv("FINADOR_DB") != "" || os.Getenv(dataDir.env) != "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	legacy := filepath.Join(home, ".finador.fin")
	to, err := Ledger()
	if err != nil || !exists(legacy) || exists(to) {
		return
	}
	if !move(w, legacy, to) {
		stuckLedger = legacy
		return
	}
	_ = os.Rename(legacy+".bak", to+".bak") // absent is the normal case
	fmt.Fprintln(w, "note: the keychain is keyed by path - the wallet password will be asked once more")
}

// move renames from to to, reporting whether the data now lives at to. A
// missing source or an existing destination is success with nothing to do.
func move(w io.Writer, from, to string) bool {
	if from == to || !exists(from) || exists(to) {
		return true
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		fmt.Fprintf(w, "warning: keeping %s (cannot create %s: %v)\n", from, filepath.Dir(to), err)
		return false
	}
	if err := os.Rename(from, to); err != nil {
		fmt.Fprintf(w, "warning: keeping %s (cannot move it to %s: %v)\n", from, to, err)
		return false
	}
	fmt.Fprintf(w, "migrated %s -> %s\n", from, to)
	return true
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/paths/ -count=1 -v`
Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/paths
git commit -m "feat(paths): migrate pre-XDG data once, by rename, never by copy"
```

---

### Task 3: wire every caller to `internal/paths`

**Files:**
- Modify: `internal/store/cache.go:16-33`
- Modify: `internal/remote/config.go:42-53`
- Modify: `internal/remote/sync.go:44-51,60-80`
- Modify: `internal/cli/cli.go:207-213`
- Modify: `cmd/finador/main.go`
- Modify: `docs/FORMAT.md` (§1 default path, §7 sidecar directory)
- Modify: `README.md` (paths: lines around 41, 323, 381, 632-680, 795-846)
- Modify: `CLAUDE.md` (architecture diagram)

**Interfaces:**
- Consumes: `paths.Config/Cache/Data/Ledger/Migrate` from Tasks 1-2.
- Produces: `remote.WorkingCopyPath(gh GitHub) (string, error)` - the working copy path for a remote, without building a Syncer (Task 4 needs it).

- [ ] **Step 1: Write the failing test**

Add to `internal/store/cache_test.go`:

```go
// TestCachePathUsesTheFinadorDirectory pins the sidecar's location: the env
// override names finador's own directory, nothing is appended to it.
func TestCachePathUsesTheFinadorDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CACHE_DIR", dir)
	path := filepath.Join(t.TempDir(), "t.fin")
	f, err := Create(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SaveCache(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".cache") {
		t.Fatalf("want one .cache file directly in %s, got %v (%v)", dir, entries, err)
	}
}
```

Add the imports `os`, `path/filepath`, `strings` to that test file if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestCachePathUsesTheFinadorDirectory -count=1`
Expected: FAIL - the file lands in `<dir>/finador/<id>.cache`, so the directory holds one entry named `finador`, not a `.cache` file.

- [ ] **Step 3: Write minimal implementation**

`internal/store/cache.go` - delete `cacheDir` and use the resolver:

```go
// cachePath derives the sidecar path from the file id: deterministic and stable
// across machines, physically outside any git repo.
func (f *File) cachePath() (string, error) {
	dir, err := paths.Cache()
	if err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(f.hdr.ID)
	return filepath.Join(dir, id+".cache"), nil
}
```

`internal/remote/config.go` - delete `configDir` and export the path:

```go
// ConfigPath is where finador reads and writes config.json. It is exported so
// `finador config get` can name the file it is reporting on.
func ConfigPath() (string, error) {
	dir, err := paths.Config()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}
```

Replace the two internal `configPath()` calls in `Load` and `Save` with `ConfigPath()`.

`internal/remote/sync.go` - delete `cacheDir`, add the exported path helper, and have `NewSyncer` use it:

```go
// WorkingCopyPath is where the working copy of gh lives. The path is derived
// from a stable hash of owner/repo/path, so it is the same on every run.
func WorkingCopyPath(gh GitHub) (string, error) {
	base, err := paths.Cache()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}
	return filepath.Join(base, "checkout", hashKey(gh)+".fin"), nil
}

func NewSyncer(b Backend, gh GitHub, readPullAfter time.Duration) (*Syncer, error) {
	copyPath, err := WorkingCopyPath(gh)
	if err != nil {
		return nil, err
	}
	if readPullAfter <= 0 {
		readPullAfter = time.Hour
	}
	return &Syncer{
		backend:       b,
		gh:            gh,
		copyPath:      copyPath,
		statePath:     strings.TrimSuffix(copyPath, ".fin") + ".state.json",
		readPullAfter: readPullAfter,
		clock:         time.Now,
	}, nil
}
```

Keep the rest of `NewSyncer` unchanged (whatever follows the struct literal in the current code). The `statePath` line needs `"strings"` in the imports, and `"path/filepath"` stays for `WorkingCopyPath`.

`internal/cli/cli.go`:

```go
// defaultDB is the ledger a command opens when --db is absent.
func defaultDB() string {
	if p := os.Getenv("FINADOR_DB"); p != "" {
		return p
	}
	p, err := paths.Ledger()
	if err != nil {
		return paths.LedgerName // no home directory: the current one will do
	}
	return p
}
```

`cmd/finador/main.go`:

```go
func main() {
	paths.Migrate(os.Stderr) // once, before any command opens a file
	if err := cli.New().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "finador:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run the whole suite**

Run: `go test ./... -count=1`
Expected: PASS everywhere. `internal/remote` and `internal/cli` tests set `FINADOR_CACHE_DIR`/`FINADOR_CONFIG_DIR` to temp dirs and assert no directory layout, so they must pass unchanged. If one fails on a hard-coded `finador` segment, fix the test to match the new semantics (the env var names finador's own directory).

- [ ] **Step 5: Update the docs**

- `docs/FORMAT.md` §7: replace the `os.UserCacheDir()/finador/` bullet with:
  `**Directory**: `~/.cache/finador/` (`$XDG_CACHE_HOME/finador` when set), on every platform. The environment variable **`FINADOR_CACHE_DIR`** names that directory outright (used by tests).`
- `docs/FORMAT.md` §1: the reference default path becomes `~/.local/share/finador/finador.fin`.
- `README.md`: every `~/.finador.fin` becomes `~/.local/share/finador/finador.fin` (lines ~41, ~323, ~381, ~634, ~644, ~646, ~673, ~795, ~818, ~821). In the paths/config section, add the layout and the migration note:

```
~/.config/finador/config.json        # remote configuration (XDG_CONFIG_HOME)
~/.cache/finador/                    # quote cache + GitHub working copy (XDG_CACHE_HOME)
~/.local/share/finador/finador.fin   # default ledger (XDG_DATA_HOME, or --db / FINADOR_DB)

# Upgrading from a version that used ~/.finador.fin or ~/Library: finador moves
# them here on its next run, and says so. The password is asked once more
# afterwards (the keychain entry is keyed by path).
```

- `CLAUDE.md`: add `paths` to the architecture block, as a leaf every layer may use:

```
             ├→  keyring                      (passwords: env → Keychain → prompt)
             └→  paths                        (XDG dirs + one-time migration)
```

- [ ] **Step 6: Verify end to end against the real binary**

```sh
make build
export FINADOR_PASSWORD=pw
HOME=$(mktemp -d) bin/finador --offline --no-keychain init
HOME=$(mktemp -d) bin/finador --offline --no-keychain --help | grep -- --db
```

Expected: `init` creates `<temp home>/.local/share/finador/finador.fin` and prints that path; `--help` shows it as the `--db` default.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(paths)!: XDG layout everywhere, with one-time migration of existing data"
```

---

### Task 4: `--db` help shows the file finador will actually open

**Files:**
- Modify: `internal/cli/cli.go:74,207-213`
- Test: `internal/cli/remote_test.go`

**Interfaces:**
- Consumes: `remote.WorkingCopyPath` (Task 3), `paths.Ledger` (Task 1).
- Produces: nothing new; `a.dbPath` now always equals the ledger the command opens, which Task 5 prints.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/remote_test.go` (it already has the helper that points `FINADOR_CONFIG_DIR`/`FINADOR_CACHE_DIR` at temp dirs - reuse it; if it is not exported inside the file, copy its two `t.Setenv` lines):

```go
// TestHelpShowsTheWorkingCopyAsDbDefault: in GitHub mode the file commands
// open is the working copy, so that is what --db must advertise.
func TestHelpShowsTheWorkingCopyAsDbDefault(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("FINADOR_CONFIG_DIR", t.TempDir())
	t.Setenv("FINADOR_CACHE_DIR", cacheDir)
	t.Setenv("FINADOR_DB", "")
	if err := remote.Save(remote.Config{
		Source: "github",
		GitHub: &remote.GitHub{Owner: "zephyr", Repo: "ledger", Path: "finador.fin", Branch: "master"},
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := cli.New()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cacheDir, "checkout")
	if !strings.Contains(out.String(), want) {
		t.Errorf("--db default should name the working copy under %s:\n%s", want, out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestHelpShowsTheWorkingCopyAsDbDefault -count=1`
Expected: FAIL - the help shows `~/.local/share/finador/finador.fin`.

- [ ] **Step 3: Write minimal implementation**

In `internal/cli/cli.go`:

```go
// defaultDB is the ledger a command opens when --db is absent: FINADOR_DB, the
// GitHub working copy when config.json selects it, otherwise the local default.
// The help text shows this value, so what it advertises is the file finador
// really opens - in GitHub mode that is the working copy, not a local path
// nothing ever wrote to.
func defaultDB() string {
	if p := os.Getenv("FINADOR_DB"); p != "" {
		return p
	}
	if cfg, err := remote.Load(); err == nil && cfg.Source == "github" && cfg.GitHub != nil {
		if p, err := remote.WorkingCopyPath(*cfg.GitHub); err == nil {
			return p
		}
	}
	p, err := paths.Ledger()
	if err != nil {
		return paths.LedgerName
	}
	return p
}
```

And the flag's usage, at `cli.go:74`:

```go
root.PersistentFlags().StringVar(&a.dbPath, "db", defaultDB(), "encrypted data file - naming one forces local mode")
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS. `merge` is unaffected: it refuses to run in GitHub mode, so `a.dbPath` there is still the local default.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "fix(cli): --db advertises the file finador actually opens"
```

---

### Task 5: `config get` shows the whole configuration

**Files:**
- Modify: `internal/cli/config.go` (whole file)
- Test: `internal/cli/cli_test.go`
- Modify: `README.md` (config section)

**Interfaces:**
- Consumes: `remote.ConfigPath` (Task 3), `a.dbPath` as the effective ledger path (Task 4).
- Produces: `configKeys []configKey` with fields `Name, Default, Doc string` - the single table of settings finador understands.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/cli_test.go`:

```go
func TestConfigGetShowsPathsDefaultsAndUnknownKeys(t *testing.T) {
	t.Setenv("FINADOR_CONFIG_DIR", t.TempDir())
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "config", "set", "risk-free", "2.5%")
	run(t, db, "config", "set", "made-up", "x")

	out := run(t, db, "config", "get")
	for _, want := range []string{
		"# ledger: " + db,
		"# config: ",
		"(source = local)",
		"risk-free",
		"2.5%",
		"currency",
		"EUR",
		"# default",
		"made-up",
		"# unknown",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config get: %q missing from:\n%s", want, out)
		}
	}
	// the ledger line comes first, as a comment
	if !strings.HasPrefix(out, "# ledger: ") {
		t.Errorf("config get must start with the ledger path:\n%s", out)
	}
	// a single key still prints one raw value, now the effective one
	if got := run(t, db, "config", "get", "keychain-ttl"); strings.TrimSpace(got) != "12h" {
		t.Errorf("config get keychain-ttl = %q, want the default 12h", got)
	}
	// an unknown key is set, with a warning
	out = run(t, db, "config", "set", "keychain_ttl", "8h")
	if !strings.Contains(out, "unknown config key") {
		t.Errorf("config set should warn on an unknown key:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestConfigGetShowsPathsDefaultsAndUnknownKeys -count=1`
Expected: FAIL - no comment lines, no defaults, no warning.

- [ ] **Step 3: Write minimal implementation**

Rewrite `internal/cli/config.go`:

```go
package cli

import (
	"fmt"
	"maps"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/remote"
)

// configKey documents one setting the ledger understands: what it means, and
// what finador does when it is unset. This table is the single source `config
// get` and the README read from - a key finador honours but does not list here
// is a key nobody can discover.
type configKey struct {
	Name    string
	Default string
	Doc     string
}

var configKeys = []configKey{
	{"currency", "EUR", "display currency"},
	{"default-account", "", "envelope used when a command omits --account"},
	{"keychain-ttl", "12h", "how long the wallet password stays in the keychain"},
	{"risk-free", "0%", "annualized risk-free rate for Sharpe and Sortino"},
}

// configDefault returns the documented default of a key, and whether finador
// knows the key at all.
func configDefault(name string) (string, bool) {
	for _, k := range configKeys {
		if k.Name == name {
			return k.Default, true
		}
	}
	return "", false
}

func configCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Settings: default-account, keychain-ttl, risk-free…"}
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config key",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, known := configDefault(args[0]); !known {
				// Never refuse: a newer finador may read keys this one ignores.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: unknown config key %q (see `finador config get`)\n", args[0])
			}
			return a.mutate(func(b *domain.Book) error {
				b.Config[args[0]] = args[1]
				return nil
			})
		},
	}
	get := &cobra.Command{
		Use:   "get [key]",
		Short: "Show the whole configuration: paths, values in effect, defaults",
		Example: "  finador config get                 # everything, defaults included\n" +
			"  finador config get keychain-ttl    # one raw value, for scripts",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 1 {
				value, ok := f.Book.Config[args[0]]
				if !ok {
					value, _ = configDefault(args[0])
				}
				fmt.Fprintln(out, value)
				return nil
			}

			// Where the settings live, before what they say.
			fmt.Fprintf(out, "# ledger: %s\n", a.dbPath)
			cfgPath, perr := remote.ConfigPath()
			cfg, cerr := remote.Load()
			if perr == nil {
				source := cfg.Source
				if cerr != nil || source == "" {
					source = "local"
				}
				fmt.Fprintf(out, "# config: %s   (source = %s)\n", cfgPath, source)
			}

			w := tabwriter.NewWriter(out, 2, 4, 1, ' ', 0)
			for _, k := range configKeys {
				value, set := f.Book.Config[k.Name]
				comment := ""
				if !set {
					value, comment = k.Default, "# default"
					if value == "" {
						comment = "# unset"
					}
				}
				fmt.Fprintf(w, "%s\t= %s\t%s\n", k.Name, value, comment)
			}
			for _, name := range slices.Sorted(maps.Keys(f.Book.Config)) {
				if _, known := configDefault(name); !known {
					fmt.Fprintf(w, "%s\t= %s\t# unknown\n", name, f.Book.Config[name])
				}
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(set, get)
	return cmd
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS.

- [ ] **Step 5: Update the README**

In the settings section, replace the `config get` example with the real output and document the table:

```sh
finador config get              # paths first, then every setting and its default
# ledger: /home/you/.local/share/finador/finador.fin
# config: /home/you/.config/finador/config.json   (source = local)
currency        = EUR    # default
default-account =        # unset
keychain-ttl    = 12h    # default
risk-free       = 2.5%

finador config get keychain-ttl # one raw value, for scripts
finador config set risk-free 2.5%
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli README.md
git commit -m "feat(config): config get shows paths, effective values and defaults"
```

---

### Task 6: `Scope.Only` - the asset filter, in one place

**Files:**
- Modify: `internal/portfolio/scope.go:26-34,100-123,156-185`
- Test: `internal/portfolio/scope_test.go` (create if absent)

**Interfaces:**
- Consumes: nothing.
- Produces: `portfolio.Scope.Only map[domain.AssetID]bool` - when non-nil, the only assets in scope; cash is out. Task 7 sets it.

- [ ] **Step 1: Write the failing test**

Create or extend `internal/portfolio/scope_test.go`:

```go
package portfolio

import (
	"testing"

	"finador/internal/domain"
)

func TestScopeOnlyFiltersAssetsAndDropsCash(t *testing.T) {
	acc := &domain.Account{ID: "acc1", Name: "PEA Zephyr"}
	kept := &domain.Asset{ID: "a1", Kind: domain.Security, Name: "CW8", Currency: domain.EUR}
	dropped := &domain.Asset{ID: "a2", Kind: domain.Security, Name: "AAPL", Currency: domain.EUR}

	s := Scope{Kind: All, Label: "portfolio", Only: map[domain.AssetID]bool{kept.ID: true}}
	if !s.HasAsset(acc, kept) {
		t.Error("an asset named by --asset must stay in scope")
	}
	if s.HasAsset(acc, dropped) {
		t.Error("an asset not named by --asset must leave the scope")
	}
	if s.HasCash(acc) {
		t.Error("an asset filter excludes cash: cash is not an asset")
	}

	// Excluded still wins over Only: the two can name the same asset.
	s.Excluded = map[domain.AssetID]bool{kept.ID: true}
	if s.HasAsset(acc, kept) {
		t.Error("--exclude must win over --asset")
	}

	// The envelope rows of a tree inherit the filter.
	env := EnvelopeScope(Scope{Kind: All, Only: map[domain.AssetID]bool{kept.ID: true}}, acc)
	if env.HasAsset(acc, dropped) {
		t.Error("EnvelopeScope must carry Only through")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/portfolio/ -run TestScopeOnly -count=1`
Expected: FAIL, "unknown field Only in struct literal".

- [ ] **Step 3: Write minimal implementation**

In `internal/portfolio/scope.go`, add the field to `Scope`:

```go
	Excluded map[domain.AssetID]bool // assets removed from the scope (throwaway, CLI --exclude)
	Only     map[domain.AssetID]bool // when non-nil, the only assets in scope (throwaway, CLI --asset)
```

Both predicates, and nowhere else:

```go
func (s Scope) hasAsset(acc *domain.Account, asset *domain.Asset) bool {
	if s.Excluded[asset.ID] {
		return false
	}
	if s.Only != nil && !s.Only[asset.ID] {
		return false
	}
	switch s.Kind {
	... unchanged ...
	}
}

func (s Scope) hasCash(acc *domain.Account) bool {
	if s.Only != nil {
		return false // an asset filter excludes cash: cash is not an asset
	}
	switch s.Kind {
	... unchanged ...
	}
}
```

And carry `Only` through the derived scopes in `EnvelopeScope`, exactly where `Excluded` is already carried - the four `Scope{...}` literals it builds gain `Only: s.Only`. `IntersectScope` and `PairScope` need nothing: they are built from lines `FilterScope` has already filtered.

Update the `Scope` doc comment to mention the two throwaway filters.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/portfolio/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/portfolio
git commit -m "feat(portfolio): Scope.Only, the asset filter both value and flows obey"
```

---

### Task 7: `--asset` on value, perf and chart

**Files:**
- Modify: `internal/cli/helpers.go:12-38`
- Modify: `internal/cli/value.go:17-106`
- Modify: `internal/cli/perf.go:16-159`
- Modify: `internal/cli/chart.go:15-91`
- Test: `internal/cli/cli_test.go`
- Modify: `README.md` (value/perf/chart recipes)

**Interfaces:**
- Consumes: `portfolio.Scope.Only` (Task 6).
- Produces: `resolveScope(b *domain.Book, ref, label string, exclude, only []string) (portfolio.Scope, error)` - one more parameter, in the same shape as `exclude`.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/cli_test.go`:

```go
// TestAssetFilterOnReadCommands: --asset narrows value/perf/chart to one or
// more assets, compounded into a single figure, and composes with a [scope].
func TestAssetFilterOnReadCommands(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "account", "add", "CTO Meridia")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--name", "World", "--group", "equities/world")
	run(t, db, "asset", "add", "AAPL", "--alias", "aapl", "--name", "Apple", "--group", "equities/us")
	run(t, db, "buy", "cw8", "10", "@100", "--account", "PEA Zephyr", "--date", "2026-01-05")
	run(t, db, "buy", "aapl", "10", "@50", "--account", "CTO Meridia", "--date", "2026-01-05")
	run(t, db, "cash", "set", "PEA Zephyr", "1000")

	// one asset: its value only, cash excluded
	out := run(t, db, "value", "--asset", "cw8", "--gross")
	if !strings.Contains(out, "1000.00 EUR") {
		t.Errorf("value --asset cw8 should total the 10x100 position:\n%s", out)
	}
	if strings.Contains(out, "cash") {
		t.Errorf("an asset filter must exclude cash:\n%s", out)
	}
	// two assets: one compounded figure
	out = run(t, db, "value", "--asset", "cw8,aapl", "--gross")
	if !strings.Contains(out, "1500.00 EUR") {
		t.Errorf("value --asset cw8,aapl should total both positions:\n%s", out)
	}
	// intersection with a [scope]: the CTO holds no CW8
	out = run(t, db, "value", "CTO Meridia", "--asset", "cw8", "--gross")
	if !strings.Contains(out, "TOTAL") || strings.Contains(out, "1000.00") {
		t.Errorf("--asset must intersect the [scope], not replace it:\n%s", out)
	}
	// the plural spelling is accepted, and perf/chart take the flag too
	run(t, db, "perf", "--assets", "cw8")
	// Value() and Series() must agree pointwise, filter included: the chart's
	// last point is the value of the same scope.
	if out := run(t, db, "chart", "--asset", "cw8"); !strings.Contains(out, "last point: 1000.00 EUR") {
		t.Errorf("chart --asset cw8 should end on the same figure value prints:\n%s", out)
	}
	// an unknown asset is an error, named as such
	if _, err := tryRun(t, db, "value", "--asset", "nope"); err == nil {
		t.Error("--asset nope should fail")
	}
}
```

The `--assets` spelling needs Task 8's normalizer; if Task 8 is not merged yet, run this line with `--asset` and restore the plural in Task 8.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestAssetFilterOnReadCommands -count=1`
Expected: FAIL, "unknown flag: --asset".

- [ ] **Step 3: Write minimal implementation**

`internal/cli/helpers.go` - one more filter, resolved like `--exclude`:

```go
// resolveScope resolves the [scope]/--label/--asset/--exclude quadruple every
// read command shares: an empty ref is the whole portfolio, --label restricts
// to labelled positions, --asset keeps only the named assets (and drops cash),
// --exclude prunes assets. --asset and --exclude narrow whatever the scope
// argument selected - they compose with it rather than replacing it.
func resolveScope(b *domain.Book, ref, label string, exclude, only []string) (portfolio.Scope, error) {
	if ref != "" && label != "" {
		return portfolio.Scope{}, fmt.Errorf("use either a [scope] argument or --label, not both")
	}
	var scope portfolio.Scope
	var err error
	if label != "" {
		scope, err = portfolio.LabelScope(b, label)
	} else {
		scope, err = portfolio.ParseScope(b, ref)
	}
	if err != nil {
		return portfolio.Scope{}, err
	}
	kept, names, err := parseAssetRefs(b, "--asset", only)
	if err != nil {
		return portfolio.Scope{}, err
	}
	if len(kept) > 0 {
		scope.Only = kept
		list := strings.Join(names, ", ")
		if ref == "" && label == "" {
			scope.Label = list
		} else {
			scope.Label += " › " + list
		}
	}
	excluded, _, err := parseAssetRefs(b, "--exclude", exclude)
	if err != nil {
		return portfolio.Scope{}, err
	}
	if len(excluded) > 0 {
		scope.Excluded = excluded
		scope.Label += " (excluding " + strings.Join(exclude, ",") + ")"
	}
	return scope, nil
}

// parseAssetRefs resolves a comma-or-repeated list of asset references into a
// set of IDs plus their canonical names, for the scope label. flag names the
// flag, so an unresolvable reference blames the right one.
func parseAssetRefs(b *domain.Book, flag string, refs []string) (map[domain.AssetID]bool, []string, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	out := map[domain.AssetID]bool{}
	var names []string
	for _, chunk := range refs {
		for _, ref := range strings.Split(chunk, ",") {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			asset, err := b.Asset(ref)
			if err != nil {
				return nil, nil, fmt.Errorf("%s %s: %w", flag, ref, err)
			}
			if !out[asset.ID] {
				names = append(names, asset.Name)
			}
			out[asset.ID] = true
		}
	}
	return out, names, nil
}
```

Delete `parseExclusions`: `parseAssetRefs` replaces it. (Check for other callers first: `grep -rn parseExclusions internal/`.)

In each of `value.go`, `perf.go` and `chart.go`: add `var only []string` beside the existing `exclude`, pass it to `resolveScope(b, ref, label, exclude, only)`, and register the flag with the same wording in the three files:

```go
	cmd.Flags().StringArrayVar(&only, "asset", nil, "keep only this asset (repeatable or comma list); several are compounded into one figure")
```

Add one example line each:

- `value.go` Example: `"  finador value --asset cw8,aapl   # only those two, compounded\n"`
- `perf.go` Example: `"  finador perf --asset cw8\n"`
- `chart.go` Example: `"  finador chart --asset cw8,aapl"`

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/ ./internal/portfolio/ -count=1`
Expected: PASS.

- [ ] **Step 5: Verify end to end**

```sh
make build
export FINADOR_PASSWORD=pw FINADOR_CACHE_DIR=$(mktemp -d) FINADOR_DB=/tmp/t7.fin
rm -f /tmp/t7.fin
bin/finador --offline --no-keychain init
bin/finador --offline --no-keychain account add "PEA Zephyr"
bin/finador --offline --no-keychain asset add CW8.PA --alias cw8
bin/finador --offline --no-keychain buy cw8 10 @100 --date 2026-01-05
bin/finador --offline --no-keychain value --asset cw8
bin/finador --offline --no-keychain perf --asset cw8
```

Expected: `value --asset cw8` prints a scope label of `World`-ish (the asset name) and 1000.00 EUR, with no cash line.

- [ ] **Step 6: Update the README**

Add `--asset` to the value/perf/chart flag tables and one recipe:

```sh
finador value --asset cw8            # one position, all envelopes, cash excluded
finador perf --asset cw8,aapl        # the two together, as one portfolio
finador perf "PEA Zephyr" --asset cw8 # inside one envelope
```

- [ ] **Step 7: Commit**

```bash
git add internal/cli internal/portfolio README.md
git commit -m "feat(cli): --asset on value, perf and chart"
```

---

### Task 8: plural command names and plural flags

**Files:**
- Modify: `internal/cli/cli.go:59-151`
- Modify: `internal/cli/account.go:15`, `asset.go:19`, `label.go:16`, `tx.go:17`, `value.go:22`, `perf.go:21`, `chart.go:21`
- Test: `internal/cli/cli_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: the commands built in `cli.New()`.
- Produces: nothing other tasks use.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/cli_test.go`:

```go
// TestPluralAliases: the noun commands answer to their plural, and a plural
// flag name reaches its singular flag.
func TestPluralAliases(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8")
	run(t, db, "buy", "cw8", "10", "@100", "--date", "2026-01-05")

	for _, args := range [][]string{
		{"accounts", "list"},
		{"assets", "list"},
		{"labels", "list"},
		{"txs", "list"},
		{"transactions", "list"},
		{"values", "--gross"},
		{"perfs"},
		{"charts"},
	} {
		if _, err := tryRun(t, db, args...); err != nil {
			t.Errorf("finador %s: %v", strings.Join(args, " "), err)
		}
	}
	if _, err := tryRun(t, db, "value", "--assets", "cw8", "--gross"); err != nil {
		t.Errorf("--assets should reach the --asset flag: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestPluralAliases -count=1`
Expected: FAIL, `unknown command "accounts"`.

- [ ] **Step 3: Write minimal implementation**

Add `Aliases` to each noun command, next to its `Use`:

| file:line | add |
|---|---|
| `account.go:15` | `Aliases: []string{"accounts"},` |
| `asset.go:19` | `Aliases: []string{"assets"},` |
| `label.go:16` | `Aliases: []string{"labels"},` |
| `tx.go:17` | `Aliases: []string{"txs", "transactions"},` |
| `value.go:22` | `Aliases: []string{"values"},` |
| `perf.go:21` | `Aliases: []string{"perfs"},` |
| `chart.go:21` | `Aliases: []string{"charts"},` |

(The verb families - buy, sell, deposit, withdraw, init, refresh, serve - keep no alias: they have no sensible plural.)

And in `cli.New()`, right after the root command is built, one rule for flags:

```go
	// A plural flag name is the same flag: --assets is --asset. Stated once
	// here, inherited by every subcommand's flag set.
	root.SetGlobalNormalizationFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		switch name {
		case "assets":
			name = "asset"
		case "excludes":
			name = "exclude"
		case "what-ifs":
			name = "what-if"
		}
		return pflag.NormalizedName(name)
	})
```

Import `"github.com/spf13/pflag"` in `cli.go` (already an indirect dependency of cobra; check `go.mod` lists it as indirect and promote it with `go mod tidy` if the linter asks).

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/ -count=1`
Expected: PASS.

- [ ] **Step 5: Update the README**

In the command reference, one line:

```
# Plural spellings work everywhere: `finador perfs`, `finador accounts list`,
# `finador value --assets cw8` are the singular commands and flags.
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli README.md
git commit -m "feat(cli): plural command names and plural flag spellings"
```

---

### Task 9: decisions, full gate, push

**Files:**
- Modify: `docs/superpowers/DECISIONS.md`
- Verify: everything

- [ ] **Step 1: Write the decision entries**

Append two entries in the file's existing French style and numbering (read the last entry first to continue the sequence, D30 and D31 if D29 is last):

```markdown
## D30 - Chemins XDG partout, migration par rename

Les répertoires macOS (`~/Library/...`) contredisaient une doc qui annonçait
déjà `~/.config/finador/config.json`. `internal/paths` résout les trois
répertoires (config, cache, données) en XDG sur toutes les plateformes, et le
ledger par défaut passe de `~/.finador.fin` à
`~/.local/share/finador/finador.fin`.

La migration est appelée une fois depuis `main`, jamais depuis `cli.New()` :
un binaire de test ne déplace donc jamais les vraies données du développeur.
Elle ne copie jamais - `os.Rename` du répertoire entier, ou rien - pour qu'il
ne puisse pas exister deux ledgers divergents. Si la destination existe déjà,
elle gagne et l'ancien emplacement est laissé intact. Si le rename échoue,
l'ancien emplacement continue d'être utilisé pour ce run, avec un
avertissement : les données restent toujours joignables. Effet de bord assumé
et annoncé : la clé du trousseau est indexée par chemin, donc le mot de passe
est redemandé une fois après le déplacement.

## D31 - `--asset` est un filtre de Scope, pas un ScopeKind

`value/perf/chart --asset` ajoute `Scope.Only`, jumeau de `Excluded`, plutôt
qu'un sixième `ScopeKind`. Les deux prédicats `hasAsset`/`hasCash` sont le seul
point de filtrage partagé par la valorisation, la série (donc les flux), les
arbres et le web : un champ y suffit pour que le filtre atteigne tout d'un
coup, sans risquer de dissocier valeur et flux (invariant D29). `Only` exclut
le cash - le cash n'est pas un asset - et se compose avec `[scope]` et
`--label` au lieu de les remplacer.
```

- [ ] **Step 2: Run the full gate**

Run: `make check`
Expected: fmt-check, vet, lint, test and race all pass.

- [ ] **Step 3: Verify the migration once, for real, on a copy**

```sh
make build
FAKE=$(mktemp -d)
mkdir -p "$FAKE/Library/Application Support/finador" "$FAKE/Library/Caches/finador"
echo '{"source":"local"}' > "$FAKE/Library/Application Support/finador/config.json"
printf 'x' > "$FAKE/Library/Caches/finador/dead.cache"
HOME=$FAKE FINADOR_PASSWORD=pw bin/finador --no-keychain --offline init
ls -R "$FAKE/.config" "$FAKE/.cache" "$FAKE/.local"
```

Expected: `config.json` under `$FAKE/.config/finador/`, `dead.cache` under `$FAKE/.cache/finador/`, the new ledger under `$FAKE/.local/share/finador/finador.fin`, and two `migrated … -> …` lines on stderr.

- [ ] **Step 4: Check the cross-implementation gate is untouched**

The on-disk format did not change, so `docs/format-testdata/sample.ledger` and the Android client are unaffected. Confirm with:

Run: `go test ./internal/store/ -run TestFormat -count=1`
Expected: PASS.

- [ ] **Step 5: Commit and push**

```bash
git add docs/superpowers/DECISIONS.md
git commit -m "docs(decisions): D30 XDG paths + migration, D31 --asset as a Scope filter"
git push origin master
```
