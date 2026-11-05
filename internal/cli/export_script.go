package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"finador/internal/domain"
	"finador/internal/portfolio"
)

// writeScript dumps the book as a replayable shell script of finador commands:
// init of the destination named as its argument, accounts, assets, config,
// then every ledger record in replay order, labels last. It rebuilds an
// equivalent portfolio (entry IDs and import hashes are regenerated, so
// the copy does not merge/sync as the same lineage - it is a rebuild recipe,
// not a backup; the encrypted file itself is the backup).
func writeScript(w io.Writer, b *domain.Book) error {
	var s strings.Builder
	// Every command names the NEW file: a bare `finador` would write to the
	// default ledger, the very one this dump came from. `set -eu` stops at
	// the first error, so an existing destination (init refuses it) or a
	// refused command never leaves a half-applied rebuild.
	s.WriteString("#!/bin/sh\n")
	s.WriteString("# finador portfolio dump - rebuild it into a NEW file:\n")
	s.WriteString("#   sh this-file.sh new.fin\n")
	s.WriteString("set -eu\n")
	s.WriteString("db=${1:?usage: sh this-file.sh new.fin}\n")
	s.WriteString("fin() { finador --db \"$db\" \"$@\"; }\n")
	s.WriteString("fin init\n")

	for _, acc := range b.Accounts {
		fmt.Fprintf(&s, "fin account add %s", shq(acc.Name))
		if acc.Currency != domain.EUR {
			fmt.Fprintf(&s, " --ccy %s", acc.Currency)
		}
		if tax := acc.Tax.String(); tax != "none" {
			fmt.Fprintf(&s, " --tax %s", shq(tax))
		}
		for _, al := range acc.Aliases {
			fmt.Fprintf(&s, " --alias %s", shq(al))
		}
		s.WriteString("\n")
	}

	for _, a := range b.Assets {
		arg := assetRef(a)
		fmt.Fprintf(&s, "fin asset add %s", shq(arg))
		if a.Kind == domain.Property {
			s.WriteString(" --kind property")
		}
		if a.Name != arg {
			fmt.Fprintf(&s, " --name %s", shq(a.Name))
		}
		if a.ISIN != "" {
			fmt.Fprintf(&s, " --isin %s", shq(a.ISIN))
		}
		if a.Currency != domain.EUR {
			fmt.Fprintf(&s, " --ccy %s", a.Currency)
		}
		if a.Group != "" {
			fmt.Fprintf(&s, " --group %s", shq(a.Group))
		}
		for _, al := range a.Aliases {
			fmt.Fprintf(&s, " --alias %s", shq(al))
		}
		s.WriteString("\n")
		if a.Withholding != 0 {
			fmt.Fprintf(&s, "fin asset edit %s --withholding %s%%\n",
				shq(arg), strconv.FormatFloat(a.Withholding*100, 'f', -1, 64))
		}
	}

	writeConfig(&s, b)

	// Replay order, not merely date order: each emitted command mints a fresh
	// id, and those ids come out in emission order (domain.NewID is monotonic
	// within a process). Emitting the ledger the way the engine reads it -
	// (date, id) - is what makes the rebuilt copy replay identically,
	// same-day round trips included.
	for _, t := range portfolio.Sorted(b) {
		if err := writeTx(&s, b, t); err != nil {
			return err
		}
	}

	for _, l := range b.Labels {
		acc, errA := b.Account(string(l.Account))
		asset, errB := b.Asset(string(l.Asset))
		if errA != nil || errB != nil {
			continue // dangling label: nothing to recreate it against
		}
		fmt.Fprintf(&s, "fin label add %s --account %s --asset %s\n",
			shq(l.Name), shq(acc.Name), shq(assetRef(asset)))
	}

	_, err := io.WriteString(w, s.String())
	return err
}

// writeTx emits one ledger record as the entry command that creates it.
// Amounts keep their exact decimal text and always carry their currency.
func writeTx(s *strings.Builder, b *domain.Book, t *domain.Transaction) error {
	acc, err := b.Account(string(t.Account))
	if err != nil {
		return fmt.Errorf("transaction %s: %w", t.ID, err)
	}
	amount, ccy := t.Amount.Amount.String(), string(t.Amount.Currency)
	note := ""
	if t.Note != "" {
		note = " --note " + shq(t.Note)
	}
	assetArg := ""
	if t.Asset != "" {
		a, err := b.Asset(string(t.Asset))
		if err != nil {
			return fmt.Errorf("transaction %s: %w", t.ID, err)
		}
		assetArg = shq(assetRef(a))
	}
	switch t.Kind {
	case domain.Buy, domain.Sell:
		fmt.Fprintf(s, "fin asset %s %s %s %s %s --account %s --ccy %s%s\n",
			kindWord(t.Kind), assetArg, t.Quantity.String(), amount, t.Date, shq(acc.Name), ccy, note)
	case domain.Fee:
		if t.Asset == "" { // charged to the account itself
			fmt.Fprintf(s, "fin cash fee %s %s %s --ccy %s%s\n", shq(acc.Name), amount, t.Date, ccy, note)
			return nil
		}
		fmt.Fprintf(s, "fin asset fee %s %s %s --account %s --ccy %s%s\n",
			assetArg, amount, t.Date, shq(acc.Name), ccy, note)
	case domain.Dividend:
		fmt.Fprintf(s, "fin asset %s %s %s %s --account %s --ccy %s%s\n",
			kindWord(t.Kind), assetArg, amount, t.Date, shq(acc.Name), ccy, note)
	case domain.Deposit, domain.Withdraw:
		fmt.Fprintf(s, "fin cash %s %s %s %s --ccy %s%s\n",
			kindWord(t.Kind), shq(acc.Name), amount, t.Date, ccy, note)
	case domain.Statement:
		if t.Asset != "" {
			fmt.Fprintf(s, "fin asset set %s %s --at %s --account %s --ccy %s%s\n",
				assetArg, amount, t.Date, shq(acc.Name), ccy, note)
			return nil
		}
		fmt.Fprintf(s, "fin cash set %s %s --at %s --ccy %s%s\n",
			shq(acc.Name), amount, t.Date, ccy, note)
	default:
		return fmt.Errorf("transaction %s: unmapped kind %v", t.ID, t.Kind)
	}
	return nil
}

// kindWord is the entry subcommand of a transaction kind.
func kindWord(k domain.TxKind) string {
	switch k {
	case domain.Buy:
		return "buy"
	case domain.Sell:
		return "sell"
	case domain.Dividend:
		return "dividend"
	case domain.Fee:
		return "fee"
	case domain.Deposit:
		return "deposit"
	case domain.Withdraw:
		return "withdraw"
	}
	return ""
}

// assetRef is the reference the script uses for an asset: the ticker when
// there is one (unambiguous by construction), the name otherwise.
func assetRef(a *domain.Asset) string {
	if a.Ticker != "" {
		return a.Ticker
	}
	return a.Name
}

// writeConfig emits the settings. One that names an account is written with
// the account's name: the rebuild mints new ids, so an id would point at
// nothing there.
func writeConfig(s *strings.Builder, b *domain.Book) {
	for _, k := range slices.Sorted(maps.Keys(b.Config)) {
		v := b.Config[k]
		if k == "default-account" {
			if acc, err := b.Account(v); err == nil {
				v = acc.Name
			}
		}
		fmt.Fprintf(s, "fin config set %s %s\n", shq(k), shq(v))
	}
}

// shq quotes a script argument for a POSIX shell: a plain token passes
// through, anything else is single-quoted, where nothing is special (no
// $(...), no backtick, no backslash, a newline stays literal); a single
// quote inside closes the quoting, is escaped, then reopens it. A name or a
// note from a broker statement never runs.
func shq(s string) string {
	const plain = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._/:%@+=,-"
	if s != "" && strings.Trim(s, plain) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
