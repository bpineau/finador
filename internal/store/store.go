// Package store reads and writes the single encrypted portfolio file.
//
// Layout: magic ‖ version ‖ argon2 params ‖ salt ‖ nonce ‖ AES-256-GCM(gzip(JSON)).
// The clear header is passed as GCM additional data, so it is authenticated:
// any byte flipped anywhere in the file fails decryption.
package store

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"

	"golang.org/x/crypto/argon2"

	"finador/internal/domain"
)

const (
	magic      = "FINADOR1"
	headerSize = len(magic) + 1 + 4 + 4 + 1 + 16
	nonceSize  = 12
)

// File is an open, decrypted portfolio file.
type File struct {
	Path string
	Book *domain.Book

	key [32]byte
	hdr header
}

// header is the clear, authenticated prefix of the file.
type header struct {
	Version  uint8
	Time     uint32 // passes Argon2id
	MemoryKB uint32
	Threads  uint8
	Salt     [16]byte
}

func defaultHeader() header {
	h := header{Version: 1, Time: 3, MemoryKB: 64 * 1024, Threads: uint8(min(4, runtime.NumCPU()))}
	mustRand(h.Salt[:])
	return h
}

func mustRand(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic(err) // plus de CSPRNG système : rien de sensé à faire
	}
}

func (h header) deriveKey(password string) [32]byte {
	return [32]byte(argon2.IDKey([]byte(password), h.Salt[:], h.Time, h.MemoryKB, h.Threads, 32))
}

func (h header) encode() []byte {
	b := make([]byte, 0, headerSize)
	b = append(b, magic...)
	b = append(b, h.Version)
	b = binary.BigEndian.AppendUint32(b, h.Time)
	b = binary.BigEndian.AppendUint32(b, h.MemoryKB)
	b = append(b, h.Threads)
	b = append(b, h.Salt[:]...)
	return b
}

func decodeHeader(path string, raw []byte) (header, error) {
	if len(raw) < headerSize+nonceSize || string(raw[:len(magic)]) != magic {
		return header{}, fmt.Errorf("%s n'est pas un fichier finador", path)
	}
	rest := raw[len(magic):]
	h := header{
		Version:  rest[0],
		Time:     binary.BigEndian.Uint32(rest[1:5]),
		MemoryKB: binary.BigEndian.Uint32(rest[5:9]),
		Threads:  rest[9],
	}
	copy(h.Salt[:], rest[10:26])
	// Les paramètres sont lus avant toute authentification (la clé en dérive) :
	// des bornes strictes évitent panique et bombe mémoire sur fichier forgé.
	if h.Time < 1 || h.Time > 16 ||
		h.MemoryKB < 8 || h.MemoryKB > 1<<20 || // ≤ 1 GiB
		h.Threads < 1 || h.Threads > 16 {
		return header{}, fmt.Errorf("%s: paramètres Argon2 hors bornes", path)
	}
	if h.Version != 1 {
		return header{}, fmt.Errorf("%s: version %d non gérée (finador trop ancien ?)", path, h.Version)
	}
	return h, nil
}

// Create makes a new encrypted file holding an empty Book. It refuses to overwrite.
func Create(path, password string) (*File, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s existe déjà", path)
	}
	f := &File{Path: path, Book: domain.NewBook(), hdr: defaultHeader()}
	f.key = f.hdr.deriveKey(password)
	return f, f.Save()
}

// Open reads and decrypts an existing file. A wrong password and a tampered
// file are indistinguishable by construction: both yield domain.ErrBadPassword.
func Open(path, password string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s n'existe pas — lancez 'finador init' pour le créer", path)
		}
		return nil, err
	}
	hdr, err := decodeHeader(path, raw)
	if err != nil {
		return nil, err
	}
	f := &File{Path: path, hdr: hdr, key: hdr.deriveKey(password)}
	nonce := raw[headerSize : headerSize+nonceSize]
	plain, err := f.gcm().Open(nil, nonce, raw[headerSize+nonceSize:], raw[:headerSize])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, domain.ErrBadPassword)
	}
	zr, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, fmt.Errorf("%s: contenu illisible: %w", path, err)
	}
	defer zr.Close()
	book := domain.NewBook()
	if err := json.NewDecoder(zr).Decode(book); err != nil {
		return nil, fmt.Errorf("%s: contenu illisible: %w", path, err)
	}
	f.Book = book
	return f, nil
}

func (f *File) gcm() cipher.AEAD {
	block, err := aes.NewCipher(f.key[:])
	if err != nil {
		panic(err) // taille de clé fixe : ne peut pas échouer
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return g
}

// Save writes atomically: tmp + fsync + rename; the previous version becomes .bak.
func (f *File) Save() error {
	var plain bytes.Buffer
	zw := gzip.NewWriter(&plain)
	if err := json.NewEncoder(zw).Encode(f.Book); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}

	hdr := f.hdr.encode()
	out := append([]byte(nil), hdr...)
	// Nonce aléatoire de 96 bits par sauvegarde : sous la clé propre au fichier,
	// le risque de collision reste négligeable jusqu'à ~2^32 écritures (NIST).
	nonce := make([]byte, nonceSize)
	mustRand(nonce)
	out = append(out, nonce...)
	out = f.gcm().Seal(out, nonce, plain.Bytes(), hdr)

	tmp := f.Path + ".tmp"
	w, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := w.Write(out); err != nil {
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
	if _, err := os.Stat(f.Path); err == nil {
		if err := os.Rename(f.Path, f.Path+".bak"); err != nil {
			return err
		}
	}
	return os.Rename(tmp, f.Path)
}
