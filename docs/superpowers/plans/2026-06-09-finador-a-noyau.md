# Finador phase A — noyau chiffré & ledger : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Le socle de finador : types métier purs, fichier unique chiffré (Argon2id + AES-256-GCM), cache de mot de passe Keychain macOS, et la CLI complète de saisie/import du ledger patrimonial.

**Architecture:** `internal/domain` (types purs, zéro I/O) ← `internal/store` (conteneur chiffré) + `internal/keyring` (mot de passe) ← `internal/cli` (cobra, façade mince). Tout l'état dérivé se recalcule depuis le ledger ; le fichier `.fin` est un snapshot `en-tête ‖ AES-256-GCM(gzip(JSON))` écrit atomiquement.

**Tech Stack:** Go 1.26 pur (zéro CGo), spf13/cobra, samber/lo, shopspring/decimal, golang.org/x/crypto (argon2), golang.org/x/term.

**Référence:** spec `docs/superpowers/specs/2026-06-09-finador-design.md` (§2, §3, §4, §7, §9, §10).

**Conventions de tous les tasks :**
- Tests : stdlib `testing` seul, pas de framework d'assertion.
- Après chaque implémentation : `gofmt -l .` doit être vide et `go vet ./...` silencieux.
- Les tests de `store/` et `cli/` prennent ~2-5 s : Argon2id est volontairement lent, c'est attendu.
- Décision actée phase A : exit code 1 pour toute erreur (la distinction 1 usage / 2 interne de la spec arrive quand il existera de vrais chemins internes — réseau, rendu).

---

### Task 1: Bootstrap du module

**Files:**
- Create: `go.mod`, `.gitignore`, `cmd/finador/main.go`

- [ ] **Step 1: Vérifier l'outillage**

Run: `go version`
Expected: `go1.26` ou plus. Sinon, STOP : demander à l'utilisateur d'installer Go ≥ 1.26.

- [ ] **Step 2: Initialiser le module et le squelette**

```bash
cd /Users/ben/projects/finador
go mod init finador
```

Create `.gitignore`:

```gitignore
bin/
*.fin
*.fin.bak
*.fin.tmp
```

Create `cmd/finador/main.go` (stub, remplacé en Task 8) :

```go
package main

func main() {}
```

- [ ] **Step 3: Vérifier que ça compile**

Run: `go build ./... && gofmt -l .`
Expected: succès, aucune sortie gofmt.

- [ ] **Step 4: Commit**

```bash
git add go.mod .gitignore cmd/
git commit -m "chore: bootstrap du module finador"
```

---

### Task 2: domain — Date civile, Currency, Money

**Files:**
- Create: `internal/domain/date.go`, `internal/domain/money.go`
- Test: `internal/domain/date_test.go`, `internal/domain/money_test.go`

- [ ] **Step 1: Écrire les tests qui échouent**

`internal/domain/date_test.go`:

```go
package domain

import (
	"encoding/json"
	"testing"
)

func TestParseDate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		ok   bool
		want string
	}{
		{"2026-06-01", true, "2026-06-01"},
		{"2026-2-1", false, ""},
		{"hier", false, ""},
		{"", false, ""},
	} {
		d, err := ParseDate(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseDate(%q): err=%v, ok attendu=%v", tc.in, err, tc.ok)
		}
		if tc.ok && d.String() != tc.want {
			t.Errorf("ParseDate(%q) = %s, attendu %s", tc.in, d, tc.want)
		}
	}
}

func TestDateOrdering(t *testing.T) {
	a, _ := ParseDate("2026-01-31")
	b, _ := ParseDate("2026-02-01")
	if !a.Before(b) || b.Before(a) || a.Before(a) {
		t.Errorf("ordre incorrect entre %s et %s", a, b)
	}
}

func TestDateJSON(t *testing.T) {
	d, _ := ParseDate("2026-06-01")
	raw, err := json.Marshal(struct{ D Date }{d})
	if err != nil || string(raw) != `{"D":"2026-06-01"}` {
		t.Fatalf("marshal = %s, err=%v", raw, err)
	}
	var back struct{ D Date }
	if err := json.Unmarshal(raw, &back); err != nil || back.D != d {
		t.Fatalf("unmarshal = %+v, err=%v", back.D, err)
	}
}
```

`internal/domain/money_test.go`:

```go
package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestMoneyString(t *testing.T) {
	m := Money{Amount: decimal.RequireFromString("5500.50"), Currency: EUR}
	if got := m.String(); got != "5500.5 EUR" {
		t.Errorf("String() = %q", got)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go get github.com/shopspring/decimal@latest && go test ./internal/domain/`
Expected: FAIL — `undefined: ParseDate`, `undefined: Money`.

- [ ] **Step 3: Implémenter**

`internal/domain/date.go`:

```go
package domain

import (
	"fmt"
	"time"
)

// Date is a civil day — no clock, no time zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("date %q (attendu AAAA-MM-JJ): %w", s, err)
	}
	return DateOf(t), nil
}

func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

func Today() Date { return DateOf(time.Now()) }

func (d Date) String() string { return d.Time().Format("2006-01-02") }

// Time renders the date as midnight UTC, for arithmetic and ordering.
func (d Date) Time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

func (d Date) Before(o Date) bool { return d.Time().Before(o.Time()) }
func (d Date) IsZero() bool       { return d == Date{} }

func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Date) UnmarshalText(b []byte) error {
	parsed, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
```

`internal/domain/money.go`:

```go
package domain

import "github.com/shopspring/decimal"

type Currency string

const (
	EUR Currency = "EUR"
	USD Currency = "USD"
)

// Money is an exact amount in a given currency. Ledger amounts are always
// decimal; performance math (phase C) works on float64 instead.
type Money struct {
	Amount   decimal.Decimal `json:"amount"`
	Currency Currency        `json:"ccy"`
}

func (m Money) String() string { return m.Amount.String() + " " + string(m.Currency) }
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/domain/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain go.mod go.sum
git commit -m "feat(domain): Date civile, Currency et Money"
```

---

### Task 3: domain — TaxRule, Slugify, Account

**Files:**
- Create: `internal/domain/tax.go`, `internal/domain/slug.go`, `internal/domain/account.go`
- Test: `internal/domain/tax_test.go`, `internal/domain/slug_test.go`

- [ ] **Step 1: Écrire les tests qui échouent**

`internal/domain/tax_test.go`:

```go
package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseTaxRule(t *testing.T) {
	for _, tc := range []struct {
		in   string
		ok   bool
		mode TaxMode
		rate string // taux décimal attendu
	}{
		{"none", true, TaxNone, "0"},
		{"", true, TaxNone, "0"},
		{"gains:17.2%", true, TaxOnGains, "0.172"},
		{"value:20%", true, TaxOnValue, "0.2"},
		{"gains:30", true, TaxOnGains, "0.3"}, // le % est optionnel
		{"plusvalue:30%", false, 0, ""},
		{"gains:abc%", false, 0, ""},
	} {
		r, err := ParseTaxRule(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseTaxRule(%q): err=%v, ok attendu=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && (r.Mode != tc.mode || !r.Rate.Equal(decimal.RequireFromString(tc.rate))) {
			t.Errorf("ParseTaxRule(%q) = %+v", tc.in, r)
		}
	}
}

func TestTaxRuleRoundTrip(t *testing.T) {
	for _, s := range []string{"none", "gains:17.2%", "value:20%"} {
		r, err := ParseTaxRule(s)
		if err != nil {
			t.Fatalf("ParseTaxRule(%q): %v", s, err)
		}
		if r.String() != s {
			t.Errorf("String() = %q, attendu %q", r.String(), s)
		}
	}
}
```

`internal/domain/slug_test.go`:

```go
package domain

import "testing"

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"PEA Zephyr":     "pea-zephyr",
		"CTO Meridia":         "cto-meridia",
		"Maison à Achères": "maison-a-acheres",
		"CW8.PA":           "cw8-pa",
		"  défi  élevé!  ": "defi-eleve",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, attendu %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/domain/`
Expected: FAIL — `undefined: ParseTaxRule`, `undefined: Slugify`.

- [ ] **Step 3: Implémenter**

`internal/domain/tax.go`:

```go
package domain

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type TaxMode uint8

const (
	TaxNone TaxMode = iota
	TaxOnGains
	TaxOnValue
)

// TaxRule estimates the latent tax of an envelope.
// TaxOnGains taxes the value beyond the contribution basis (PEA, CTO, AV);
// TaxOnValue taxes the whole value (PER deducted at entry).
type TaxRule struct {
	Mode TaxMode
	Rate decimal.Decimal // 0.172 pour 17,2 %
}

var hundred = decimal.NewFromInt(100)

// ParseTaxRule reads "none", "gains:17.2%" or "value:20%" (the % is optional).
func ParseTaxRule(s string) (TaxRule, error) {
	if s == "" || s == "none" {
		return TaxRule{}, nil
	}
	mode, pct, ok := strings.Cut(s, ":")
	if !ok {
		return TaxRule{}, fmt.Errorf("règle fiscale %q: attendu none, gains:N%% ou value:N%%", s)
	}
	rate, err := decimal.NewFromString(strings.TrimSuffix(pct, "%"))
	if err != nil {
		return TaxRule{}, fmt.Errorf("règle fiscale %q: taux invalide: %w", s, err)
	}
	rule := TaxRule{Rate: rate.Div(hundred)}
	switch mode {
	case "gains":
		rule.Mode = TaxOnGains
	case "value":
		rule.Mode = TaxOnValue
	default:
		return TaxRule{}, fmt.Errorf("règle fiscale %q: mode %q inconnu", s, mode)
	}
	return rule, nil
}

func (r TaxRule) String() string {
	pct := r.Rate.Mul(hundred).String() + "%"
	switch r.Mode {
	case TaxOnGains:
		return "gains:" + pct
	case TaxOnValue:
		return "value:" + pct
	default:
		return "none"
	}
}

func (r TaxRule) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

func (r *TaxRule) UnmarshalText(b []byte) error {
	parsed, err := ParseTaxRule(string(b))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}
```

`internal/domain/slug.go`:

```go
package domain

import "strings"

var accents = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c",
)

// Slugify turns a free-form name into a stable identifier:
// "PEA Zephyr" → "pea-zephyr", "Maison à Achères" → "maison-a-acheres".
func Slugify(name string) string {
	s := accents.Replace(strings.ToLower(name))
	var out []rune
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			out = append(out, r)
		case len(out) > 0 && out[len(out)-1] != '-':
			out = append(out, '-')
		}
	}
	return strings.TrimSuffix(string(out), "-")
}
```

`internal/domain/account.go`:

```go
package domain

type AccountID string

// Account is an envelope: where assets are held, and how it is taxed.
// Name is free-form ("PEA Zephyr"); ID is its stable slug.
type Account struct {
	ID       AccountID `json:"id"`
	Name     string    `json:"name"`
	Currency Currency  `json:"ccy"`
	Tax      TaxRule   `json:"tax"`
}
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/domain/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain
git commit -m "feat(domain): TaxRule par enveloppe, Slugify, Account"
```

---

### Task 4: domain — Asset, TxKind, Transaction

**Files:**
- Create: `internal/domain/asset.go`, `internal/domain/tx.go`
- Test: `internal/domain/tx_test.go`

- [ ] **Step 1: Écrire les tests qui échouent**

`internal/domain/tx_test.go`:

```go
package domain

import (
	"encoding/json"
	"testing"
)

func TestParseTxKind(t *testing.T) {
	for in, want := range map[string]TxKind{
		"buy": Buy, "SELL": Sell, "dividend": Dividend, "fee": Fee,
		"deposit": Deposit, "withdraw": Withdraw, "statement": Statement,
	} {
		k, err := ParseTxKind(in)
		if err != nil || k != want {
			t.Errorf("ParseTxKind(%q) = %v, %v", in, k, err)
		}
	}
	if _, err := ParseTxKind("achat"); err == nil {
		t.Error("ParseTxKind(\"achat\") aurait dû échouer")
	}
}

func TestTxKindJSON(t *testing.T) {
	raw, err := json.Marshal(Buy)
	if err != nil || string(raw) != `"buy"` {
		t.Fatalf("marshal = %s, err=%v", raw, err)
	}
	var k TxKind
	if err := json.Unmarshal([]byte(`"statement"`), &k); err != nil || k != Statement {
		t.Fatalf("unmarshal = %v, err=%v", k, err)
	}
}

func TestParseAssetKind(t *testing.T) {
	if k, err := ParseAssetKind("security"); err != nil || k != Security {
		t.Errorf("security: %v, %v", k, err)
	}
	if k, err := ParseAssetKind("property"); err != nil || k != Property {
		t.Errorf("property: %v, %v", k, err)
	}
	if _, err := ParseAssetKind("crypto"); err == nil {
		t.Error("crypto aurait dû échouer")
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go get github.com/samber/lo@latest && go test ./internal/domain/`
Expected: FAIL — `undefined: ParseTxKind`, etc.

- [ ] **Step 3: Implémenter**

`internal/domain/asset.go`:

```go
package domain

import "fmt"

type AssetKind uint8

const (
	Security AssetKind = iota + 1 // coté : valorisé au cours de marché
	Property                      // bien : valorisé par estimations datées
)

func ParseAssetKind(s string) (AssetKind, error) {
	switch s {
	case "security":
		return Security, nil
	case "property":
		return Property, nil
	}
	return 0, fmt.Errorf("type d'actif %q: attendu security ou property", s)
}

func (k AssetKind) String() string {
	switch k {
	case Security:
		return "security"
	case Property:
		return "property"
	}
	return fmt.Sprintf("AssetKind(%d)", uint8(k))
}

func (k AssetKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

func (k *AssetKind) UnmarshalText(b []byte) error {
	parsed, err := ParseAssetKind(string(b))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

type AssetID string

// Asset is anything owned: a quoted security or a free-form property.
// Cash is not an asset — it belongs to each Account.
type Asset struct {
	ID       AssetID   `json:"id"`
	Kind     AssetKind `json:"kind"`
	Name     string    `json:"name"`
	Ticker   string    `json:"ticker,omitempty"` // symbole Yahoo ("CW8.PA")
	ISIN     string    `json:"isin,omitempty"`
	Aliases  []string  `json:"aliases,omitempty"`
	Currency Currency  `json:"ccy"`
	Group    string    `json:"group,omitempty"` // chemin hiérarchique : "actions/us/tech"
}
```

`internal/domain/tx.go`:

```go
package domain

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

type TxKind uint8

const (
	Buy TxKind = iota + 1
	Sell
	Dividend
	Fee
	Deposit   // apport externe : alimente la base fiscale et le XIRR
	Withdraw  // retrait externe
	Statement // solde de cash ou estimation de bien, constaté à une date
)

var txKindNames = map[TxKind]string{
	Buy: "buy", Sell: "sell", Dividend: "dividend", Fee: "fee",
	Deposit: "deposit", Withdraw: "withdraw", Statement: "statement",
}

var txKindByName = lo.Invert(txKindNames)

func ParseTxKind(s string) (TxKind, error) {
	k, ok := txKindByName[strings.ToLower(s)]
	if !ok {
		return 0, fmt.Errorf("type de transaction %q inconnu", s)
	}
	return k, nil
}

func (k TxKind) String() string { return txKindNames[k] }

func (k TxKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

func (k *TxKind) UnmarshalText(b []byte) error {
	parsed, err := ParseTxKind(string(b))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

type TxID uint64

// Transaction is one immutable-by-default ledger line; everything derived
// (positions, bases fiscales, séries) is recomputed from the ledger.
// Quantity and Amount are always positive: Kind carries the direction.
type Transaction struct {
	ID         TxID            `json:"id"`
	Date       Date            `json:"date"`
	Account    AccountID       `json:"account"`
	Asset      AssetID         `json:"asset,omitempty"` // vide : cash pur du compte
	Kind       TxKind          `json:"kind"`
	Quantity   decimal.Decimal `json:"qty"`
	Amount     Money           `json:"amount"`
	Note       string          `json:"note,omitempty"`
	ImportHash string          `json:"importHash,omitempty"`
}
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/domain/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain go.mod go.sum
git commit -m "feat(domain): Asset, TxKind et Transaction"
```

---

### Task 5: domain — Book (l'état complet, résolution de références)

**Files:**
- Create: `internal/domain/errors.go`, `internal/domain/book.go`
- Test: `internal/domain/book_test.go`

- [ ] **Step 1: Écrire les tests qui échouent**

`internal/domain/book_test.go`:

```go
package domain

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func sampleBook(t *testing.T) *Book {
	t.Helper()
	b := NewBook()
	if err := b.AddAccount(&Account{ID: "pea-zephyr", Name: "PEA Zephyr", Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddAsset(&Asset{
		ID: "cw8", Kind: Security, Name: "Amundi MSCI World", Ticker: "CW8.PA",
		ISIN: "LU1681043599", Aliases: []string{"world"}, Currency: EUR, Group: "actions/monde",
	}); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestResolveAccount(t *testing.T) {
	b := sampleBook(t)
	for _, ref := range []string{"pea-zephyr", "PEA Zephyr", "pea zephyr"} {
		if _, err := b.Account(ref); (ref == "pea zephyr") == (err == nil) {
			// "pea zephyr" ne matche ni l'ID ni le nom exact → ErrNotFound
			t.Errorf("Account(%q): err=%v", ref, err)
		}
	}
	if _, err := b.Account("inconnu"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Account(inconnu): %v, attendu ErrNotFound", err)
	}
}

func TestResolveAssetTiers(t *testing.T) {
	b := sampleBook(t)
	for _, ref := range []string{"cw8", "CW8.PA", "lu1681043599", "WORLD", "amundi msci world"} {
		if _, err := b.Asset(ref); err != nil {
			t.Errorf("Asset(%q): %v", ref, err)
		}
	}
	if _, err := b.Asset(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Asset(\"\"): %v, attendu ErrNotFound", err)
	}
}

func TestResolveAmbiguous(t *testing.T) {
	b := sampleBook(t)
	if err := b.AddAsset(&Asset{ID: "cw8-cto", Kind: Security, Name: "CW8 bis",
		Aliases: []string{"world"}, Currency: EUR}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Asset("world"); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("Asset(world): %v, attendu ErrAmbiguous", err)
	}
	// le tier ID gagne avant que le tier alias ne devienne ambigu
	if _, err := b.Asset("cw8"); err != nil {
		t.Errorf("Asset(cw8): %v", err)
	}
}

func TestDuplicates(t *testing.T) {
	b := sampleBook(t)
	if err := b.AddAccount(&Account{ID: "pea-zephyr", Name: "Autre"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("ID dupliqué: %v", err)
	}
	if err := b.AddAccount(&Account{ID: "autre", Name: "PEA Zephyr"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("nom dupliqué: %v", err)
	}
}

func TestLedger(t *testing.T) {
	b := sampleBook(t)
	d, _ := ParseDate("2026-06-01")
	tx := b.Add(Transaction{Date: d, Account: "pea-zephyr", Asset: "cw8", Kind: Buy,
		Quantity: decimal.NewFromInt(10),
		Amount:   Money{Amount: decimal.NewFromInt(5500), Currency: EUR}})
	if tx.ID != 1 {
		t.Fatalf("premier ID = %d", tx.ID)
	}
	if tx2 := b.Add(Transaction{Date: d, Account: "pea-zephyr", Kind: Deposit,
		Amount: Money{Amount: decimal.NewFromInt(1000), Currency: EUR}}); tx2.ID != 2 {
		t.Fatalf("second ID = %d", tx2.ID)
	}
	if err := b.RemoveTx(1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tx(1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Tx(1) après suppression: %v", err)
	}
	if _, err := b.Tx(2); err != nil {
		t.Errorf("Tx(2): %v", err)
	}
}

func TestBookJSONRoundTrip(t *testing.T) {
	b := sampleBook(t)
	d, _ := ParseDate("2026-06-01")
	b.Add(Transaction{Date: d, Account: "pea-zephyr", Asset: "cw8", Kind: Buy,
		Quantity: decimal.NewFromInt(10),
		Amount:   Money{Amount: decimal.RequireFromString("5500.50"), Currency: EUR}})
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	back := NewBook()
	if err := json.Unmarshal(raw, back); err != nil {
		t.Fatal(err)
	}
	if len(back.Accounts) != 1 || len(back.Assets) != 1 || len(back.Transactions) != 1 {
		t.Fatalf("roundtrip incomplet: %+v", back)
	}
	got := back.Transactions[0]
	if !got.Amount.Amount.Equal(decimal.RequireFromString("5500.50")) || got.Kind != Buy {
		t.Fatalf("transaction altérée: %+v", got)
	}
	if back.LastTxID != 1 {
		t.Fatalf("LastTxID = %d", back.LastTxID)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/domain/`
Expected: FAIL — `undefined: NewBook`, `undefined: ErrNotFound`.

- [ ] **Step 3: Implémenter**

`internal/domain/errors.go`:

```go
package domain

import "errors"

var (
	ErrNotFound    = errors.New("introuvable")
	ErrAmbiguous   = errors.New("référence ambiguë")
	ErrDuplicate   = errors.New("existe déjà")
	ErrBadPassword = errors.New("mot de passe incorrect ou fichier corrompu")
)
```

`internal/domain/book.go`:

```go
package domain

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
)

// Book is the whole persisted state: exactly what the encrypted file contains.
type Book struct {
	Accounts     []*Account        `json:"accounts"`
	Assets       []*Asset          `json:"assets"`
	Transactions []*Transaction    `json:"transactions"`
	Config       map[string]string `json:"config,omitempty"`
	LastTxID     TxID              `json:"lastTxId"`
}

func NewBook() *Book { return &Book{Config: map[string]string{}} }

func (b *Book) AddAccount(a *Account) error {
	for _, ref := range []string{string(a.ID), a.Name} {
		if _, err := b.Account(ref); err == nil {
			return fmt.Errorf("compte %q: %w", ref, ErrDuplicate)
		}
	}
	b.Accounts = append(b.Accounts, a)
	return nil
}

// Account resolves a reference: ID first, then free-form name — case-insensitive.
func (b *Book) Account(ref string) (*Account, error) {
	return resolve(ref, "compte", b.Accounts,
		func(a *Account) []string { return []string{string(a.ID)} },
		func(a *Account) []string { return []string{a.Name} },
	)
}

func (b *Book) AddAsset(a *Asset) error {
	if _, err := b.Asset(string(a.ID)); err == nil {
		return fmt.Errorf("actif %q: %w", a.ID, ErrDuplicate)
	}
	b.Assets = append(b.Assets, a)
	return nil
}

// Asset resolves a reference, trying tiers in order:
// ID, ticker, ISIN, alias, then name — all case-insensitive.
func (b *Book) Asset(ref string) (*Asset, error) {
	return resolve(ref, "actif", b.Assets,
		func(a *Asset) []string { return []string{string(a.ID)} },
		func(a *Asset) []string { return []string{a.Ticker} },
		func(a *Asset) []string { return []string{a.ISIN} },
		func(a *Asset) []string { return a.Aliases },
		func(a *Asset) []string { return []string{a.Name} },
	)
}

// resolve returns the single item matching ref on the first tier that yields
// any match; several matches on the same tier is an ambiguity.
func resolve[T any](ref, what string, items []*T, tiers ...func(*T) []string) (*T, error) {
	if ref == "" {
		return nil, fmt.Errorf("%s (référence vide): %w", what, ErrNotFound)
	}
	for _, tier := range tiers {
		matches := lo.Filter(items, func(it *T, _ int) bool {
			return lo.SomeBy(tier(it), func(s string) bool {
				return s != "" && strings.EqualFold(s, ref)
			})
		})
		switch len(matches) {
		case 0: // tier suivant
		case 1:
			return matches[0], nil
		default:
			return nil, fmt.Errorf("%s %q: %w", what, ref, ErrAmbiguous)
		}
	}
	return nil, fmt.Errorf("%s %q: %w", what, ref, ErrNotFound)
}

// Add appends t to the ledger with a fresh ID and returns the stored transaction.
func (b *Book) Add(t Transaction) *Transaction {
	b.LastTxID++
	t.ID = b.LastTxID
	stored := &t
	b.Transactions = append(b.Transactions, stored)
	return stored
}

func (b *Book) Tx(id TxID) (*Transaction, error) {
	tx, ok := lo.Find(b.Transactions, func(t *Transaction) bool { return t.ID == id })
	if !ok {
		return nil, fmt.Errorf("transaction %d: %w", id, ErrNotFound)
	}
	return tx, nil
}

func (b *Book) RemoveTx(id TxID) error {
	if _, err := b.Tx(id); err != nil {
		return err
	}
	b.Transactions = lo.Reject(b.Transactions, func(t *Transaction, _ int) bool { return t.ID == id })
	return nil
}

func (b *Book) HasImportHash(h string) bool {
	return h != "" && lo.SomeBy(b.Transactions, func(t *Transaction) bool { return t.ImportHash == h })
}
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/domain/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain
git commit -m "feat(domain): Book — état complet, résolution de références, ledger"
```

---

### Task 6: store — le conteneur chiffré

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

Format du fichier : `magic "FINADOR1" (8) ‖ version (1) ‖ time (4 BE) ‖ memKiB (4 BE) ‖ threads (1) ‖ sel (16) ‖ nonce (12) ‖ AES-256-GCM(gzip(JSON(Book)))`. L'en-tête sert d'AAD : il est authentifié par GCM. Nonce régénéré à chaque Save. Mauvais mot de passe et fichier altéré sont indistinguables → `domain.ErrBadPassword`.

- [ ] **Step 1: Écrire les tests qui échouent**

`internal/store/store_test.go`:

```go
package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finador/internal/domain"
)

func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.fin")
}

func TestCreateOpenRoundTrip(t *testing.T) {
	path := tmpPath(t)
	f, err := Create(path, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Book.AddAccount(&domain.Account{ID: "pea", Name: "PEA", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := Open(path, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Book.Accounts) != 1 || back.Book.Accounts[0].Name != "PEA" {
		t.Fatalf("contenu perdu: %+v", back.Book)
	}
}

func TestWrongPassword(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "bon"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "mauvais"); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("attendu ErrBadPassword, eu: %v", err)
	}
}

func TestTamperedFile(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "s3cret"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF // altère le dernier octet du sceau
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "s3cret"); !errors.Is(err, domain.ErrBadPassword) {
		t.Fatalf("attendu ErrBadPassword, eu: %v", err)
	}
}

func TestNotAFinadorFile(t *testing.T) {
	path := tmpPath(t)
	if err := os.WriteFile(path, []byte("PK\x03\x04 pas finador du tout"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, "s3cret"); err == nil || !strings.Contains(err.Error(), "finador") {
		t.Fatalf("attendu erreur de format, eu: %v", err)
	}
}

func TestCreateRefusesExisting(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(path, "b"); err == nil {
		t.Fatal("Create aurait dû refuser d'écraser")
	}
}

func TestSaveKeepsBackup(t *testing.T) {
	path := tmpPath(t)
	f, err := Create(path, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Book.AddAccount(&domain.Account{ID: "v2", Name: "V2", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	bak, err := Open(path+".bak", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(bak.Book.Accounts) != 0 {
		t.Fatalf(".bak devrait être l'état précédent (vide), eu %d comptes", len(bak.Book.Accounts))
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go get golang.org/x/crypto@latest && go test ./internal/store/`
Expected: FAIL — `undefined: Create`, `undefined: Open`.

- [ ] **Step 3: Implémenter**

`internal/store/store.go`:

```go
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
	"fmt"
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

	out := f.hdr.encode()
	nonce := make([]byte, nonceSize)
	mustRand(nonce)
	out = append(out, nonce...)
	out = f.gcm().Seal(out, nonce, plain.Bytes(), f.hdr.encode())

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
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/store/`
Expected: PASS (≈ 2-4 s : 8 dérivations Argon2id, c'est volontairement lent).

- [ ] **Step 5: Commit**

```bash
git add internal/store go.mod go.sum
git commit -m "feat(store): fichier unique chiffré Argon2id + AES-256-GCM, save atomique + .bak"
```

---

### Task 7: keyring — mot de passe, Keychain macOS par terminal avec TTL

**Files:**
- Create: `internal/keyring/keyring.go`, `internal/keyring/keychain.go`, `internal/keyring/tty_unix.go`, `internal/keyring/tty_other.go`
- Test: `internal/keyring/keyring_test.go`

L'entrée Keychain (service `finador`, account `<db>@<tty>`) stocke `expiry-unix\npassword` : le TTL est figé au moment du Put (il vient de la config du Book, connue après ouverture). Tout passe par `/usr/bin/security` en `os/exec` — zéro CGo — derrière un `run` injectable pour les tests.

- [ ] **Step 1: Écrire les tests qui échouent**

`internal/keyring/keyring_test.go`:

```go
package keyring

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeRun simule /usr/bin/security sur une map account → payload.
func fakeRun(entries map[string]string) func(args ...string) (string, error) {
	return func(args ...string) (string, error) {
		switch args[0] {
		case "find-generic-password":
			if p, ok := entries[args[4]]; ok { // args: find -s finador -a <key> -w
				return p, nil
			}
			return "", errors.New("not found")
		case "add-generic-password":
			// args: add-generic-password -U -s finador -a <key> -w <payload>
			entries[args[5]] = args[7]
			return "", nil
		case "delete-generic-password":
			for k := range entries {
				delete(entries, k)
				return "", nil
			}
			return "", errors.New("empty")
		}
		return "", fmt.Errorf("commande inattendue %v", args)
	}
}

func testKeychain(entries map[string]string, now time.Time) *keychain {
	return &keychain{now: func() time.Time { return now }, run: fakeRun(entries)}
}

func TestKeychainPutGet(t *testing.T) {
	entries := map[string]string{}
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	k := testKeychain(entries, now)
	k.Put("db@tty1", "s3cret", time.Hour)

	if pw, ok := k.Get("db@tty1"); !ok || pw != "s3cret" {
		t.Fatalf("Get = %q, %v", pw, ok)
	}
	if _, ok := k.Get("autre@tty1"); ok {
		t.Fatal("Get d'une clé inconnue devrait échouer")
	}
}

func TestKeychainTTLExpiry(t *testing.T) {
	entries := map[string]string{}
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	k := testKeychain(entries, now)
	k.Put("db@tty1", "s3cret", time.Hour)

	k.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, ok := k.Get("db@tty1"); ok {
		t.Fatal("le mot de passe aurait dû expirer")
	}
}

func TestKeychainPurge(t *testing.T) {
	entries := map[string]string{"a": "1", "b": "2"}
	k := testKeychain(entries, time.Now())
	k.Purge()
	if len(entries) != 0 {
		t.Fatalf("Purge incomplet: %v", entries)
	}
}

func TestPasswordForEnv(t *testing.T) {
	t.Setenv("FINADOR_PASSWORD", "env-pw")
	pw, fresh, err := PasswordFor("/tmp/x.fin", Disabled(), nil)
	if err != nil || pw != "env-pw" || fresh {
		t.Fatalf("pw=%q fresh=%v err=%v", pw, fresh, err)
	}
}

func TestPasswordForPrompt(t *testing.T) {
	t.Setenv("FINADOR_PASSWORD", "")
	prompt := func(string) (string, error) { return "typed", nil }
	pw, fresh, err := PasswordFor("/tmp/x.fin", Disabled(), prompt)
	if err != nil || pw != "typed" || !fresh {
		t.Fatalf("pw=%q fresh=%v err=%v", pw, fresh, err)
	}
}

func TestKeyIsPerFileAndTerminal(t *testing.T) {
	if k := Key("/tmp/a.fin"); !strings.HasPrefix(k, "/tmp/a.fin@") {
		t.Fatalf("Key = %q", k)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go get golang.org/x/term@latest && go test ./internal/keyring/`
Expected: FAIL — `undefined: keychain`, `undefined: PasswordFor`.

- [ ] **Step 3: Implémenter**

`internal/keyring/keyring.go`:

```go
// Package keyring obtains the database password: environment, then a per-terminal
// cache (macOS Keychain), then an interactive no-echo prompt.
package keyring

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/term"
)

// Cache remembers the password of a (file, terminal) pair for a while.
type Cache interface {
	Get(key string) (password string, ok bool)
	Put(key, password string, ttl time.Duration)
	Purge()
}

// Disabled returns a cache that remembers nothing (--no-keychain, tests).
func Disabled() Cache { return nop{} }

type nop struct{}

func (nop) Get(string) (string, bool)              { return "", false }
func (nop) Put(string, string, time.Duration)      {}
func (nop) Purge()                                 {}

// Key identifies the cache slot: one per database file and terminal device,
// so each terminal gets its own grace period.
func Key(dbPath string) string { return dbPath + "@" + ttyID() }

// Prompt reads a password without echo from the controlling terminal.
func Prompt(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	defer fmt.Fprintln(os.Stderr)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	return string(pw), err
}

// PasswordFor finds the password for db: $FINADOR_PASSWORD, then cache, then
// prompt. fresh reports that the user just typed it (worth caching after a
// successful open — not before, we don't want to cache a wrong password).
func PasswordFor(db string, cache Cache, prompt func(string) (string, error)) (pw string, fresh bool, err error) {
	if pw := os.Getenv("FINADOR_PASSWORD"); pw != "" {
		return pw, false, nil
	}
	if pw, ok := cache.Get(Key(db)); ok {
		return pw, false, nil
	}
	pw, err = prompt("Mot de passe : ")
	return pw, true, err
}
```

`internal/keyring/keychain.go`:

```go
package keyring

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// System returns the platform cache: macOS Keychain entries, a no-op elsewhere.
func System() Cache {
	if runtime.GOOS != "darwin" {
		return Disabled()
	}
	return &keychain{now: time.Now, run: runSecurity}
}

const service = "finador"

// keychain stores "expiry-unix\npassword" as a Keychain generic password,
// via /usr/bin/security — no CGo. The expiry is fixed at Put time.
type keychain struct {
	now func() time.Time
	run func(args ...string) (string, error)
}

func runSecurity(args ...string) (string, error) {
	out, err := exec.Command("/usr/bin/security", args...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

func (k *keychain) Get(key string) (string, bool) {
	payload, err := k.run("find-generic-password", "-s", service, "-a", key, "-w")
	if err != nil {
		return "", false
	}
	stamp, password, ok := strings.Cut(payload, "\n")
	expiry, perr := strconv.ParseInt(stamp, 10, 64)
	if !ok || perr != nil || k.now().After(time.Unix(expiry, 0)) {
		return "", false
	}
	return password, true
}

func (k *keychain) Put(key, password string, ttl time.Duration) {
	payload := fmt.Sprintf("%d\n%s", k.now().Add(ttl).Unix(), password)
	// -U met à jour l'entrée si elle existe ; l'échec est bénin (on retapera).
	k.run("add-generic-password", "-U", "-s", service, "-a", key, "-w", payload)
}

// Purge deletes every finador entry; security removes one match per call.
func (k *keychain) Purge() {
	for {
		if _, err := k.run("delete-generic-password", "-s", service); err != nil {
			return
		}
	}
}
```

`internal/keyring/tty_unix.go`:

```go
//go:build unix

package keyring

import (
	"fmt"
	"os"
	"syscall"
)

// ttyID names the terminal device of stdin, or "notty" for pipes and scripts.
func ttyID() string {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return "notty"
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "notty"
	}
	return fmt.Sprintf("tty%d", st.Rdev)
}
```

`internal/keyring/tty_other.go`:

```go
//go:build !unix

package keyring

func ttyID() string { return "notty" }
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/keyring/ && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/keyring go.mod go.sum
git commit -m "feat(keyring): prompt sans écho + cache Keychain macOS par (fichier, terminal) avec TTL"
```

---

### Task 8: cli — racine cobra, init, account, harnais de test

**Files:**
- Create: `internal/cli/cli.go`, `internal/cli/init.go`, `internal/cli/account.go`
- Modify: `cmd/finador/main.go`
- Test: `internal/cli/cli_test.go`

- [ ] **Step 1: Écrire les tests qui échouent**

`internal/cli/cli_test.go` — le harnais sert à tous les tasks CLI suivants :

```go
package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finador/internal/cli"
)

// tryRun exécute finador contre db, mot de passe fourni par l'environnement,
// Keychain désactivé pour ne jamais toucher le vrai trousseau en test.
func tryRun(t *testing.T, db string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FINADOR_PASSWORD", "secret-de-test")
	var out bytes.Buffer
	cmd := cli.New()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--db", db, "--no-keychain"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func run(t *testing.T, db string, args ...string) string {
	t.Helper()
	out, err := tryRun(t, db, args...)
	if err != nil {
		t.Fatalf("finador %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func newDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "test.fin")
	run(t, db, "init")
	return db
}

func TestInitCreatesFile(t *testing.T) {
	db := newDB(t)
	if _, err := os.Stat(db); err != nil {
		t.Fatal(err)
	}
	if _, err := tryRun(t, db, "init"); err == nil {
		t.Fatal("init sur un fichier existant devrait échouer")
	}
}

func TestAccountAddAndList(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "account", "add", "CTO Meridia", "--tax", "gains:30%", "--ccy", "USD")
	out := run(t, db, "account", "list")
	for _, want := range []string{"pea-zephyr", "PEA Zephyr", "gains:17.2%", "cto-meridia", "USD"} {
		if !strings.Contains(out, want) {
			t.Errorf("list: %q manquant dans:\n%s", want, out)
		}
	}
	if _, err := tryRun(t, db, "account", "add", "PEA Zephyr"); err == nil {
		t.Fatal("doublon accepté")
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go get github.com/spf13/cobra@latest && go test ./internal/cli/`
Expected: FAIL — `undefined: cli.New`.

- [ ] **Step 3: Implémenter**

`internal/cli/cli.go`:

```go
// Package cli is the thin command-line facade over the finador engine.
package cli

import (
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"finador/internal/domain"
	"finador/internal/keyring"
	"finador/internal/store"
)

const defaultTTL = 12 * time.Hour

// app carries the persistent flags shared by every command.
type app struct {
	dbPath     string
	noKeychain bool
}

func New() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:           "finador",
		Short:         "Suivi de patrimoine chiffré — CLI et web, single binary",
		SilenceUsage:  true,
		SilenceErrors: true, // main les affiche, une seule fois
	}
	root.PersistentFlags().StringVar(&a.dbPath, "db", defaultDB(), "fichier de données chiffré")
	root.PersistentFlags().BoolVar(&a.noKeychain, "no-keychain", false, "ne pas mémoriser le mot de passe")
	root.AddCommand(initCmd(a), accountCmd(a))
	return root
}

func defaultDB() string {
	if p := os.Getenv("FINADOR_DB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".finador.fin")
}

func (a *app) cache() keyring.Cache {
	if a.noKeychain {
		return keyring.Disabled()
	}
	return keyring.System()
}

// open decrypts the database; a freshly typed password is cached only after a
// successful open — never cache a password that didn't decrypt anything.
func (a *app) open() (*store.File, error) {
	cache := a.cache()
	pw, fresh, err := keyring.PasswordFor(a.dbPath, cache, keyring.Prompt)
	if err != nil {
		return nil, err
	}
	f, err := store.Open(a.dbPath, pw)
	if err != nil {
		return nil, err
	}
	if fresh {
		cache.Put(keyring.Key(a.dbPath), pw, configTTL(f.Book))
	}
	return f, nil
}

// mutate opens, applies fn to the book, then saves atomically.
// If fn fails, nothing is written.
func (a *app) mutate(fn func(*domain.Book) error) error {
	f, err := a.open()
	if err != nil {
		return err
	}
	if err := fn(f.Book); err != nil {
		return err
	}
	return f.Save()
}

// configTTL reads the Keychain TTL from the book config ("keychain-ttl": "8h"),
// defaulting to 12h.
func configTTL(b *domain.Book) time.Duration {
	if d, err := time.ParseDuration(b.Config["keychain-ttl"]); err == nil && d > 0 {
		return d
	}
	return defaultTTL
}
```

`internal/cli/init.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"finador/internal/keyring"
	"finador/internal/store"
)

func initCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Crée le fichier de données chiffré",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw := os.Getenv("FINADOR_PASSWORD")
			if pw == "" {
				var err error
				if pw, err = askTwice(); err != nil {
					return err
				}
			}
			if _, err := store.Create(a.dbPath, pw); err != nil {
				return err
			}
			a.cache().Put(keyring.Key(a.dbPath), pw, defaultTTL)
			fmt.Fprintf(cmd.OutOrStdout(), "Créé %s\n", a.dbPath)
			return nil
		},
	}
}

func askTwice() (string, error) {
	p1, err := keyring.Prompt("Mot de passe : ")
	if err != nil {
		return "", err
	}
	p2, err := keyring.Prompt("Confirmez : ")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.New("les mots de passe diffèrent")
	}
	if p1 == "" {
		return "", errors.New("mot de passe vide refusé")
	}
	return p1, nil
}
```

`internal/cli/account.go`:

```go
package cli

import (
	"cmp"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func accountCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "account", Short: "Gère les enveloppes (PEA, CTO, PER, comptes bancaires…)"}
	cmd.AddCommand(accountAdd(a), accountList(a))
	return cmd
}

func accountAdd(a *app) *cobra.Command {
	var tax, ccy, id string
	cmd := &cobra.Command{
		Use:   "add <nom>",
		Short: "Crée une enveloppe — le nom est libre : \"PEA Zephyr\", \"CTO Meridia\"…",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := domain.ParseTaxRule(tax)
			if err != nil {
				return err
			}
			acc := &domain.Account{
				ID:       domain.AccountID(cmp.Or(id, domain.Slugify(args[0]))),
				Name:     args[0],
				Currency: domain.Currency(ccy),
				Tax:      rule,
			}
			return a.mutate(func(b *domain.Book) error {
				if err := b.AddAccount(acc); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Compte %s (%s) créé\n", acc.Name, acc.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&tax, "tax", "none", "règle fiscale : none, gains:17.2%, value:20%")
	cmd.Flags().StringVar(&ccy, "ccy", "EUR", "devise du compte")
	cmd.Flags().StringVar(&id, "id", "", "identifiant (défaut : slug du nom)")
	return cmd
}

func accountList(a *app) *cobra.Command {
	return &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNOM\tDEVISE\tFISCALITÉ")
			for _, acc := range f.Book.Accounts {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", acc.ID, acc.Name, acc.Currency, acc.Tax)
			}
			return w.Flush()
		},
	}
}
```

`cmd/finador/main.go` (remplace le stub) :

```go
package main

import (
	"fmt"
	"os"

	"finador/internal/cli"
)

func main() {
	if err := cli.New().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "finador:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/cli/ && go build ./...`
Expected: PASS (quelques secondes : chaque commande dérive la clé Argon2id).

- [ ] **Step 5: Commit**

```bash
git add internal/cli cmd go.mod go.sum
git commit -m "feat(cli): racine cobra, init, account add/list"
```

---

### Task 9: cli — asset add/set/list, résolution d'enveloppe par défaut

**Files:**
- Create: `internal/cli/asset.go`, `internal/cli/helpers.go`
- Modify: `internal/cli/cli.go` (ligne AddCommand)
- Test: `internal/cli/cli_test.go` (ajout)

- [ ] **Step 1: Écrire le test qui échoue**

Ajouter à `internal/cli/cli_test.go`:

```go
func TestAssetAddSetList(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "Patrimoine")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--name", "Amundi MSCI World", "--group", "actions/monde")
	run(t, db, "asset", "add", "Maison à Achères", "--kind", "property", "--group", "immo")
	out := run(t, db, "asset", "list")
	for _, want := range []string{"cw8", "CW8.PA", "actions/monde", "maison-a-acheres", "property"} {
		if !strings.Contains(out, want) {
			t.Errorf("asset list: %q manquant dans:\n%s", want, out)
		}
	}
	// estimation datée ; l'enveloppe par défaut est l'unique compte existant
	out = run(t, db, "asset", "set", "maison-a-acheres", "450000", "--at", "2026-06-01")
	for _, want := range []string{"450000 EUR", "2026-06-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("asset set: %q manquant dans %q", want, out)
		}
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/cli/ -run TestAssetAddSetList`
Expected: FAIL — `unknown command "asset"`.

- [ ] **Step 3: Implémenter**

Dans `internal/cli/cli.go`, étendre la ligne AddCommand :

```go
	root.AddCommand(initCmd(a), accountCmd(a), assetCmd(a))
```

`internal/cli/helpers.go`:

```go
package cli

import (
	"fmt"

	"finador/internal/domain"
)

// dateOrToday parses a --at flag, empty meaning today.
func dateOrToday(s string) (domain.Date, error) {
	if s == "" {
		return domain.Today(), nil
	}
	return domain.ParseDate(s)
}

// accountFor picks the envelope of a new transaction: the --account flag, the
// account of the asset's latest transaction, the config default-account, or
// the sole existing account — in that order.
func accountFor(b *domain.Book, flag string, asset *domain.Asset) (*domain.Account, error) {
	if flag != "" {
		return b.Account(flag)
	}
	if asset != nil {
		for i := len(b.Transactions) - 1; i >= 0; i-- {
			if t := b.Transactions[i]; t.Asset == asset.ID {
				return b.Account(string(t.Account))
			}
		}
	}
	if def := b.Config["default-account"]; def != "" {
		return b.Account(def)
	}
	if len(b.Accounts) == 1 {
		return b.Accounts[0], nil
	}
	return nil, fmt.Errorf("précisez l'enveloppe avec --account: %w", domain.ErrAmbiguous)
}
```

`internal/cli/asset.go`:

```go
package cli

import (
	"cmp"
	"fmt"
	"text/tabwriter"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func assetCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "asset", Short: "Gère les actifs : titres cotés et biens"}
	cmd.AddCommand(assetAdd(a), assetSet(a), assetList(a))
	return cmd
}

func assetAdd(a *app) *cobra.Command {
	var kind, name, isin, ccy, group, id string
	var aliases []string
	cmd := &cobra.Command{
		Use:   "add <ticker|nom>",
		Short: "Déclare un actif : ticker Yahoo pour un titre, nom pour un bien",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := domain.ParseAssetKind(kind)
			if err != nil {
				return err
			}
			asset := &domain.Asset{
				Kind:     k,
				Name:     cmp.Or(name, args[0]),
				ISIN:     isin,
				Aliases:  aliases,
				Currency: domain.Currency(ccy),
				Group:    group,
			}
			if k == domain.Security {
				asset.Ticker = args[0]
			}
			asset.ID = domain.AssetID(cmp.Or(id, domain.Slugify(asset.Name)))
			return a.mutate(func(b *domain.Book) error {
				if err := b.AddAsset(asset); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Actif %s (%s) créé\n", asset.Name, asset.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "security", "security ou property")
	cmd.Flags().StringVar(&name, "name", "", "nom (défaut : l'argument)")
	cmd.Flags().StringVar(&isin, "isin", "", "code ISIN")
	cmd.Flags().StringArrayVar(&aliases, "alias", nil, "alias supplémentaire (répétable)")
	cmd.Flags().StringVar(&ccy, "ccy", "EUR", "devise de cotation")
	cmd.Flags().StringVar(&group, "group", "", "poche hiérarchique, ex. actions/us/tech")
	cmd.Flags().StringVar(&id, "id", "", "identifiant (défaut : slug du nom)")
	return cmd
}

func assetSet(a *app) *cobra.Command {
	var at, account, ccy string
	cmd := &cobra.Command{
		Use:   "set <actif> <valeur>",
		Short: "Pose une estimation datée (biens, parts non cotées)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				asset, err := b.Asset(args[0])
				if err != nil {
					return err
				}
				value, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("valeur %q: %w", args[1], err)
				}
				date, err := dateOrToday(at)
				if err != nil {
					return err
				}
				acc, err := accountFor(b, account, asset)
				if err != nil {
					return err
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Asset: asset.ID, Kind: domain.Statement,
					Amount: domain.Money{Amount: value, Currency: cmp.Or(domain.Currency(ccy), asset.Currency)},
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s = %s au %s\n", tx.ID, asset.Name, tx.Amount, tx.Date)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "date AAAA-MM-JJ (défaut : aujourd'hui)")
	cmd.Flags().StringVar(&account, "account", "", "enveloppe (nom ou id)")
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : celle de l'actif)")
	return cmd
}

func assetList(a *app) *cobra.Command {
	return &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTYPE\tNOM\tTICKER\tGROUPE\tDEVISE")
			for _, as := range f.Book.Assets {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", as.ID, as.Kind, as.Name, as.Ticker, as.Group, as.Currency)
			}
			return w.Flush()
		},
	}
}
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): asset add/set/list, enveloppe par défaut"
```

---

### Task 10: cli — add (achat/vente), cash set, deposit/withdraw

**Files:**
- Create: `internal/cli/add.go`, `internal/cli/cash.go`, `internal/cli/flows.go`
- Modify: `internal/cli/cli.go` (ligne AddCommand)
- Test: `internal/cli/cli_test.go` (ajout)

- [ ] **Step 1: Écrire le test qui échoue**

Ajouter à `internal/cli/cli_test.go`:

```go
func TestAddTradeCashAndFlows(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8", "--group", "actions/monde")

	out := run(t, db, "add", "cw8", "10", "@550", "2026-06-01")
	for _, want := range []string{"buy", "5500 EUR", "PEA Zephyr"} {
		if !strings.Contains(out, want) {
			t.Errorf("achat: %q manquant dans %q", want, out)
		}
	}
	out = run(t, db, "sell", "cw8", "4", "2310", "2026-06-05") // vente, montant total
	if !strings.Contains(out, "sell") || !strings.Contains(out, "2310 EUR") {
		t.Errorf("vente: %q", out)
	}
	// quantité négative possible via add, derrière -- (sinon pflag lit -4 comme un flag)
	out = run(t, db, "add", "--", "cw8", "-2", "@577", "2026-06-06")
	if !strings.Contains(out, "sell") || !strings.Contains(out, "1154 EUR") {
		t.Errorf("vente via qté négative: %q", out)
	}
	if _, err := tryRun(t, db, "add", "cw8", "5"); err == nil {
		t.Fatal("prix manquant accepté")
	}

	out = run(t, db, "cash", "set", "pea-zephyr", "12500")
	if !strings.Contains(out, "12500 EUR") {
		t.Errorf("cash set: %q", out)
	}
	out = run(t, db, "deposit", "PEA Zephyr", "5000", "2026-01-10")
	if !strings.Contains(out, "deposit") || !strings.Contains(out, "5000 EUR") {
		t.Errorf("deposit: %q", out)
	}
	out = run(t, db, "withdraw", "PEA Zephyr", "1000")
	if !strings.Contains(out, "withdraw") {
		t.Errorf("withdraw: %q", out)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/cli/ -run TestAddTradeCashAndFlows`
Expected: FAIL — `unknown command "add"`.

- [ ] **Step 3: Implémenter**

Dans `internal/cli/cli.go`, étendre la ligne AddCommand :

```go
	root.AddCommand(initCmd(a), accountCmd(a), assetCmd(a), addCmd(a), sellCmd(a),
		cashCmd(a), depositCmd(a), withdrawCmd(a))
```

`internal/cli/add.go` — `add` et `sell` partagent le même corps ; une quantité négative
passée à `add` (derrière `--`, sinon pflag la lit comme un flag) bascule aussi en vente :

```go
package cli

import (
	"cmp"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func addCmd(a *app) *cobra.Command {
	return tradeCmd(a, "add", domain.Buy,
		"Enregistre un achat (ou une vente : sell, ou quantité négative après --)")
}

func sellCmd(a *app) *cobra.Command {
	return tradeCmd(a, "sell", domain.Sell, "Enregistre une vente")
}

func tradeCmd(a *app, use string, kind domain.TxKind, short string) *cobra.Command {
	var account, note, ccy string
	cmd := &cobra.Command{
		Use:   use + " <actif> <quantité> [@prix-unitaire|total] [date]",
		Short: short,
		Args:  cobra.RangeArgs(2, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				asset, err := b.Asset(args[0])
				if err != nil {
					return err
				}
				qty, err := decimal.NewFromString(args[1])
				if err != nil || qty.IsZero() {
					return fmt.Errorf("quantité %q invalide", args[1])
				}
				total, date, err := parseTradeTail(args[2:], qty)
				if err != nil {
					return err
				}
				acc, err := accountFor(b, account, asset)
				if err != nil {
					return err
				}
				effective := kind
				if qty.IsNegative() {
					effective = domain.Sell
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Asset: asset.ID, Kind: effective,
					Quantity: qty.Abs(),
					Amount:   domain.Money{Amount: total, Currency: cmp.Or(domain.Currency(ccy), asset.Currency)},
					Note:     note,
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s %s × %s = %s (%s)\n",
					tx.ID, tx.Kind, asset.Name, tx.Quantity, tx.Amount, acc.Name)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "enveloppe (nom ou id)")
	cmd.Flags().StringVar(&note, "note", "", "note libre")
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise du montant (défaut : celle de l'actif)")
	return cmd
}

// parseTradeTail reads the optional price and date arguments, in any order:
// "@550" is a unit price (total = |qty| × 550), "5500" a total, "2026-06-01" the date.
func parseTradeTail(rest []string, qty decimal.Decimal) (total decimal.Decimal, date domain.Date, err error) {
	date = domain.Today()
	for _, arg := range rest {
		if unit, ok := strings.CutPrefix(arg, "@"); ok {
			p, perr := decimal.NewFromString(unit)
			if perr != nil {
				return total, date, fmt.Errorf("prix %q: %w", arg, perr)
			}
			total = p.Mul(qty.Abs())
		} else if d, derr := domain.ParseDate(arg); derr == nil {
			date = d
		} else if t, terr := decimal.NewFromString(arg); terr == nil {
			total = t.Abs()
		} else {
			return total, date, fmt.Errorf("argument %q incompris (attendu @prix, total ou date)", arg)
		}
	}
	if total.IsZero() {
		return total, date, errors.New("prix manquant : @prix-unitaire ou montant total")
	}
	return total, date, nil
}
```

`internal/cli/cash.go`:

```go
package cli

import (
	"cmp"
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func cashCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "cash", Short: "Soldes de liquidités des comptes"}
	var at, ccy string
	set := &cobra.Command{
		Use:   "set <compte> <solde>",
		Short: "Pose le solde constaté d'un compte à une date",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				acc, err := b.Account(args[0])
				if err != nil {
					return err
				}
				amount, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("solde %q: %w", args[1], err)
				}
				date, err := dateOrToday(at)
				if err != nil {
					return err
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Kind: domain.Statement,
					Amount: domain.Money{Amount: amount, Currency: cmp.Or(domain.Currency(ccy), acc.Currency)},
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s : %s au %s\n", tx.ID, acc.Name, tx.Amount, tx.Date)
				return nil
			})
		},
	}
	set.Flags().StringVar(&at, "at", "", "date AAAA-MM-JJ (défaut : aujourd'hui)")
	set.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : celle du compte)")
	cmd.AddCommand(set)
	return cmd
}
```

`internal/cli/flows.go`:

```go
package cli

import (
	"cmp"
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func depositCmd(a *app) *cobra.Command {
	return flowCmd(a, "deposit", domain.Deposit, "Apport externe vers un compte (base fiscale, XIRR)")
}

func withdrawCmd(a *app) *cobra.Command {
	return flowCmd(a, "withdraw", domain.Withdraw, "Retrait externe d'un compte")
}

func flowCmd(a *app, use string, kind domain.TxKind, short string) *cobra.Command {
	var ccy, note string
	cmd := &cobra.Command{
		Use:   use + " <compte> <montant> [date]",
		Short: short,
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				acc, err := b.Account(args[0])
				if err != nil {
					return err
				}
				amount, err := decimal.NewFromString(args[1])
				if err != nil {
					return fmt.Errorf("montant %q: %w", args[1], err)
				}
				date := domain.Today()
				if len(args) == 3 {
					if date, err = domain.ParseDate(args[2]); err != nil {
						return err
					}
				}
				tx := b.Add(domain.Transaction{
					Date: date, Account: acc.ID, Kind: kind,
					Amount: domain.Money{Amount: amount.Abs(), Currency: cmp.Or(domain.Currency(ccy), acc.Currency)},
					Note:   note,
				})
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s %s : %s le %s\n", tx.ID, tx.Kind, acc.Name, tx.Amount, tx.Date)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&ccy, "ccy", "", "devise (défaut : celle du compte)")
	cmd.Flags().StringVar(&note, "note", "", "note libre")
	return cmd
}
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): add achat/vente, cash set, deposit/withdraw"
```

---

### Task 11: cli — tx list/edit/rm

**Files:**
- Create: `internal/cli/tx.go`
- Modify: `internal/cli/cli.go` (ligne AddCommand)
- Test: `internal/cli/cli_test.go` (ajout)

- [ ] **Step 1: Écrire le test qui échoue**

Ajouter à `internal/cli/cli_test.go`:

```go
func TestTxListEditRm(t *testing.T) {
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "asset", "add", "CW8.PA", "--id", "cw8")
	run(t, db, "add", "cw8", "10", "@550", "2026-06-01")
	run(t, db, "cash", "set", "pea-zephyr", "12500", "--at", "2026-06-02")

	out := run(t, db, "tx", "list")
	if !strings.Contains(out, "buy") || !strings.Contains(out, "statement") {
		t.Fatalf("tx list:\n%s", out)
	}
	if out = run(t, db, "tx", "list", "--kind", "buy"); strings.Contains(out, "statement") {
		t.Fatalf("filtre --kind inopérant:\n%s", out)
	}

	run(t, db, "tx", "edit", "1", "--qty", "12", "--total", "6600")
	if out = run(t, db, "tx", "list", "--kind", "buy"); !strings.Contains(out, "6600 EUR") {
		t.Fatalf("edit inopérant:\n%s", out)
	}

	run(t, db, "tx", "rm", "2")
	if out = run(t, db, "tx", "list"); strings.Contains(out, "statement") {
		t.Fatalf("rm inopérant:\n%s", out)
	}
	if _, err := tryRun(t, db, "tx", "rm", "99"); err == nil {
		t.Fatal("rm d'un ID inconnu aurait dû échouer")
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/cli/ -run TestTxListEditRm`
Expected: FAIL — `unknown command "tx"`.

- [ ] **Step 3: Implémenter**

Dans `internal/cli/cli.go`, étendre la ligne AddCommand :

```go
	root.AddCommand(initCmd(a), accountCmd(a), assetCmd(a), addCmd(a), sellCmd(a),
		cashCmd(a), depositCmd(a), withdrawCmd(a), txCmd(a))
```

`internal/cli/tx.go`:

```go
package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"text/tabwriter"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func txCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "tx", Short: "Liste et corrige les transactions du ledger"}
	cmd.AddCommand(txList(a), txEdit(a), txRm(a))
	return cmd
}

func parseTxID(s string) (domain.TxID, error) {
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("identifiant de transaction %q invalide", s)
	}
	return domain.TxID(id), nil
}

func txList(a *app) *cobra.Command {
	var account, asset, kind string
	cmd := &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			b := f.Book

			var accID domain.AccountID
			if account != "" {
				acc, err := b.Account(account)
				if err != nil {
					return err
				}
				accID = acc.ID
			}
			var assetID domain.AssetID
			if asset != "" {
				as, err := b.Asset(asset)
				if err != nil {
					return err
				}
				assetID = as.ID
			}
			var k domain.TxKind
			if kind != "" {
				if k, err = domain.ParseTxKind(kind); err != nil {
					return err
				}
			}

			txs := slices.Clone(b.Transactions)
			slices.SortStableFunc(txs, func(x, y *domain.Transaction) int {
				if c := x.Date.Time().Compare(y.Date.Time()); c != 0 {
					return c
				}
				return cmp.Compare(x.ID, y.ID)
			})
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDATE\tTYPE\tCOMPTE\tACTIF\tQTÉ\tMONTANT\tNOTE")
			for _, t := range txs {
				if accID != "" && t.Account != accID ||
					assetID != "" && t.Asset != assetID ||
					k != 0 && t.Kind != k {
					continue
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					t.ID, t.Date, t.Kind, t.Account, t.Asset, t.Quantity, t.Amount, t.Note)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "filtre par enveloppe")
	cmd.Flags().StringVar(&asset, "asset", "", "filtre par actif")
	cmd.Flags().StringVar(&kind, "kind", "", "filtre par type (buy, sell, statement…)")
	return cmd
}

func txEdit(a *app) *cobra.Command {
	var date, account, asset, qty, total, note, kind string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Corrige les champs passés en flag, laisse les autres intacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseTxID(args[0])
			if err != nil {
				return err
			}
			return a.mutate(func(b *domain.Book) error {
				tx, err := b.Tx(id)
				if err != nil {
					return err
				}
				if date != "" {
					if tx.Date, err = domain.ParseDate(date); err != nil {
						return err
					}
				}
				if account != "" {
					acc, err := b.Account(account)
					if err != nil {
						return err
					}
					tx.Account = acc.ID
				}
				if asset != "" {
					as, err := b.Asset(asset)
					if err != nil {
						return err
					}
					tx.Asset = as.ID
				}
				if kind != "" {
					if tx.Kind, err = domain.ParseTxKind(kind); err != nil {
						return err
					}
				}
				if qty != "" {
					q, err := decimal.NewFromString(qty)
					if err != nil {
						return fmt.Errorf("quantité %q: %w", qty, err)
					}
					tx.Quantity = q.Abs()
				}
				if total != "" {
					m, err := decimal.NewFromString(total)
					if err != nil {
						return fmt.Errorf("montant %q: %w", total, err)
					}
					tx.Amount.Amount = m.Abs()
				}
				if cmd.Flags().Changed("note") {
					tx.Note = note
				}
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %s %s qté=%s %s\n",
					tx.ID, tx.Date, tx.Kind, tx.Quantity, tx.Amount)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "nouvelle date AAAA-MM-JJ")
	cmd.Flags().StringVar(&account, "account", "", "nouvelle enveloppe")
	cmd.Flags().StringVar(&asset, "asset", "", "nouvel actif")
	cmd.Flags().StringVar(&qty, "qty", "", "nouvelle quantité")
	cmd.Flags().StringVar(&total, "total", "", "nouveau montant total")
	cmd.Flags().StringVar(&note, "note", "", "nouvelle note")
	cmd.Flags().StringVar(&kind, "kind", "", "nouveau type")
	return cmd
}

func txRm(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Supprime une transaction",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseTxID(args[0])
			if err != nil {
				return err
			}
			return a.mutate(func(b *domain.Book) error {
				if err := b.RemoveTx(id); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Transaction %d supprimée\n", id)
				return nil
			})
		},
	}
}
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): tx list/edit/rm — ledger éditable"
```

---

### Task 12: cli — import CSV idempotent

**Files:**
- Create: `internal/cli/import.go`
- Modify: `internal/cli/cli.go` (ligne AddCommand)
- Test: `internal/cli/import_test.go` (unitaire, boîte blanche), `internal/cli/cli_test.go` (ajout)

- [ ] **Step 1: Écrire les tests qui échouent**

`internal/cli/import_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"finador/internal/domain"
)

const sampleCSV = `date,kind,account,asset,quantity,price,amount,currency,group,note
2026-01-15,buy,PEA Zephyr,CW8.PA,10,550,,EUR,actions/monde,premier achat
2026-01-20,deposit,PEA Zephyr,,,,5000,EUR,,
2026-02-01,statement,Livret A,,,,12000,EUR,,
`

func TestImportCSV(t *testing.T) {
	b := domain.NewBook()
	added, skipped, err := importCSV(b, strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 || skipped != 0 {
		t.Fatalf("added=%d skipped=%d", added, skipped)
	}
	// comptes et actif créés à la volée
	if _, err := b.Account("pea-zephyr"); err != nil {
		t.Error(err)
	}
	if _, err := b.Account("Livret A"); err != nil {
		t.Error(err)
	}
	asset, err := b.Asset("CW8.PA")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Group != "actions/monde" {
		t.Errorf("group = %q", asset.Group)
	}
	// price unitaire × quantité → montant total
	if got := b.Transactions[0].Amount.Amount.String(); got != "5500" {
		t.Errorf("amount = %s", got)
	}
}

func TestImportCSVIdempotent(t *testing.T) {
	b := domain.NewBook()
	if _, _, err := importCSV(b, strings.NewReader(sampleCSV)); err != nil {
		t.Fatal(err)
	}
	added, skipped, err := importCSV(b, strings.NewReader(sampleCSV))
	if err != nil || added != 0 || skipped != 3 {
		t.Fatalf("ré-import: added=%d skipped=%d err=%v", added, skipped, err)
	}
}

func TestImportCSVBadLine(t *testing.T) {
	b := domain.NewBook()
	bad := "date,kind,account,amount,currency\n2026-13-45,buy,X,100,EUR\n"
	if _, _, err := importCSV(b, strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "ligne 2") {
		t.Fatalf("err = %v", err)
	}
}
```

Ajouter à `internal/cli/cli_test.go` (test bout-en-bout) :

```go
func TestImportCommand(t *testing.T) {
	db := newDB(t)
	csvPath := filepath.Join(t.TempDir(), "txs.csv")
	content := "date,kind,account,asset,quantity,price,amount,currency,group,note\n" +
		"2026-01-15,buy,PEA,CW8.PA,10,550,,EUR,actions/monde,\n"
	if err := os.WriteFile(csvPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := run(t, db, "import", csvPath); !strings.Contains(out, "1 importée(s), 0 ignorée(s)") {
		t.Fatalf("import: %q", out)
	}
	if out := run(t, db, "import", csvPath); !strings.Contains(out, "0 importée(s), 1 ignorée(s)") {
		t.Fatalf("ré-import: %q", out)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/cli/ -run TestImport`
Expected: FAIL — `undefined: importCSV`.

- [ ] **Step 3: Implémenter**

Dans `internal/cli/cli.go`, étendre la ligne AddCommand :

```go
	root.AddCommand(initCmd(a), accountCmd(a), assetCmd(a), addCmd(a), sellCmd(a),
		cashCmd(a), depositCmd(a), withdrawCmd(a), txCmd(a), importCmd(a))
```

`internal/cli/import.go`:

```go
package cli

import (
	"cmp"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func importCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "import <fichier.csv>",
		Short: "Importe des transactions (colonnes par en-tête ; ré-import sans doublon)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer file.Close()
			var added, skipped int
			// mutate n'écrit le fichier que si tout l'import a réussi.
			if err := a.mutate(func(b *domain.Book) error {
				added, skipped, err = importCSV(b, file)
				return err
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d importée(s), %d ignorée(s) (doublons)\n", added, skipped)
			return nil
		},
	}
}

// importCSV reads header-mapped transactions: date, kind, account, asset,
// quantity, price, amount, currency, group, note — in any column order.
// Unknown accounts and assets are created on the fly; lines whose content
// hash is already in the book are skipped.
func importCSV(b *domain.Book, r io.Reader) (added, skipped int, err error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return 0, 0, fmt.Errorf("en-tête CSV: %w", err)
	}
	col := map[string]int{}
	for i, name := range header {
		col[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for line := 2; ; line++ {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return added, skipped, err
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(record) {
				return strings.TrimSpace(record[i])
			}
			return ""
		}
		tx, err := rowToTx(b, get)
		if err != nil {
			return added, skipped, fmt.Errorf("ligne %d: %w", line, err)
		}
		if b.HasImportHash(tx.ImportHash) {
			skipped++
			continue
		}
		b.Add(tx)
		added++
	}
	return added, skipped, nil
}

func rowToTx(b *domain.Book, get func(string) string) (domain.Transaction, error) {
	var zero domain.Transaction
	date, err := domain.ParseDate(get("date"))
	if err != nil {
		return zero, err
	}
	kind, err := domain.ParseTxKind(get("kind"))
	if err != nil {
		return zero, err
	}
	acc, err := ensureAccount(b, get("account"), get("currency"))
	if err != nil {
		return zero, err
	}
	ccy := domain.Currency(cmp.Or(get("currency"), string(acc.Currency)))

	tx := domain.Transaction{Date: date, Account: acc.ID, Kind: kind, Note: get("note")}

	if ref := get("asset"); ref != "" {
		asset, err := ensureAsset(b, ref, ccy, get("group"))
		if err != nil {
			return zero, err
		}
		tx.Asset = asset.ID
	}

	qty := decimal.Zero
	if q := get("quantity"); q != "" {
		if qty, err = decimal.NewFromString(q); err != nil {
			return zero, fmt.Errorf("quantité %q: %w", q, err)
		}
	}
	tx.Quantity = qty.Abs()

	var amount decimal.Decimal
	switch {
	case get("amount") != "":
		if amount, err = decimal.NewFromString(get("amount")); err != nil {
			return zero, fmt.Errorf("montant %q: %w", get("amount"), err)
		}
	case get("price") != "":
		price, err := decimal.NewFromString(get("price"))
		if err != nil {
			return zero, fmt.Errorf("prix %q: %w", get("price"), err)
		}
		amount = price.Mul(tx.Quantity)
	default:
		return zero, errors.New("ni amount ni price")
	}
	tx.Amount = domain.Money{Amount: amount.Abs(), Currency: ccy}
	tx.ImportHash = hashTx(tx)
	return tx, nil
}

// hashTx fingerprints the canonical content of a row, for idempotent re-imports.
// Two genuinely identical operations the same day must differ by their note.
func hashTx(t domain.Transaction) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		t.Date.String(), t.Kind.String(), string(t.Account), string(t.Asset),
		t.Quantity.String(), t.Amount.Amount.String(), string(t.Amount.Currency), t.Note,
	}, "|")))
	return hex.EncodeToString(sum[:8])
}

func ensureAccount(b *domain.Book, ref, ccy string) (*domain.Account, error) {
	if ref == "" {
		return nil, errors.New("colonne account vide")
	}
	if acc, err := b.Account(ref); err == nil {
		return acc, nil
	}
	acc := &domain.Account{ID: domain.AccountID(domain.Slugify(ref)), Name: ref,
		Currency: domain.Currency(cmp.Or(ccy, "EUR"))}
	return acc, b.AddAccount(acc)
}

func ensureAsset(b *domain.Book, ref string, ccy domain.Currency, group string) (*domain.Asset, error) {
	if asset, err := b.Asset(ref); err == nil {
		return asset, nil
	}
	asset := &domain.Asset{ID: domain.AssetID(domain.Slugify(ref)), Kind: domain.Security,
		Name: ref, Ticker: ref, Currency: ccy, Group: group}
	return asset, b.AddAsset(asset)
}
```

- [ ] **Step 4: Vérifier le succès**

Run: `go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): import CSV idempotent, création des comptes/actifs à la volée"
```

---

### Task 13: cli — config, lock, finition de la phase

**Files:**
- Create: `internal/cli/config.go`, `internal/cli/lock.go`
- Modify: `internal/cli/cli.go` (ligne AddCommand)
- Test: `internal/cli/cli_test.go` (ajout)

- [ ] **Step 1: Écrire le test qui échoue**

Ajouter à `internal/cli/cli_test.go`:

```go
func TestConfigSetGet(t *testing.T) {
	db := newDB(t)
	run(t, db, "config", "set", "risk-free", "2.4%")
	if out := run(t, db, "config", "get", "risk-free"); !strings.Contains(out, "2.4%") {
		t.Fatalf("config get: %q", out)
	}
	if out := run(t, db, "config", "get"); !strings.Contains(out, "risk-free = 2.4%") {
		t.Fatalf("config get (tout): %q", out)
	}
}
```

- [ ] **Step 2: Vérifier l'échec**

Run: `go test ./internal/cli/ -run TestConfigSetGet`
Expected: FAIL — `unknown command "config"`.

- [ ] **Step 3: Implémenter**

Dans `internal/cli/cli.go`, la ligne AddCommand finale de la phase A :

```go
	root.AddCommand(initCmd(a), accountCmd(a), assetCmd(a), addCmd(a), sellCmd(a),
		cashCmd(a), depositCmd(a), withdrawCmd(a), txCmd(a), importCmd(a),
		configCmd(a), lockCmd(a))
```

`internal/cli/config.go`:

```go
package cli

import (
	"fmt"
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"finador/internal/domain"
)

func configCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Réglages : default-account, keychain-ttl, risk-free…"}
	set := &cobra.Command{
		Use:   "set <clé> <valeur>",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.mutate(func(b *domain.Book) error {
				b.Config[args[0]] = args[1]
				return nil
			})
		},
	}
	get := &cobra.Command{
		Use:  "get [clé]",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := a.open()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				fmt.Fprintln(cmd.OutOrStdout(), f.Book.Config[args[0]])
				return nil
			}
			for _, k := range slices.Sorted(maps.Keys(f.Book.Config)) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", k, f.Book.Config[k])
			}
			return nil
		},
	}
	cmd.AddCommand(set, get)
	return cmd
}
```

`internal/cli/lock.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"finador/internal/keyring"
)

func lockCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:   "lock",
		Short: "Oublie les mots de passe mémorisés dans le Keychain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			keyring.System().Purge()
			fmt.Fprintln(cmd.OutOrStdout(), "Keychain purgé")
			return nil
		},
	}
}
```

- [ ] **Step 4: Vérifier le succès — toute la phase**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: gofmt muet, vet muet, tous les packages PASS.

Run: `go build -trimpath -o bin/finador ./cmd/finador && FINADOR_PASSWORD=demo ./bin/finador --db /tmp/demo.fin --no-keychain init && FINADOR_PASSWORD=demo ./bin/finador --db /tmp/demo.fin --no-keychain account add "PEA Zephyr" --tax gains:17.2% && FINADOR_PASSWORD=demo ./bin/finador --db /tmp/demo.fin --no-keychain account list && rm -f /tmp/demo.fin*`
Expected: binaire construit, smoke test manuel OK (création, ajout, listing).

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): config set/get et lock — la phase A est complète"
git tag phase-a
```

---

## Couverture spec (phase A)

| Spec § | Couvert par |
|---|---|
| §2 architecture, libs | Tasks 1, 8 (cobra), 4-5 (lo), 2 (decimal) |
| §3 enveloppes/TaxRule | Task 3 (le *calcul* d'impôt latent est en phase B avec la valorisation) |
| §3 actifs, ledger, cash, multi-comptes | Tasks 4, 5, 10 |
| §3 import CSV idempotent | Task 12 |
| §4 fichier .fin, Argon2id, AES-GCM, .bak | Task 6 |
| §4 Keychain par (fichier, tty), TTL, lock | Tasks 7, 13 |
| §7 CLI de saisie | Tasks 8-13 (value/perf/chart/refresh/serve : phases B-D) |
| §9 erreurs sentinelles, %w | Tasks 5, 6 et toute la CLI |
| §10 tests sans réseau | tous les tasks |


