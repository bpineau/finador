package remote

import (
	"context"
	"errors"
)

// Version is an opaque concurrency token. For the GitHub backend it is the
// blob SHA returned by the Contents API.
type Version string

var (
	// ErrRemoteConflict is returned by Push when the base SHA is stale
	// (the remote has been updated since the last Fetch).
	ErrRemoteConflict = errors.New("remote changed since last sync")

	// ErrRemoteMissing is returned by Fetch when the file does not exist yet.
	ErrRemoteMissing = errors.New("remote file does not exist yet")

	// ErrRemoteAuth is returned on HTTP 401 or 403 (bad token or insufficient
	// permissions). It is distinct from ErrOffline so callers can show a
	// targeted message.
	ErrRemoteAuth = errors.New("github authentication failed")

	// ErrOffline is returned when the network is unreachable (transport
	// error, DNS failure, etc.). It is distinct from auth errors.
	ErrOffline = errors.New("cannot reach the remote")
)

// Backend is the seam between the sync layer and the actual remote storage.
// The only production implementation is GitHubClient; the interface allows
// tests to use a stub.
type Backend interface {
	// Fetch downloads the current file content and returns the opaque version
	// token. Returns ErrRemoteMissing if the file does not exist yet.
	Fetch(ctx context.Context) (data []byte, v Version, err error)

	// Push uploads data, committing it with message. base must be the Version
	// returned by the last Fetch; pass Version("") when creating a new file.
	// Returns ErrRemoteConflict if the remote has moved on since base.
	Push(ctx context.Context, data []byte, base Version, message string) (Version, error)

	// Describe returns a human-readable identifier for the remote location
	// (e.g. "github:owner/repo/path@branch").
	Describe() string
}
