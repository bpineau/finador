// Package store reads and writes the encrypted portfolio file.
//
// Layout (UTF-8 text, one record per line):
//
//	line 1            clear JSON header: format, Argon2 params, salt, file id
//	lines 2..N+1      base64( nonce ‖ AES-256-GCM(record, AAD) ), AAD chains records
//	last line         base64( nonce ‖ AES-256-GCM(head, AAD) ): record count + final tag
//
// Writes are diff-on-save: unchanged record lines are re-emitted byte-for-byte,
// so a small logical change is a small change on disk (git-friendly). The market
// cache lives in a separate local sidecar (see cache.go), never in this file.
package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"finador/internal/domain"
)

// ErrConcurrent signals another process wrote the file since it was opened.
var ErrConcurrent = errors.New("file modified by another process since it was opened — retry the command")

type diskStamp struct {
	size  int64
	mtime int64 // ns
}

func stampOf(path string) (diskStamp, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return diskStamp{}, false
	}
	return diskStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}, true
}

// File is an open, decrypted portfolio file.
type File struct {
	Path string
	Book *domain.Book

	hdr      header
	hdrLine  []byte // exact on-disk header bytes (so re-emission is byte-stable)
	hdrHash  []byte // sha256(hdrLine), the AAD prefix
	keyLog   [32]byte
	keyCache [32]byte
	entries  []entry
	snap     snapshot
	disk     diskStamp // (size, mtime) at Open; zero at Create
}

// Create makes a new encrypted file holding an empty Book. It refuses to overwrite.
func Create(path, password string) (*File, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s already exists", path)
	}
	h := defaultHeader()
	hdrLine := h.encode()
	hh := sha256.Sum256(hdrLine)
	keyLog, keyCache := deriveKeys(password, h)
	f := &File{
		Path: path, Book: domain.NewBook(),
		hdr: h, hdrLine: hdrLine, hdrHash: hh[:],
		keyLog: keyLog, keyCache: keyCache,
		snap: snapshotOf(domain.NewBook()),
	}
	return f, f.Save()
}

// Open reads and decrypts a file. A wrong password and a tampered file are
// indistinguishable by construction: both yield domain.ErrBadPassword.
func Open(path, password string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s does not exist — run 'finador init' to create it", path)
		}
		return nil, err
	}
	h, hdrLine, entries, keyLog, keyCache, err := readLog(path, raw, password)
	if err != nil {
		return nil, err
	}
	book, err := replay(entries)
	if err != nil {
		return nil, fmt.Errorf("%s: unreadable content: %w", path, err)
	}
	hh := sha256.Sum256(hdrLine)
	f := &File{
		Path: path, Book: book,
		hdr: h, hdrLine: hdrLine, hdrHash: hh[:],
		keyLog: keyLog, keyCache: keyCache,
		entries: entries, snap: snapshotOf(book),
	}
	f.loadCache()
	f.disk, _ = stampOf(path)
	return f, nil
}

// Save appends the records needed to materialize the current Book, re-emitting
// unchanged lines byte-for-byte, then writes atomically (tmp + fsync + rename;
// the previous version becomes .bak). Optimistic concurrency: ErrConcurrent if
// another process wrote since Open.
func (f *File) Save() error {
	unlock, err := lockSidecar(f.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	if f.disk != (diskStamp{}) {
		if cur, ok := stampOf(f.Path); ok && cur != f.disk {
			return fmt.Errorf("%s: %w", f.Path, ErrConcurrent)
		}
	}

	g := gcmOf(f.keyLog)
	entries := append([]entry(nil), f.entries...) // local copy: commit only on success
	prev := lastTagOrZero(entries)
	seq := uint64(len(entries))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, r := range diff(f.snap, f.Book) {
		seq++
		r.Ts = now // stamp each NEW record at save time (part of the sealed plaintext)
		line, tag := sealLine(g, f.hdrHash, seq, prev, r)
		entries = append(entries, entry{line: line, tag: tag, rec: r})
		prev = tag
	}
	head := sealHead(g, f.hdrHash, len(entries), lastTagOrZero(entries))
	out := writeLog(f.hdrLine, entries, head)

	if err := atomicWrite(f.Path, out, true); err != nil {
		return err
	}
	f.entries = entries
	f.snap = snapshotOf(f.Book)
	f.disk, _ = stampOf(f.Path)
	return nil
}

// atomicWrite writes data to path via tmp + fsync + rename. When backup is true,
// an existing file is rotated to .bak first.
func atomicWrite(path string, data []byte, backup bool) error {
	tmp := path + ".tmp"
	w, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return err
	}
	if err := w.Sync(); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if backup {
		if _, err := os.Stat(path); err == nil {
			if err := os.Rename(path, path+".bak"); err != nil {
				return err
			}
		}
	}
	return os.Rename(tmp, path)
}

// Compact rewrites a minimal log from the current Book — dropping superseded and
// tombstoned records — with a fresh chain. One full-file rewrite; rare.
func (f *File) Compact() error {
	unlock, err := lockSidecar(f.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if f.disk != (diskStamp{}) {
		if cur, ok := stampOf(f.Path); ok && cur != f.disk {
			return fmt.Errorf("%s: %w", f.Path, ErrConcurrent)
		}
	}

	g := gcmOf(f.keyLog)
	var entries []entry
	prev := lastTagOrZero(entries)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i, r := range diff(snapshot{}, f.Book) { // empty prev -> all creates
		r.Ts = now // a compacted log re-stamps every record at compaction time
		line, tag := sealLine(g, f.hdrHash, uint64(i+1), prev, r)
		entries = append(entries, entry{line: line, tag: tag, rec: r})
		prev = tag
	}
	head := sealHead(g, f.hdrHash, len(entries), lastTagOrZero(entries))
	out := writeLog(f.hdrLine, entries, head)
	if err := atomicWrite(f.Path, out, true); err != nil {
		return err
	}
	f.entries = entries
	f.snap = snapshotOf(f.Book)
	f.disk, _ = stampOf(f.Path)
	return nil
}
