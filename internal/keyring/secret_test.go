package keyring

import (
	"testing"
	"time"
)

func TestGetSecretDisabled(t *testing.T) {
	_, ok := GetSecret(Disabled(), "finador:github:alice/repo")
	if ok {
		t.Fatal("GetSecret on Disabled cache should return not-found")
	}
}

// fakeCache is a minimal in-memory Cache for testing PutSecret round-trips.
type fakeCache struct {
	entries map[string]string
}

func newFakeCache() *fakeCache {
	return &fakeCache{entries: map[string]string{}}
}

func (f *fakeCache) Get(key string) (string, bool) {
	v, ok := f.entries[key]
	return v, ok
}

func (f *fakeCache) Put(key, password string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	f.entries[key] = password
}

func (f *fakeCache) Purge() {
	for k := range f.entries {
		delete(f.entries, k)
	}
}

func TestPutSecretRoundTrip(t *testing.T) {
	c := newFakeCache()
	const key = "finador:github:alice/repo"
	const token = "ghp_abc123"

	PutSecret(c, key, token)

	got, ok := GetSecret(c, key)
	if !ok {
		t.Fatal("GetSecret: not found after PutSecret")
	}
	if got != token {
		t.Errorf("GetSecret: got %q, want %q", got, token)
	}
}

func TestSecretTTLIsEffectivelyNever(t *testing.T) {
	// Ensure secretTTL is at least 50 years so it behaves as "never".
	min := 50 * 365 * 24 * time.Hour
	if secretTTL < min {
		t.Errorf("secretTTL = %v; want at least %v", secretTTL, min)
	}
}
