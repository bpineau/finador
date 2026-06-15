package store

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"finador/internal/domain"
)

type recKind string

const (
	kAcct     recKind = "acct"
	kAcctDel  recKind = "acct-del"
	kAsset    recKind = "asset"
	kAssetDel recKind = "asset-del"
	kConfig   recKind = "config"
	kTx       recKind = "tx"
	kTxEdit   recKind = "tx-edit"
	kTxDel    recKind = "tx-del"
	kLabel    recKind = "label"
	kLabelDel recKind = "label-del"
)

// record is one log entry: a kind tag, a creation timestamp (RFC3339Nano, part
// of the sealed plaintext) and the JSON of its payload.
type record struct {
	K  recKind         `json:"k"`
	Ts string          `json:"ts"`
	D  json.RawMessage `json:"d"`
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func gcmOf(key [32]byte) cipher.AEAD {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err) // fixed key size: cannot fail
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return g
}

// aad binds a record to the file (header hash), its position (seq) and the
// previous record's tag (hash chain): reorder/drop/splice break decryption.
func aad(hdrHash []byte, seq uint64, prevTag []byte) []byte {
	b := make([]byte, 0, len(hdrHash)+8+tagSize)
	b = append(b, hdrHash...)
	b = binary.BigEndian.AppendUint64(b, seq)
	return append(b, prevTag...)
}

// sealLine seals one record and returns its base64 line and its GCM tag.
func sealLine(g cipher.AEAD, hdrHash []byte, seq uint64, prevTag []byte, rec record) (line string, tag []byte) {
	pt := mustJSON(rec)
	nonce := make([]byte, nonceSize)
	mustRand(nonce)
	ct := g.Seal(nil, nonce, pt, aad(hdrHash, seq, prevTag))
	raw := append(append([]byte(nil), nonce...), ct...)
	return base64.StdEncoding.EncodeToString(raw), ct[len(ct)-tagSize:]
}

// openLine opens one record line. Any failure (malformed, wrong key, broken
// chain, tampering) is reported as domain.ErrBadPassword - indistinguishable.
func openLine(g cipher.AEAD, hdrHash []byte, seq uint64, prevTag []byte, line string) (record, []byte, error) {
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) < nonceSize+tagSize {
		return record{}, nil, domain.ErrBadPassword
	}
	nonce, ct := raw[:nonceSize], raw[nonceSize:]
	pt, err := g.Open(nil, nonce, ct, aad(hdrHash, seq, prevTag))
	if err != nil {
		return record{}, nil, domain.ErrBadPassword
	}
	var rec record
	if err := json.Unmarshal(pt, &rec); err != nil {
		// Reachable only AFTER a valid GCM tag (correct key, untampered bytes):
		// a malformed payload here means an internal encoding bug, never an
		// attacker-distinguishable path - so it stays distinct from ErrBadPassword.
		return record{}, nil, fmt.Errorf("unreadable record: %w", err)
	}
	return rec, ct[len(ct)-tagSize:], nil
}

// headPayload is the authenticated trailer committing the record count and the
// final tag, so truncation (dropping trailing records) is detected.
type headPayload struct {
	Count int    `json:"count"`
	Head  []byte `json:"head"`
}

func headAAD(hdrHash []byte, count int) []byte {
	b := append(append([]byte(nil), hdrHash...), []byte("finador-head")...)
	return binary.BigEndian.AppendUint64(b, uint64(count))
}

func sealHead(g cipher.AEAD, hdrHash []byte, count int, lastTag []byte) string {
	pt := mustJSON(headPayload{Count: count, Head: lastTag})
	nonce := make([]byte, nonceSize)
	mustRand(nonce)
	ct := g.Seal(nil, nonce, pt, headAAD(hdrHash, count))
	raw := append(append([]byte(nil), nonce...), ct...)
	return base64.StdEncoding.EncodeToString(raw)
}

func openHead(g cipher.AEAD, hdrHash []byte, count int, line string) (headPayload, error) {
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) < nonceSize+tagSize {
		return headPayload{}, domain.ErrBadPassword
	}
	pt, err := g.Open(nil, raw[:nonceSize], raw[nonceSize:], headAAD(hdrHash, count))
	if err != nil {
		return headPayload{}, domain.ErrBadPassword
	}
	var hp headPayload
	if err := json.Unmarshal(pt, &hp); err != nil {
		return headPayload{}, domain.ErrBadPassword
	}
	return hp, nil
}
