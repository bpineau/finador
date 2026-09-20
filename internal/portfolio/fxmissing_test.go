package portfolio

import (
	"strings"
	"testing"

	"finador/internal/domain"
)

// bookWithForeignFee adds a fee booked in a currency no account and no asset
// declares - the case the market refresh used to leave without a rate.
func bookWithForeignFee(t *testing.T) (*domain.Book, *domain.Transaction) {
	t.Helper()
	b := valuationBook(t)
	fee := b.Add(domain.Transaction{Date: mustDate("2026-02-20"), Account: "pea", Asset: "cw8",
		Kind: domain.Fee, Amount: domain.Money{Amount: dec("10000"), Currency: "JPY"}})
	return b, fee
}

// A total must never lose a line in silence. When no rate crosses a record's
// currency, Value refuses the total - and says which record, which currency
// and which date, so the user can act instead of guessing.
func TestValueRefusesTheTotalAndNamesTheRecord(t *testing.T) {
	b, fee := bookWithForeignFee(t)
	_, err := Value(b, Scope{Kind: All}, mustDate("2026-06-10"), domain.EUR, fxStub{})
	if err == nil {
		t.Fatal("Value succeeded: a missing rate must refuse the total, never drop the line")
	}
	for _, want := range []string{"JPY", "2026-02-20", "fee", "CW8", "PEA", string(fee.ID)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A position whose own quote currency cannot be crossed is named too: the
// error must point at the holding, not at an anonymous currency pair.
func TestValueNamesThePositionWhoseCurrencyCannotBeCrossed(t *testing.T) {
	b := valuationBook(t)
	cw8, err := b.Asset("cw8")
	if err != nil {
		t.Fatal(err)
	}
	cw8.Currency = "JPY"
	_, err = Value(b, Scope{Kind: All}, mustDate("2026-06-10"), domain.EUR, fxStub{})
	if err == nil {
		t.Fatal("Value succeeded although no JPY rate exists")
	}
	if !strings.Contains(err.Error(), "CW8") || !strings.Contains(err.Error(), "JPY") {
		t.Errorf("error %q names neither the position nor the currency", err)
	}
}

// Series must stay drawable, so it counts an unconvertible amount as zero -
// but it says which record it dropped, its currency and its date. The generic
// "cannot convert JPY" told the user nothing about where to look.
func TestSeriesWarningNamesTheRecord(t *testing.T) {
	b, fee := bookWithForeignFee(t)
	res, err := Series(b, Scope{Kind: All}, mustDate("2026-02-01"), mustDate("2026-06-10"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"JPY", "2026-02-20", "fee", "CW8", string(fee.ID)} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q do not name %q", joined, want)
		}
	}
	if !strings.Contains(joined, "counted as 0") {
		t.Errorf("warnings %q do not say the amount was counted as zero", joined)
	}
}

// The warning is one line per (record, currency pair), whatever the length of
// the window: a daily loop must not repeat itself 200 times.
func TestSeriesWarningsAreDeduplicated(t *testing.T) {
	b := valuationBook(t)
	cw8, err := b.Asset("cw8")
	if err != nil {
		t.Fatal(err)
	}
	cw8.Currency = "JPY"
	res, err := Series(b, Scope{Kind: All}, mustDate("2026-01-01"), mustDate("2026-06-10"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("no warning for an unconvertible position")
	}
	if len(res.Warnings) > 6 {
		t.Errorf("%d warnings for one currency over five months: %v", len(res.Warnings), res.Warnings)
	}
	seen := map[string]bool{}
	for _, w := range res.Warnings {
		if seen[w] {
			t.Errorf("duplicate warning %q", w)
		}
		seen[w] = true
	}
}
