//go:build !unix

package store

// Without flock, the timestamp check remains: the race window shrinks
// to a few microseconds — accepted off unix.
func lockSidecar(string) (func(), error) { return func() {}, nil }
