package keyring

import "time"

// secretTTL is effectively "never" - long-lived secrets such as a GitHub token.
const secretTTL = 100 * 365 * 24 * time.Hour

// PutSecret stores a long-lived secret (e.g. a GitHub PAT) in c under key.
func PutSecret(c Cache, key, value string) {
	c.Put(key, value, secretTTL)
}

// GetSecret retrieves a secret previously stored with PutSecret.
func GetSecret(c Cache, key string) (string, bool) {
	return c.Get(key)
}
