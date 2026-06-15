package remote

import (
	"os"
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

	got, err := configDir()
	if err != nil {
		t.Fatalf("configDir: %v", err)
	}
	if got != dir {
		t.Errorf("configDir: got %q, want %q", got, dir)
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
	path, _ := configPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// 0600 = rw-------
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode: got %o, want 600", mode)
	}
}
