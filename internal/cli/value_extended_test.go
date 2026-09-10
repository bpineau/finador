package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"finador/internal/cli"
	"finador/internal/domain"
	"finador/internal/market"
)

// sessionSource is fakeSource plus both batch shapes: the regular one always
// answers the last close, the extended one an off-hours print whose session
// the test picks. It is the only way to exercise `value --extended` without a
// venue: session is what the whole feature keys on.
type sessionSource struct {
	fakeSource
	session string  // "", "pre" or "post" - "" means the extended batch names no session
	price   float64 // the off-hours price
}

const regularSpot = 560.0 // matches fakeSource's last CW8.PA close

func (s sessionSource) LatestBatch(_ context.Context, refs []market.Ref) market.BatchQuotes {
	return s.answer(refs, regularSpot, "regular")
}

func (s sessionSource) LatestBatchExtended(_ context.Context, refs []market.Ref) market.BatchQuotes {
	return s.answer(refs, s.price, s.session)
}

// answer quotes CW8.PA at the given price and session; every other ref (the
// FX crosses) keeps the regular close, as a real venue's off-hours pass does.
func (s sessionSource) answer(refs []market.Ref, price float64, session string) market.BatchQuotes {
	at := time.Date(2026, 9, 10, 8, 14, 0, 0, time.Local)
	out := market.BatchQuotes{Quotes: map[market.Ref]market.Quote{}, Errs: map[market.Ref]error{}}
	for _, ref := range refs {
		if ref.Symbol != "CW8.PA" {
			out.Errs[ref] = market.ErrNotCovered
			continue
		}
		out.Quotes[ref] = market.Quote{
			Price: price, Time: at, Currency: domain.EUR, Live: true, Session: session,
		}
	}
	return out
}

// runSession drives the real CLI against the scripted source, online.
func runSession(t *testing.T, src market.Source, db string, args ...string) string {
	t.Helper()
	t.Setenv("FINADOR_PASSWORD", "secret-de-test")
	var out bytes.Buffer
	cmd := cli.New(cli.WithSource(src))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--db", db, "--no-keychain"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("finador %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

// bookWithTenShares: ten CW8.PA in a PEA, so the total is ten times whatever
// price the valuation used - the cheapest possible read on which quote won.
func bookWithTenShares(t *testing.T) string {
	t.Helper()
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")
	return db
}

// TestValueExtendedHours: the opt-in changes the price used and says so; off,
// and on a source that names no session, nothing moves.
func TestValueExtendedHours(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		session string
		price   float64
		total   string
		label   string // "" = the output must carry no extended-hours mark
	}{
		{"off", []string{"value", "--gross"}, "pre", 600, "5600.00 EUR", ""},
		{"pre", []string{"value", "--gross", "--extended"}, "pre", 600, "6000.00 EUR", "pre 08:14"},
		{"post", []string{"value", "--gross", "--extended"}, "post", 620, "6200.00 EUR", "post 08:14"},
		// No session: the print is a plain live quote and behaves as ever -
		// merged, used, unlabelled.
		{"no session", []string{"value", "--gross", "--extended"}, "", regularSpot, "5600.00 EUR", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := bookWithTenShares(t)
			src := sessionSource{session: tc.session, price: tc.price}
			out := runSession(t, src, db, tc.args...)
			if !strings.Contains(out, tc.total) {
				t.Errorf("total %s missing from:\n%s", tc.total, out)
			}
			if tc.label == "" {
				if strings.Contains(out, "extended hours") {
					t.Errorf("output claims extended hours it did not use:\n%s", out)
				}
				return
			}
			if !strings.Contains(out, "extended hours") {
				t.Errorf("the figure does not say it is an extended-hours one:\n%s", out)
			}
			if !strings.Contains(out, tc.label) || !strings.Contains(out, "CW8.PA") {
				t.Errorf("session label %q missing from:\n%s", tc.label, out)
			}
			if !strings.Contains(out, "not a close") {
				t.Errorf("the note does not warn that this is no close:\n%s", out)
			}
		})
	}
}

// TestValueExtendedNotPersisted: the off-hours print is never written to the
// quote cache - the next offline valuation is back to the last real close.
func TestValueExtendedNotPersisted(t *testing.T) {
	db := bookWithTenShares(t)
	src := sessionSource{session: "pre", price: 600}
	if out := runSession(t, src, db, "value", "--gross", "--extended"); !strings.Contains(out, "6000.00 EUR") {
		t.Fatalf("extended valuation:\n%s", out)
	}
	if out := run(t, db, "value", "--gross"); !strings.Contains(out, "5600.00 EUR") {
		t.Errorf("offline valuation after an extended pass:\n%s", out)
	}
}

// TestValueExtendedFromConfig: the display preference lives in the ledger too,
// and the flag overrides it in both directions.
func TestValueExtendedFromConfig(t *testing.T) {
	db := bookWithTenShares(t)
	src := sessionSource{session: "post", price: 620}
	run(t, db, "config", "set", "extended-hours", "true")
	if out := runSession(t, src, db, "value", "--gross"); !strings.Contains(out, "6200.00 EUR") {
		t.Errorf("config extended-hours=true:\n%s", out)
	}
	if out := runSession(t, src, db, "value", "--gross", "--extended=false"); !strings.Contains(out, "5600.00 EUR") {
		t.Errorf("--extended=false must override the config:\n%s", out)
	}
	// A tree cannot carry an override; inheriting the config must not break it.
	if out := runSession(t, src, db, "value", "--tree"); !strings.Contains(out, "CW8.PA") {
		t.Errorf("value --tree with extended-hours in config:\n%s", out)
	}
	if _, err := tryRun(t, db, "value", "--tree", "--extended"); err == nil {
		t.Error("value --tree --extended must be refused")
	}
}
