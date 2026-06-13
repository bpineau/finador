package store

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"sort"
)

// Conflict is a true tie surfaced to the user: the same entity edited at the
// exact same ts on both sides with different payloads. The resolver picks which
// Candidate to keep (by index).
type Conflict struct {
	Class, ID, Ts string
	Candidates    []string // human-readable, e.g. "(this file) {...}" / "(other) {...}"
}

// MergeStats reports what the merge did, for the CLI summary.
type MergeStats struct {
	Entities  int // distinct entities in the merged log (non-tombstone winners)
	Conflicts int // ties resolved by the user
	FromOther int // winners whose chosen record came only from the other file
}

// classOf maps a record kind to its entity class. A delete record is a
// tombstone version of the same entity, so it shares the class.
func classOf(k recKind) string {
	switch k {
	case kAcct, kAcctDel:
		return "acct"
	case kAsset, kAssetDel:
		return "asset"
	case kTx, kTxEdit, kTxDel:
		return "tx"
	case kConfig:
		return "config"
	default:
		return string(k)
	}
}

func isTombstone(k recKind) bool {
	return k == kAcctDel || k == kAssetDel || k == kTxDel
}

// entityID returns the identity of the entity a record refers to: d.id for
// every class, except config which is keyed by d.key.
func entityID(r record) (string, error) {
	if classOf(r.K) == "config" {
		var kv cfgKV
		if err := json.Unmarshal(r.D, &kv); err != nil {
			return "", err
		}
		return kv.Key, nil
	}
	var ref idRef
	if err := json.Unmarshal(r.D, &ref); err != nil {
		return "", err
	}
	return ref.ID, nil
}

type entityKey struct{ class, id string }

// taggedRecord carries provenance so conflict candidates can name their source.
type taggedRecord struct {
	rec       record
	fromOther bool
}

// sameLedger reports whether two files are copies of the same ledger (same
// header id). The id is random per ledger and copied verbatim, so equality is
// the right cross-machine test.
func (f *File) sameLedger(other *File) bool {
	return subtle.ConstantTimeCompare(f.hdr.ID, other.hdr.ID) == 1
}

// Merge reconciles other into f: both are copies of the same ledger that
// diverged on two machines. Distinct entities (adds/deletes/edits) union with
// no loss; concurrent edits of the same entity are resolved last-writer-wins by
// ts; a true tie (same entity, equal ts, different payload) is handed to
// resolve, which returns the index of the candidate to keep. On success f is
// re-sealed as a fresh chained log (winners' original ts preserved) and written
// atomically; f.Book/entries/snap/disk are updated like Save/Compact.
func (f *File) Merge(other *File, resolve func(Conflict) (int, error)) (MergeStats, error) {
	if !f.sameLedger(other) {
		return MergeStats{}, fmt.Errorf("refusing to merge: different ledgers (file id mismatch) — merge expects copies of the same ledger")
	}

	// Group every record from both files by entity key, tagging provenance.
	groups := map[entityKey][]taggedRecord{}
	var order []entityKey // stable first-seen order, so the merge is deterministic
	gather := func(entries []entry, fromOther bool) error {
		for _, e := range entries {
			id, err := entityID(e.rec)
			if err != nil {
				return fmt.Errorf("merge: unreadable record id: %w", err)
			}
			k := entityKey{class: classOf(e.rec.K), id: id}
			if _, seen := groups[k]; !seen {
				order = append(order, k)
			}
			groups[k] = append(groups[k], taggedRecord{rec: e.rec, fromOther: fromOther})
		}
		return nil
	}
	if err := gather(f.entries, false); err != nil {
		return MergeStats{}, err
	}
	if err := gather(other.entries, true); err != nil {
		return MergeStats{}, err
	}

	var stats MergeStats
	var winners []record // non-tombstone winners, sorted by ts at the end

	for _, k := range order {
		recs := groups[k]
		// Sort by ts (RFC3339Nano string compare is chronological); stable, so
		// equal-ts records keep their gather order (this file before other).
		sort.SliceStable(recs, func(i, j int) bool { return recs[i].rec.Ts < recs[j].rec.Ts })
		maxTs := recs[len(recs)-1].rec.Ts

		// The contenders are the records at maxTs. Reduce them to distinct
		// payloads (same K + same D bytes): identical writes are not a conflict.
		var top []taggedRecord
		for _, tr := range recs {
			if tr.rec.Ts == maxTs {
				top = append(top, tr)
			}
		}
		distinct := distinctByPayload(top)

		var winner taggedRecord
		switch len(distinct) {
		case 1:
			winner = distinct[0]
		default:
			idx, err := resolve(Conflict{
				Class:      k.class,
				ID:         k.id,
				Ts:         maxTs,
				Candidates: renderCandidates(distinct),
			})
			if err != nil {
				return MergeStats{}, err
			}
			if idx < 0 || idx >= len(distinct) {
				return MergeStats{}, fmt.Errorf("merge: conflict resolution returned out-of-range index %d (have %d candidates)", idx, len(distinct))
			}
			winner = distinct[idx]
			stats.Conflicts++
		}

		// A tombstone winner means the entity stays deleted: omit it. A live
		// winner is emitted, preserving its original ts and payload.
		if isTombstone(winner.rec.K) {
			continue
		}
		stats.Entities++
		if winner.fromOther {
			stats.FromOther++
		}
		winners = append(winners, winner.rec)
	}

	// Output records sorted by ts (preserving each record's own ts), so history
	// stays meaningful and the log is chronological.
	sort.SliceStable(winners, func(i, j int) bool { return winners[i].Ts < winners[j].Ts })

	if err := f.reseal(winners); err != nil {
		return MergeStats{}, err
	}
	return stats, nil
}

// distinctByPayload collapses records with identical (K, D) into one, keeping
// the first occurrence (this file before other). Identical concurrent writes
// are not a conflict.
func distinctByPayload(trs []taggedRecord) []taggedRecord {
	var out []taggedRecord
	for _, tr := range trs {
		dup := false
		for _, kept := range out {
			if kept.rec.K == tr.rec.K && bytes.Equal(kept.rec.D, tr.rec.D) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, tr)
		}
	}
	return out
}

// renderCandidates pretty-prints the competing records for the user, labeling
// each with the file it came from.
func renderCandidates(trs []taggedRecord) []string {
	out := make([]string, len(trs))
	for i, tr := range trs {
		src := "(this file)"
		if tr.fromOther {
			src = "(other)     "
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, tr.rec.D, "  ", "  "); err != nil {
			pretty.Write(tr.rec.D)
		}
		out[i] = fmt.Sprintf("%s [%s] %s", src, tr.rec.K, pretty.String())
	}
	return out
}

// reseal rewrites f from the ordered winner records as a fresh chained log
// (new nonces, recomputed head), preserving each record's ts and payload, then
// writes atomically (tmp+fsync+rename, .bak). It mirrors Compact's sealing loop
// and locking/stamp discipline, feeding pre-ordered records instead of diff().
func (f *File) reseal(recs []record) error {
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
	for i, r := range recs {
		line, tag := sealLine(g, f.hdrHash, uint64(i+1), prev, r) // r.Ts preserved
		entries = append(entries, entry{line: line, tag: tag, rec: r})
		prev = tag
	}
	head := sealHead(g, f.hdrHash, len(entries), lastTagOrZero(entries))
	out := writeLog(f.hdrLine, entries, head)
	if err := atomicWrite(f.Path, out, true); err != nil {
		return err
	}

	book, err := replay(entries)
	if err != nil {
		return fmt.Errorf("%s: merged content unreadable: %w", f.Path, err)
	}
	f.Book = book
	f.entries = entries
	f.snap = snapshotOf(book)
	f.disk, _ = stampOf(f.Path)
	return nil
}
