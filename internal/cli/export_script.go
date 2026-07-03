package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"finador/internal/domain"
)

// writeScript dumps the book as a replayable sequence of finador commands:
// config, accounts, assets, then every ledger record in date order, labels
// last. Running the script against a freshly `finador init`-ed file rebuilds
// an equivalent portfolio (entry IDs and import hashes are regenerated, so
// the copy does not merge/sync as the same lineage - it is a rebuild recipe,
// not a backup; the encrypted file itself is the backup).
func writeScript(w io.Writer, b *domain.Book) error {
	var s strings.Builder
	s.WriteString("# finador portfolio dump - replay into a fresh file:\n")
	s.WriteString("#   finador --db new.fin init && sh this-file.sh\n")

	keys := make([]string, 0, len(b.Config))
	for k := range b.Config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&s, "finador config set %s %s\n", shq(k), shq(b.Config[k]))
	}

	for _, acc := range b.Accounts {
		fmt.Fprintf(&s, "finador account add %s", shq(acc.Name))
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
		fmt.Fprintf(&s, "finador asset add %s", shq(arg))
		if a.Kind == domain.Property {
			s.WriteString(" --kind property")
		}
		if a.Name != arg {
			fmt.Fprintf(&s, " --name %s", shq(a.Name))
		}
		if a.ISIN != "" {
			fmt.Fprintf(&s, " --isin %s", a.ISIN)
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
			fmt.Fprintf(&s, "finador asset edit %s --withholding %s%%\n",
				shq(arg), strconv.FormatFloat(a.Withholding*100, 'f', -1, 64))
		}
	}

	txs := make([]*domain.Transaction, len(b.Transactions))
	copy(txs, b.Transactions)
	sort.SliceStable(txs, func(i, j int) bool { return txs[i].Date.Before(txs[j].Date) })
	for _, t := range txs {
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
		fmt.Fprintf(&s, "finador label add %s --account %s --asset %s\n",
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
		fmt.Fprintf(s, "finador asset %s %s %s %s %s --account %s --ccy %s%s\n",
			kindWord(t.Kind), assetArg, t.Quantity.String(), amount, t.Date, shq(acc.Name), ccy, note)
	case domain.Dividend, domain.Fee:
		fmt.Fprintf(s, "finador asset %s %s %s %s --account %s --ccy %s%s\n",
			kindWord(t.Kind), assetArg, amount, t.Date, shq(acc.Name), ccy, note)
	case domain.Deposit, domain.Withdraw:
		fmt.Fprintf(s, "finador cash %s %s %s %s --ccy %s%s\n",
			kindWord(t.Kind), shq(acc.Name), amount, t.Date, ccy, note)
	case domain.Statement:
		if t.Asset != "" {
			fmt.Fprintf(s, "finador asset set %s %s --at %s --account %s --ccy %s%s\n",
				assetArg, amount, t.Date, shq(acc.Name), ccy, note)
			return nil
		}
		fmt.Fprintf(s, "finador cash set %s %s --at %s --ccy %s\n",
			shq(acc.Name), amount, t.Date, ccy)
		if t.Note != "" {
			fmt.Fprintf(s, "# note on the statement above: %s\n", t.Note)
		}
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

// shq quotes a script argument when it needs it: plain tokens pass through,
// anything with spaces or shell-significant characters is double-quoted
// (Go syntax, which a POSIX shell reads identically for these strings).
func shq(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`#|&;()<>*?[]{}~") {
		return s
	}
	return strconv.Quote(s)
}
