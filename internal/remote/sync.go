package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// state is the sidecar that tracks what the local working copy knows about the
// remote. It lives next to the working copy under the checkout directory.
type state struct {
	SHA      Version   `json:"sha"`      // last-known remote blob sha; "" means "remote has no file yet"
	LastPull time.Time `json:"lastPull"` // when the working copy was last refreshed from the remote
	Dirty    bool      `json:"dirty"`    // working copy has local changes not yet on the remote
}

// MergeFunc reconciles the remote file (at remotePath) INTO the local working
// copy (at localPath), writing the merged result back to localPath. The CLI will
// implement this with store.Merge; tests pass a stub.
type MergeFunc func(localPath, remotePath string) error

// maxPushRetries bounds the conflict-resolution loop on Push.
const maxPushRetries = 3

// Syncer keeps a local working copy of the remote .fin in sync with a Backend.
// It never decrypts: conflict reconciliation is delegated to a MergeFunc. The
// working copy is what commands open and mutate; this layer pulls it fresh
// before reads/writes and pushes it after writes.
type Syncer struct {
	backend       Backend
	gh            GitHub
	copyPath      string // the .fin working copy commands open
	statePath     string // the JSON state sidecar
	readPullAfter time.Duration
	clock         func() time.Time
}

// cacheDir mirrors store.cacheDir: os.UserCacheDir() unless FINADOR_CACHE_DIR
// overrides it (honored first, for tests and the rest of the code).
func cacheDir() (string, error) {
	if d := os.Getenv("FINADOR_CACHE_DIR"); d != "" {
		return d, nil
	}
	return os.UserCacheDir()
}

// hashKey derives a stable, filesystem-safe key from owner/repo/path so the
// working copy and state live at a deterministic location across runs.
func hashKey(gh GitHub) string {
	sum := sha256.Sum256([]byte(gh.Owner + "/" + gh.Repo + "/" + gh.Path))
	return hex.EncodeToString(sum[:16])
}

// NewSyncer builds a Syncer for the given backend and remote coordinates. The
// working copy and state paths are derived from a stable hash of the
// owner/repo/path under the checkout directory.
func NewSyncer(b Backend, gh GitHub, readPullAfter time.Duration) (*Syncer, error) {
	base, err := cacheDir()
	if err != nil {
		return nil, fmt.Errorf("cache dir: %w", err)
	}
	if readPullAfter <= 0 {
		readPullAfter = time.Hour
	}
	dir := filepath.Join(base, "finador", "checkout")
	key := hashKey(gh)
	return &Syncer{
		backend:       b,
		gh:            gh,
		copyPath:      filepath.Join(dir, key+".fin"),
		statePath:     filepath.Join(dir, key+".state.json"),
		readPullAfter: readPullAfter,
		clock:         time.Now,
	}, nil
}

// WorkingCopy returns the path commands open, mutate and save.
func (s *Syncer) WorkingCopy() string { return s.copyPath }

// HasWorkingCopy reports whether a local working copy exists on disk.
func (s *Syncer) HasWorkingCopy() bool { return s.copyExists() }

// Adopt seeds the remote from bytes you already have locally — a one-time
// migration into GitHub mode. It pushes them as the file (no decryption) and
// installs them as the working copy + state, so the next read uses them. It
// refuses to overwrite an existing remote file unless force is set.
func (s *Syncer) Adopt(ctx context.Context, data []byte, message string, force bool) error {
	st, err := s.loadState()
	if err != nil {
		return err
	}
	var base Version
	switch _, v, ferr := s.backend.Fetch(ctx); {
	case ferr == nil:
		if !force {
			return fmt.Errorf("the remote already holds a file — refusing to overwrite; pass --force, or use `finador sync` to reconcile")
		}
		base = v
	case errors.Is(ferr, ErrRemoteMissing):
		base = ""
	default:
		return ferr
	}
	newSHA, err := s.backend.Push(ctx, data, base, message)
	if err != nil {
		return err
	}
	if err := atomicWrite(s.copyPath, data); err != nil {
		return err
	}
	st.SHA = newSHA
	st.LastPull = s.now()
	st.Dirty = false
	return s.saveState(st)
}

// Describe returns the backend's human-readable remote identifier.
func (s *Syncer) Describe() string { return s.backend.Describe() }

// EnsureDir creates the checkout directory (0700) so store.Create/Open can write
// the working copy and its lock sidecar. Pulls go through atomicWrite, which
// already does this, but a first `init` creates the file directly.
func (s *Syncer) EnsureDir() error {
	if err := os.MkdirAll(s.checkoutDir(), 0o700); err != nil {
		return fmt.Errorf("mkdir checkout dir: %w", err)
	}
	return nil
}

// Status returns the current sync state for `finador remote show`: the
// last-known remote blob sha, when the working copy was last pulled, and whether
// it holds unpushed local changes. A missing state sidecar yields the zero value.
func (s *Syncer) Status() (sha string, lastPull time.Time, dirty bool) {
	st, err := s.loadState()
	if err != nil {
		return "", time.Time{}, false
	}
	return string(st.SHA), st.LastPull, st.Dirty
}

// now returns the current time via the (overridable) clock, always set in NewSyncer.
func (s *Syncer) now() time.Time { return s.clock() }

// checkoutDir is the directory holding the working copy and state.
func (s *Syncer) checkoutDir() string { return filepath.Dir(s.copyPath) }

// copyExists reports whether the working copy is present on disk.
func (s *Syncer) copyExists() bool {
	_, err := os.Stat(s.copyPath)
	return err == nil
}

// loadState reads the state sidecar. A missing sidecar yields the zero value
// (SHA:"", Dirty:false, LastPull: zero) and no error.
func (s *Syncer) loadState() (state, error) {
	data, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return state{}, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("read sync state: %w", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, fmt.Errorf("parse sync state: %w", err)
	}
	return st, nil
}

// saveState writes the state sidecar atomically (0600), creating the checkout
// directory (0700) if needed.
func (s *Syncer) saveState(st state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sync state: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(s.statePath, data)
}

// atomicWrite writes data to path via a temp file + rename, mode 0600, after
// ensuring the parent directory exists (0700).
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir checkout dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".finsync.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		_ = os.Remove(tmpName) // no-op if the rename succeeded
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// pull refreshes the working copy from the remote. INVARIANT: only call this
// when the working copy is clean (!Dirty) — it overwrites the copy bytes.
//
//   - success           → write remote bytes to the copy, SHA=new,
//     LastPull=now, Dirty=false.
//   - ErrRemoteMissing  → SHA="" (remote has no file yet), copy left as-is.
//   - ErrOffline        → no change; offline=true so the caller can warn.
//   - other errors      → returned.
func (s *Syncer) pull(ctx context.Context, st *state) (offline bool, err error) {
	data, v, err := s.backend.Fetch(ctx)
	switch {
	case err == nil:
		if werr := atomicWrite(s.copyPath, data); werr != nil {
			return false, werr
		}
		st.SHA = v
		st.LastPull = s.now()
		st.Dirty = false
		return false, nil
	case errors.Is(err, ErrRemoteMissing):
		st.SHA = ""
		// Leave the working copy as-is; the remote is created on first push.
		return false, nil
	case errors.Is(err, ErrOffline):
		return true, nil
	default:
		return false, err
	}
}

// pushCopy uploads the current working-copy bytes, reconciling conflicts via
// merge and retrying (bounded). It is the only writer of remote state on the
// push side.
//
//   - success          → SHA=new, Dirty=false.
//   - ErrRemoteConflict → Fetch remote into a temp file, merge(copy, temp)
//     (which rewrites the copy = merged), re-Push with the new remote SHA.
//     Bounded retry; SHA/Dirty persisted on eventual success.
//   - ErrOffline       → Dirty=true, offline warning, NO error (local write stands).
//   - ErrRemoteAuth / other → returned.
func (s *Syncer) pushCopy(ctx context.Context, st *state, message string, merge MergeFunc) (offline bool, err error) {
	// DATA SAFETY: the working copy holds unpushed local content for the whole
	// duration of this call. Mark it dirty up front and only clear that once a
	// push (or merged re-push) actually lands. Callers persist *st even on the
	// error return below, so a later read never clobbers these edits.
	st.Dirty = true

	base := st.SHA
	for attempt := 0; attempt < maxPushRetries; attempt++ {
		data, rerr := os.ReadFile(s.copyPath)
		if rerr != nil {
			return false, fmt.Errorf("read working copy: %w", rerr)
		}

		newSHA, perr := s.backend.Push(ctx, data, base, message)
		switch {
		case perr == nil:
			st.SHA = newSHA
			st.Dirty = false
			st.LastPull = s.now()
			return false, nil

		case errors.Is(perr, ErrOffline):
			// Stays dirty (set above); local write stands, push deferred.
			return true, nil

		case errors.Is(perr, ErrRemoteConflict):
			newBase, merr := s.reconcile(ctx, merge)
			if merr != nil {
				// Stays dirty; the merged-or-original copy is preserved.
				return false, merr
			}
			base = newBase
			// loop and re-push the merged working copy with the fresh base.

		default:
			// ErrRemoteAuth and any other error: stays dirty.
			return false, perr
		}
	}
	return false, fmt.Errorf("push: still conflicting after %d attempts: %w", maxPushRetries, ErrRemoteConflict)
}

// reconcile fetches the current remote into a temp file and merges it into the
// working copy, returning the fresh remote SHA to use as the next push base.
func (s *Syncer) reconcile(ctx context.Context, merge MergeFunc) (Version, error) {
	if merge == nil {
		return "", errors.New("push conflict but no merge function provided")
	}
	remoteData, remoteSHA, ferr := s.backend.Fetch(ctx)
	if ferr != nil {
		// Offline mid-conflict, auth, missing, etc. — surface it; the caller's
		// pushCopy turns ErrOffline into dirty only on the Push path, so here we
		// translate offline into a conflict-time error to avoid a silent loss.
		return "", fmt.Errorf("fetch remote for merge: %w", ferr)
	}

	tmp, err := os.CreateTemp(s.checkoutDir(), ".finmerge.*.tmp")
	if err != nil {
		return "", fmt.Errorf("create merge temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("chmod merge temp: %w", err)
	}
	if _, err := tmp.Write(remoteData); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write merge temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close merge temp: %w", err)
	}

	// merge rewrites the working copy in place with the reconciled content.
	if err := merge(s.copyPath, tmpName); err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	return remoteSHA, nil
}

// ForRead makes the working copy reasonably fresh and returns any warnings.
//
//   - Dirty       → use the copy as-is (do NOT clobber unpushed edits); no pull.
//   - else online and stale (now-LastPull > readPullAfter) or no copy yet → pull.
//   - offline     → warn "using local copy"; with no copy at all → error.
//   - otherwise   → use the copy.
func (s *Syncer) ForRead(ctx context.Context) (warnings []string, err error) {
	st, err := s.loadState()
	if err != nil {
		return nil, err
	}

	// INVARIANT 4 / dirty-not-clobbered: never pull over unpushed local edits.
	if st.Dirty {
		return nil, nil
	}

	stale := s.now().Sub(st.LastPull) > s.readPullAfter
	if !stale && s.copyExists() {
		return nil, nil
	}

	offline, perr := s.pull(ctx, &st)
	if perr != nil {
		return nil, perr
	}
	if serr := s.saveState(st); serr != nil {
		return nil, serr
	}
	if offline {
		if !s.copyExists() {
			return nil, fmt.Errorf("%w: no local copy to read", ErrOffline)
		}
		return []string{"offline: using local copy"}, nil
	}
	// Online pull succeeded but the remote has no file yet (ErrRemoteMissing
	// left the copy absent): there is nothing to read — the caller must
	// init or adopt. Surfacing it here beats a cryptic "file does not exist".
	if !s.copyExists() {
		return nil, ErrRemoteMissing
	}
	return nil, nil
}

// ForWrite makes the working copy fresh enough to mutate safely, reconciling
// any dirty state first. After it returns, the caller opens, mutates and saves
// the working copy, then calls AfterWrite.
//
//   - Dirty → reconcile first via pushCopy (merges on conflict). Offline →
//     leave dirty, proceed to mutate on the current copy (warn).
//   - else  → pull (best-effort; offline → warn, proceed on current copy).
//     First-init (ErrRemoteMissing and no copy) → proceed; the caller/store
//     creates the copy and the first push creates the remote.
func (s *Syncer) ForWrite(ctx context.Context, merge MergeFunc) (warnings []string, err error) {
	st, err := s.loadState()
	if err != nil {
		return nil, err
	}

	if st.Dirty {
		// Push the unpushed edits before mutating again so we build on the
		// freshest reconciled base. pushCopy merges on conflict; offline keeps
		// the copy dirty and lets us proceed.
		offline, perr := s.pushCopy(ctx, &st, "finador: sync before write", merge)
		// Persist state regardless: pushCopy keeps Dirty=true on any failure so
		// a later read won't clobber the unpushed working copy.
		if serr := s.saveState(st); serr != nil {
			return nil, serr
		}
		if perr != nil {
			return nil, perr
		}
		if offline {
			warnings = append(warnings, "offline: working copy kept, will push later")
		}
		return warnings, nil
	}

	// Clean copy: pull a fresh base (best-effort).
	offline, perr := s.pull(ctx, &st)
	if perr != nil {
		return nil, perr
	}
	if serr := s.saveState(st); serr != nil {
		return nil, serr
	}
	if offline {
		warnings = append(warnings, "offline: using local copy")
	}
	return warnings, nil
}

// AfterWrite pushes the just-mutated working copy, resolving conflicts via
// merge. Offline → the working copy is kept dirty and pushed at the next online
// access; no error is returned (the local write stands).
func (s *Syncer) AfterWrite(ctx context.Context, message string, merge MergeFunc) (warnings []string, err error) {
	st, err := s.loadState()
	if err != nil {
		return nil, err
	}

	offline, perr := s.pushCopy(ctx, &st, message, merge)
	// Persist state regardless: pushCopy keeps Dirty=true on any failure so a
	// later read won't clobber the just-mutated working copy.
	if serr := s.saveState(st); serr != nil {
		return nil, serr
	}
	if perr != nil {
		return nil, perr
	}
	if offline {
		warnings = append(warnings, "offline: change saved locally, not pushed (will push later)")
	}
	return warnings, nil
}

// Sync forces a reconcile: push if dirty, then refresh. Backs `finador sync`.
func (s *Syncer) Sync(ctx context.Context, merge MergeFunc) (warnings []string, err error) {
	st, err := s.loadState()
	if err != nil {
		return nil, err
	}

	if st.Dirty {
		offline, perr := s.pushCopy(ctx, &st, "finador: sync", merge)
		// Persist state regardless: Dirty stays true on failure, protecting the
		// unpushed working copy from a clobbering refresh below or on next read.
		if serr := s.saveState(st); serr != nil {
			return nil, serr
		}
		if perr != nil {
			return nil, perr
		}
		if offline {
			warnings = append(warnings, "offline: changes kept locally, not pushed")
			return warnings, nil
		}
	}

	// Now clean (or never dirty): refresh from the remote.
	offline, perr := s.pull(ctx, &st)
	if perr != nil {
		return nil, perr
	}
	if serr := s.saveState(st); serr != nil {
		return nil, serr
	}
	if offline {
		warnings = append(warnings, "offline: using local copy")
	}
	return warnings, nil
}
