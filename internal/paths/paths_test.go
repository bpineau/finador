package paths

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setHome points every resolver at a private home and clears the overrides a
// developer may have exported.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{
		"FINADOR_CONFIG_DIR", "FINADOR_CACHE_DIR", "FINADOR_DATA_DIR", "FINADOR_DB",
		"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
	} {
		t.Setenv(k, "")
	}
	t.Cleanup(func() { stuck, stuckLedger = map[string]string{}, "" })
	return home
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
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "xdg-cache"))
	if got, _ := Cache(); got != filepath.Join(home, "xdg-cache", "finador") {
		t.Errorf("XDG_CACHE_HOME ignored: %q", got)
	}
	// A relative XDG base is invalid per spec and must be ignored.
	t.Setenv("XDG_CONFIG_HOME", "relative/path")
	if got, _ := Config(); got != filepath.Join(home, ".config", "finador") {
		t.Errorf("relative XDG_CONFIG_HOME should be ignored: %q", got)
	}
	// FINADOR_*_DIR names finador's own directory: nothing is appended.
	t.Setenv("FINADOR_CONFIG_DIR", filepath.Join(home, "cfg"))
	if got, _ := Config(); got != filepath.Join(home, "cfg") {
		t.Errorf("FINADOR_CONFIG_DIR = %q", got)
	}
	t.Setenv("FINADOR_DATA_DIR", filepath.Join(home, "data"))
	if got, _ := Ledger(); got != filepath.Join(home, "data", LedgerName) {
		t.Errorf("Ledger = %q", got)
	}
}

func TestMigrateMovesLegacyData(t *testing.T) {
	home := setHome(t)
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
	t.Setenv("FINADOR_DB", filepath.Join(home, "elsewhere.fin"))
	writeFile(t, filepath.Join(home, ".finador.fin"), "legacy")

	Migrate(&bytes.Buffer{})

	if _, err := os.Stat(filepath.Join(home, ".finador.fin")); err != nil {
		t.Error("FINADOR_DB must skip the ledger migration")
	}
}

func TestMigrateFailureKeepsUsingLegacy(t *testing.T) {
	home := setHome(t)
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
