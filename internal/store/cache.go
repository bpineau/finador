package store

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"

	"finador/internal/domain"
)

const cacheMagic = "FINCACHE2"

// cacheDir is os.UserCacheDir() unless FINADOR_CACHE_DIR overrides it (tests).
func cacheDir() (string, error) {
	if d := os.Getenv("FINADOR_CACHE_DIR"); d != "" {
		return d, nil
	}
	return os.UserCacheDir()
}

// cachePath derives the sidecar path from the file id: deterministic and stable
// across machines, physically outside any git repo.
func (f *File) cachePath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(f.hdr.ID)
	return filepath.Join(dir, "finador", id+".cache"), nil
}

// SaveCache writes the market cache to its encrypted sidecar (cache subkey). It
// is independent of Save(): only market-touching paths (refresh) call it.
func (f *File) SaveCache() error {
	p, err := f.cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	var plain bytes.Buffer
	zw := gzip.NewWriter(&plain)
	if err := json.NewEncoder(zw).Encode(f.Book.Market); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	g := gcmOf(f.keyCache)
	nonce := make([]byte, nonceSize)
	mustRand(nonce)
	out := append([]byte(cacheMagic), nonce...)
	out = g.Seal(out, nonce, plain.Bytes(), []byte(cacheMagic))
	return atomicWrite(p, out, false)
}

// loadCache populates Book.Market from the sidecar. A missing or unreadable
// sidecar leaves the market empty (a refresh rebuilds it): never an error.
func (f *File) loadCache() {
	p, err := f.cachePath()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	if len(raw) < len(cacheMagic)+nonceSize+tagSize || string(raw[:len(cacheMagic)]) != cacheMagic {
		return
	}
	g := gcmOf(f.keyCache)
	nonce := raw[len(cacheMagic) : len(cacheMagic)+nonceSize]
	plain, err := g.Open(nil, nonce, raw[len(cacheMagic)+nonceSize:], []byte(cacheMagic))
	if err != nil {
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return
	}
	defer zr.Close()
	var md domain.MarketData
	if json.NewDecoder(zr).Decode(&md) == nil {
		f.Book.Market = md
	}
}
