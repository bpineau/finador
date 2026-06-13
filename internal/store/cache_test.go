package store

import (
	"path/filepath"
	"testing"

	"finador/internal/domain"
)

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
