package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finador/internal/domain"
)

// TestCachePathUsesTheFinadorDirectory pins the sidecar's location: the env
// override names finador's own directory, and nothing is appended to it.
func TestCachePathUsesTheFinadorDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINADOR_CACHE_DIR", dir)
	f, err := Create(filepath.Join(t.TempDir(), "t.fin"), "pw")
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

func TestMarketCacheRoundTripsViaSidecar(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "x.fin")
	f, err := Create(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	f.Book.Market.Price("cw8").Merge([]domain.PricePoint{{Date: domain.Date{Year: 2025, Month: 1, Day: 2}, Close: 12.5}})
	if err := f.SaveCache(); err != nil {
		t.Fatal(err)
	}

	back, err := Open(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if back.Book.Market.Prices["cw8"] == nil || len(back.Book.Market.Prices["cw8"].Points) != 1 {
		t.Fatalf("market not restored from sidecar: %+v", back.Book.Market)
	}
}

// A sidecar sealed under an older magic is a cache of another version: it
// decrypts fine but is ignored, so the next refresh rebuilds every series
// from its floor (D55: the FX crosses cached before pofo v0.2.88 are
// misdated, and an incremental refresh never rewrites them).
func TestOlderCacheVersionIsIgnored(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "v.fin")
	f, err := Create(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	f.Book.Market.FXSeries("USD").Merge([]domain.PricePoint{{Date: domain.Date{Year: 2025, Month: 1, Day: 3}, Close: 1.03}})
	if err := f.SaveCache(); err != nil {
		t.Fatal(err)
	}
	p, err := f.cachePath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	g := gcmOf(f.keyCache)
	nonce := raw[len(cacheMagic) : len(cacheMagic)+nonceSize]
	plain, err := g.Open(nil, nonce, raw[len(cacheMagic)+nonceSize:], []byte(cacheMagic))
	if err != nil {
		t.Fatal(err)
	}
	const older = "FINCACHE2"
	old := g.Seal(append([]byte(older), nonce...), nonce, plain, []byte(older))
	if err := os.WriteFile(p, old, 0o600); err != nil {
		t.Fatal(err)
	}

	back, err := Open(path, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Market.FX) != 0 {
		t.Fatalf("a %s sidecar was read: %+v", older, back.Book.Market.FX)
	}
}

func TestMissingSidecarIsEmptyMarketNoError(t *testing.T) {
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "y.fin")
	if _, err := Create(path, "pw"); err != nil {
		t.Fatal(err)
	}
	back, err := Open(path, "pw") // no SaveCache ever called
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Market.Prices) != 0 {
		t.Fatal("expected empty market")
	}
}
