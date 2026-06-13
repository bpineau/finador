package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"finador/internal/domain"
)

// entry is one decoded log line kept in memory: its verbatim base64 line (so
// Save can re-emit unchanged lines byte-identically), its GCM tag (for the
// chain) and the decoded record.
type entry struct {
	line string
	tag  []byte
	rec  record
}

func lastTagOrZero(entries []entry) []byte {
	if len(entries) == 0 {
		return make([]byte, tagSize)
	}
	return entries[len(entries)-1].tag
}

func writeLog(hdrLine []byte, entries []entry, headLine string) []byte {
	var b bytes.Buffer
	b.Write(hdrLine)
	b.WriteByte('\n')
	for _, e := range entries {
		b.WriteString(e.line)
		b.WriteByte('\n')
	}
	b.WriteString(headLine)
	b.WriteByte('\n')
	return b.Bytes()
}

// readLog parses and fully authenticates a ledger file body. Returns the
// header (with its exact on-disk line), the entries, and both subkeys.
func readLog(path string, raw []byte, password string) (h header, hdrLine []byte, entries []entry, keyLog, keyCache [32]byte, err error) {
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return header{}, nil, nil, keyLog, keyCache, fmt.Errorf("%s is not a finador file", path)
	}
	h, err = parseHeader([]byte(lines[0]))
	if err != nil {
		return header{}, nil, nil, keyLog, keyCache, fmt.Errorf("%s: %w", path, err)
	}
	keyLog, keyCache = deriveKeys(password, h)
	hh := sha256.Sum256([]byte(lines[0]))
	g := gcmOf(keyLog)

	recordLines := lines[1 : len(lines)-1]
	prev := make([]byte, tagSize)
	for i, line := range recordLines {
		rec, tag, e := openLine(g, hh[:], uint64(i+1), prev, line)
		if e != nil {
			return header{}, nil, nil, keyLog, keyCache, fmt.Errorf("%s: %w", path, e)
		}
		entries = append(entries, entry{line: line, tag: tag, rec: rec})
		prev = tag
	}
	hp, e := openHead(g, hh[:], len(entries), lines[len(lines)-1])
	if e != nil || hp.Count != len(entries) || !bytes.Equal(hp.Head, lastTagOrZero(entries)) {
		return header{}, nil, nil, keyLog, keyCache, fmt.Errorf("%s: %w", path, domain.ErrBadPassword)
	}
	return h, []byte(lines[0]), entries, keyLog, keyCache, nil
}

type idRef struct {
	ID string `json:"id"`
}

type txRef struct {
	ID domain.TxID `json:"id"`
}

type cfgKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// replay folds the log into a materialized Book: create/upsert/delete per id,
// in append order. Ids are random and self-assigned, so nothing is derived.
func replay(entries []entry) (*domain.Book, error) {
	b := domain.NewBook()
	for _, e := range entries {
		switch e.rec.K {
		case kAcct:
			var a domain.Account
			if err := json.Unmarshal(e.rec.D, &a); err != nil {
				return nil, err
			}
			b.Accounts = upsertAccount(b.Accounts, &a)
		case kAcctDel:
			var ref idRef
			if err := json.Unmarshal(e.rec.D, &ref); err != nil {
				return nil, err
			}
			b.Accounts = rejectAccount(b.Accounts, domain.AccountID(ref.ID))
		case kAsset:
			var a domain.Asset
			if err := json.Unmarshal(e.rec.D, &a); err != nil {
				return nil, err
			}
			b.Assets = upsertAsset(b.Assets, &a)
		case kAssetDel:
			var ref idRef
			if err := json.Unmarshal(e.rec.D, &ref); err != nil {
				return nil, err
			}
			b.Assets = rejectAsset(b.Assets, domain.AssetID(ref.ID))
		case kConfig:
			var kv cfgKV
			if err := json.Unmarshal(e.rec.D, &kv); err != nil {
				return nil, err
			}
			b.Config[kv.Key] = kv.Value
		case kTx, kTxEdit:
			var t domain.Transaction
			if err := json.Unmarshal(e.rec.D, &t); err != nil {
				return nil, err
			}
			b.Transactions = upsertTx(b.Transactions, &t)
		case kTxDel:
			var ref txRef
			if err := json.Unmarshal(e.rec.D, &ref); err != nil {
				return nil, err
			}
			b.Transactions = rejectTx(b.Transactions, ref.ID)
		case kLabel:
			var l domain.Label
			if err := json.Unmarshal(e.rec.D, &l); err != nil {
				return nil, err
			}
			b.Labels = upsertLabel(b.Labels, &l)
		case kLabelDel:
			var ref idRef
			if err := json.Unmarshal(e.rec.D, &ref); err != nil {
				return nil, err
			}
			b.Labels = rejectLabel(b.Labels, domain.LabelID(ref.ID))
		default:
			return nil, fmt.Errorf("unknown record kind %q", e.rec.K)
		}
	}
	return b, nil
}

func upsertAccount(xs []*domain.Account, a *domain.Account) []*domain.Account {
	for i, x := range xs {
		if x.ID == a.ID {
			xs[i] = a
			return xs
		}
	}
	return append(xs, a)
}

func rejectAccount(xs []*domain.Account, id domain.AccountID) []*domain.Account {
	out := xs[:0]
	for _, x := range xs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}

func upsertAsset(xs []*domain.Asset, a *domain.Asset) []*domain.Asset {
	for i, x := range xs {
		if x.ID == a.ID {
			xs[i] = a
			return xs
		}
	}
	return append(xs, a)
}

func rejectAsset(xs []*domain.Asset, id domain.AssetID) []*domain.Asset {
	out := xs[:0]
	for _, x := range xs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}

func upsertTx(xs []*domain.Transaction, t *domain.Transaction) []*domain.Transaction {
	for i, x := range xs {
		if x.ID == t.ID {
			xs[i] = t
			return xs
		}
	}
	return append(xs, t)
}

func rejectTx(xs []*domain.Transaction, id domain.TxID) []*domain.Transaction {
	out := xs[:0]
	for _, x := range xs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}

func upsertLabel(xs []*domain.Label, l *domain.Label) []*domain.Label {
	for i, x := range xs {
		if x.ID == l.ID {
			xs[i] = l
			return xs
		}
	}
	return append(xs, l)
}

func rejectLabel(xs []*domain.Label, id domain.LabelID) []*domain.Label {
	out := xs[:0]
	for _, x := range xs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}

// snapshot is the last-persisted state, by stable identity, used to compute the
// minimal set of records to append on Save. Entities are compared by their JSON.
type snapshot struct {
	accts  map[domain.AccountID][]byte
	assets map[domain.AssetID][]byte
	txs    map[domain.TxID][]byte
	labels map[domain.LabelID][]byte
	config map[string]string
}

func snapshotOf(b *domain.Book) snapshot {
	s := snapshot{
		accts:  map[domain.AccountID][]byte{},
		assets: map[domain.AssetID][]byte{},
		txs:    map[domain.TxID][]byte{},
		labels: map[domain.LabelID][]byte{},
		config: map[string]string{},
	}
	for _, a := range b.Accounts {
		s.accts[a.ID] = mustJSON(a)
	}
	for _, a := range b.Assets {
		s.assets[a.ID] = mustJSON(a)
	}
	for _, t := range b.Transactions {
		s.txs[t.ID] = mustJSON(t)
	}
	for _, l := range b.Labels {
		s.labels[l.ID] = mustJSON(l)
	}
	for k, v := range b.Config {
		s.config[k] = v
	}
	return s
}

// diff returns the records to append so the log materializes b, given prev as
// the last-persisted state. Order: config, accounts (+deletes), assets
// (+deletes), then transactions (+deletes) — definitions before references.
func diff(prev snapshot, b *domain.Book) []record {
	var recs []record

	for k, v := range b.Config {
		if old, ok := prev.config[k]; !ok || old != v {
			recs = append(recs, record{K: kConfig, D: mustJSON(cfgKV{Key: k, Value: v})})
		}
	}

	for _, a := range b.Accounts {
		cur := mustJSON(a)
		if old, ok := prev.accts[a.ID]; !ok || !bytes.Equal(old, cur) {
			recs = append(recs, record{K: kAcct, D: cur})
		}
	}
	for id := range prev.accts {
		if !hasAccount(b, id) {
			recs = append(recs, record{K: kAcctDel, D: mustJSON(idRef{ID: string(id)})})
		}
	}

	for _, a := range b.Assets {
		cur := mustJSON(a)
		if old, ok := prev.assets[a.ID]; !ok || !bytes.Equal(old, cur) {
			recs = append(recs, record{K: kAsset, D: cur})
		}
	}
	for id := range prev.assets {
		if !hasAsset(b, id) {
			recs = append(recs, record{K: kAssetDel, D: mustJSON(idRef{ID: string(id)})})
		}
	}

	for _, t := range b.Transactions {
		cur := mustJSON(t)
		old, ok := prev.txs[t.ID]
		if !ok {
			recs = append(recs, record{K: kTx, D: cur})
		} else if !bytes.Equal(old, cur) {
			recs = append(recs, record{K: kTxEdit, D: cur})
		}
	}
	for id := range prev.txs {
		if !hasTx(b, id) {
			recs = append(recs, record{K: kTxDel, D: mustJSON(txRef{ID: id})})
		}
	}

	for _, l := range b.Labels {
		cur := mustJSON(l)
		if old, ok := prev.labels[l.ID]; !ok || !bytes.Equal(old, cur) {
			recs = append(recs, record{K: kLabel, D: cur})
		}
	}
	for id := range prev.labels {
		if !hasLabel(b, id) {
			recs = append(recs, record{K: kLabelDel, D: mustJSON(idRef{ID: string(id)})})
		}
	}
	return recs
}

func hasAccount(b *domain.Book, id domain.AccountID) bool {
	for _, a := range b.Accounts {
		if a.ID == id {
			return true
		}
	}
	return false
}

func hasAsset(b *domain.Book, id domain.AssetID) bool {
	for _, a := range b.Assets {
		if a.ID == id {
			return true
		}
	}
	return false
}

func hasTx(b *domain.Book, id domain.TxID) bool {
	for _, t := range b.Transactions {
		if t.ID == id {
			return true
		}
	}
	return false
}

func hasLabel(b *domain.Book, id domain.LabelID) bool {
	for _, l := range b.Labels {
		if l.ID == id {
			return true
		}
	}
	return false
}
