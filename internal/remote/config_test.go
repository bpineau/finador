package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CONFIG_DIR", dir)

	want := Config{
		Source: "github",
		GitHub: &GitHub{
			Owner:  "alice",
			Repo:   "finador-data",
			Path:   "portfolio.fin",
			Branch: "main",
		},
		ReadPullAfter: "30m",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Source != want.Source {
		t.Errorf("Source: got %q, want %q", got.Source, want.Source)
	}
	if got.GitHub == nil {
		t.Fatal("GitHub section missing after round-trip")
	}
	if got.GitHub.Owner != want.GitHub.Owner {
		t.Errorf("Owner: got %q, want %q", got.GitHub.Owner, want.GitHub.Owner)
	}
	if got.GitHub.Repo != want.GitHub.Repo {
		t.Errorf("Repo: got %q, want %q", got.GitHub.Repo, want.GitHub.Repo)
	}
	if got.GitHub.Path != want.GitHub.Path {
		t.Errorf("Path: got %q, want %q", got.GitHub.Path, want.GitHub.Path)
	}
	if got.GitHub.Branch != want.GitHub.Branch {
		t.Errorf("Branch: got %q, want %q", got.GitHub.Branch, want.GitHub.Branch)
	}
	if got.ReadPullAfter != want.ReadPullAfter {
		t.Errorf("ReadPullAfter: got %q, want %q", got.ReadPullAfter, want.ReadPullAfter)
	}
}

func TestLoadMissingFileReturnsLocalDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CONFIG_DIR", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load of missing file: %v", err)
	}
	if c.Source != "local" {
		t.Errorf("Source: got %q, want \"local\"", c.Source)
	}
	if c.GitHub != nil {
		t.Errorf("GitHub should be nil for local default, got %+v", c.GitHub)
	}
}

func TestReadPullDurationDefault(t *testing.T) {
	c := Config{}
	if d := c.ReadPullDuration(); d != time.Hour {
		t.Errorf("empty ReadPullAfter: got %v, want 1h", d)
	}
}

func TestReadPullDurationInvalid(t *testing.T) {
	c := Config{ReadPullAfter: "not-a-duration"}
	if d := c.ReadPullDuration(); d != time.Hour {
		t.Errorf("invalid ReadPullAfter: got %v, want 1h", d)
	}
}

func TestReadPullDurationParse(t *testing.T) {
	c := Config{ReadPullAfter: "30m"}
	if d := c.ReadPullDuration(); d != 30*time.Minute {
		t.Errorf("ReadPullDuration: got %v, want 30m", d)
	}
}

func TestConfigDirEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CONFIG_DIR", dir)

	got, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if want := filepath.Join(dir, "config.json"); got != want {
		t.Errorf("ConfigPath: got %q, want %q", got, want)
	}
}

func TestValidateBranchDefaultsToMaster(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CONFIG_DIR", dir)

	c := Config{
		Source: "github",
		GitHub: &GitHub{
			Owner: "alice",
			Repo:  "repo",
			Path:  "data.fin",
			// Branch intentionally empty
		},
	}
	if err := Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.GitHub.Branch != "master" {
		t.Errorf("Branch: got %q, want \"master\"", got.GitHub.Branch)
	}
}

func TestValidateUnknownSourceReturnsError(t *testing.T) {
	_, err := validate(Config{Source: "s3"})
	if err == nil {
		t.Fatal("expected error for unknown source, got nil")
	}
}

func TestValidateGithubMissingFieldsReturnsError(t *testing.T) {
	_, err := validate(Config{Source: "github", GitHub: &GitHub{Owner: "x"}})
	if err == nil {
		t.Fatal("expected error for missing repo/path")
	}
}

func TestSaveAtomicMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CONFIG_DIR", dir)

	c := Config{
		Source: "github",
		GitHub: &GitHub{Owner: "a", Repo: "b", Path: "c.fin", Branch: "main"},
	}
	if err := Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path, _ := ConfigPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// 0600 = rw-------
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode: got %o, want 600", mode)
	}
}

// validate is the single gate on a hand-edited config: it fills the defaults
// and names the missing field instead of letting a half-configured remote
// reach the network.
func TestValidateTable(t *testing.T) {
	full := func(f func(*GitHub)) Config {
		gh := GitHub{Owner: "alice", Repo: "data", Path: "portfolio.fin", Branch: "main"}
		f(&gh)
		return Config{Source: "github", GitHub: &gh}
	}
	cases := []struct {
		name string
		in   Config
		want string // "" = valid
	}{
		{"empty is local", Config{}, ""},
		{"local needs nothing", Config{Source: "local"}, ""},
		{"unknown source", Config{Source: "s3"}, "unknown source"},
		{"github without a section", Config{Source: "github"}, "requires a [github] section"},
		{"github without an owner", full(func(g *GitHub) { g.Owner = "" }), "github.owner is required"},
		{"github without a repo", full(func(g *GitHub) { g.Repo = "" }), "github.repo is required"},
		{"github without a path", full(func(g *GitHub) { g.Path = "" }), "github.path is required"},
		{"github is otherwise complete", full(func(*GitHub) {}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := validate(c.in)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("validate = %v, want it accepted", err)
			case c.want == "":
				if got.Source == "" {
					t.Error("validate left the source empty, want the local default")
				}
			case err == nil || !strings.Contains(err.Error(), c.want):
				t.Fatalf("validate = %v, want %q", err, c.want)
			}
		})
	}
	// Save applies the same gate before touching the disk.
	t.Setenv("FINADOR_CONFIG_DIR", t.TempDir())
	if err := Save(Config{Source: "s3"}); err == nil {
		t.Error("Save should refuse an invalid config")
	}
	path, _ := ConfigPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a refused Save must not write a file")
	}
}

// A config file that is not readable as JSON is an error, not a silent
// fallback to local mode: the user asked for a remote and must be told the
// file is broken instead of quietly writing to a different place.
func TestLoadRejectsCorruptedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Errorf("Load(corrupt) = %v, want a parse error", err)
	}
	// syntactically fine but incomplete: the validation gate speaks up
	if err := os.WriteFile(path, []byte(`{"source":"github"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "[github] section") {
		t.Errorf("Load(incomplete) = %v, want the validation error", err)
	}
	// unreadable (a directory where the file should be)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Errorf("Load(unreadable) = %v, want a read error", err)
	}
}

// With no home directory and no override there is nowhere to read or write the
// config: reported, never guessed.
func TestConfigPathNeedsAHome(t *testing.T) {
	t.Setenv("FINADOR_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if _, err := ConfigPath(); err == nil {
		t.Error("ConfigPath should fail with no home and no override")
	}
	if _, err := Load(); err == nil {
		t.Error("Load should fail with no home and no override")
	}
	if err := Save(Config{Source: "local"}); err == nil {
		t.Error("Save should fail with no home and no override")
	}
}
