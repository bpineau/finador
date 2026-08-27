package cli_test

import (
	"strings"
	"testing"
)

// accountScopeDB builds a two-envelope book: one group held in both, one
// group and some cash held in only one - enough to tell an envelope, a
// group and their intersection apart.
func accountScopeDB(t *testing.T) string {
	t.Helper()
	t.Setenv("FINADOR_CACHE_DIR", t.TempDir())
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "account", "add", "CTO Meridia", "--tax", "gains:30%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "equities/world")
	run(t, db, "asset", "add", "GTWR", "--alias", "gtwr", "--group", "bonds")
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01", "--account", "PEA Zephyr")
	run(t, db, "asset", "buy", "gtwr", "4", "@100", "2026-06-01", "--account", "PEA Zephyr")
	run(t, db, "asset", "buy", "cw8", "2", "@550", "2026-06-01", "--account", "CTO Meridia")
	return db
}

// TestScopeAccountFlagMatchesPositional: `--account X` selects exactly what
// the positional envelope scope selects, on every command that shares
// resolveScope.
func TestScopeAccountFlagMatchesPositional(t *testing.T) {
	db := accountScopeDB(t)
	cases := []struct {
		name string
		args []string
	}{
		{"value", []string{"value", "--at", "2026-06-05"}},
		{"value --tree", []string{"value", "--tree", "--at", "2026-06-05"}},
		{"perf", []string{"perf", "--to", "2026-06-05"}},
		{"chart", []string{"chart", "--to", "2026-06-05"}},
		{"export", []string{"export", "--at", "2026-06-05"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			positional := run(t, db, append(append([]string{}, tc.args...), "PEA Zephyr")...)
			flagged := run(t, db, append(append([]string{}, tc.args...), "--account", "PEA Zephyr")...)
			if positional != flagged {
				t.Errorf("--account differs from the positional scope:\n%s\n---\n%s", positional, flagged)
			}
			if !strings.Contains(flagged, "PEA Zephyr") {
				t.Errorf("scope label missing:\n%s", flagged)
			}
		})
	}
}

// TestScopeAccountIntersectsGroup: an envelope plus a group reference is
// their intersection - the group's assets held in that envelope, without the
// envelope's cash and without the same group held elsewhere.
func TestScopeAccountIntersectsGroup(t *testing.T) {
	db := accountScopeDB(t)
	cases := []struct {
		name  string
		args  []string
		want  []string
		avoid []string
	}{
		{
			name:  "envelope alone holds everything it has",
			args:  []string{"value", "--account", "PEA Zephyr", "--at", "2026-06-05"},
			want:  []string{"PEA Zephyr", "10900.00 EUR"}, // 5500 + 400 + 5000 of cash
			avoid: []string{"›"},
		},
		{
			name:  "envelope ∩ group: no cash, no other envelope",
			args:  []string{"value", "--account", "PEA Zephyr", "equities", "--at", "2026-06-05"},
			want:  []string{"PEA Zephyr › equities", "5500.00 EUR"},
			avoid: []string{"cash"},
		},
		{
			name:  "the group alone spans both envelopes",
			args:  []string{"value", "equities", "--at", "2026-06-05"},
			want:  []string{"6600.00 EUR"}, // 10 + 2 shares at 550
			avoid: []string{"cash"},
		},
		{
			name: "--asset composes with --account",
			args: []string{"value", "--account", "PEA Zephyr", "--asset", "gtwr", "--at", "2026-06-05"},
			want: []string{"PEA Zephyr › GTWR", "400.00 EUR"},
		},
		{
			name: "--exclude composes with --account",
			args: []string{"value", "--account", "PEA Zephyr", "--exclude", "cw8", "--at", "2026-06-05"},
			want: []string{"excluding cw8", "5400.00 EUR"}, // 400 + 5000 of cash
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := run(t, db, tc.args...)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("%q missing from:\n%s", want, out)
				}
			}
			for _, avoid := range tc.avoid {
				if strings.Contains(out, avoid) {
					t.Errorf("%q should not appear in:\n%s", avoid, out)
				}
			}
		})
	}
}

// TestScopeAccountLabelIntersection: --account narrows a label to one
// envelope, like it narrows a group.
func TestScopeAccountLabelIntersection(t *testing.T) {
	db := accountScopeDB(t)
	run(t, db, "label", "add", "retraite", "--account", "PEA Zephyr", "--asset", "cw8")
	run(t, db, "label", "add", "retraite", "--account", "CTO Meridia", "--asset", "cw8")

	if out := run(t, db, "value", "--label", "retraite", "--at", "2026-06-05"); !strings.Contains(out, "6600.00 EUR") {
		t.Errorf("label across both envelopes:\n%s", out)
	}
	out := run(t, db, "value", "--account", "CTO Meridia", "--label", "retraite", "--at", "2026-06-05")
	for _, want := range []string{"CTO Meridia › retraite", "1100.00 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from:\n%s", want, out)
		}
	}
}

// TestScopeAccountErrors: --account names an envelope and only an envelope,
// and never silently falls back to a group or an asset.
func TestScopeAccountErrors(t *testing.T) {
	db := accountScopeDB(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"asset reference", []string{"--account", "cw8"}, `--account: account "cw8": not found`},
		{"group reference", []string{"--account", "equities"}, `--account: account "equities": not found`},
		{"unknown reference", []string{"--account", "Livret Nimbus"}, "--account: account"},
		{"second envelope", []string{"--account", "PEA Zephyr", "CTO Meridia"}, "conflict"},
		{"an asset scope", []string{"--account", "PEA Zephyr", "cw8"}, "conflict"},
		{"unknown scope", []string{"--account", "PEA Zephyr", "nimporte"}, "unknown scope"},
	}
	for _, cmd := range []string{"value", "perf", "chart", "export"} {
		for _, tc := range cases {
			t.Run(cmd+"/"+tc.name, func(t *testing.T) {
				out, err := tryRun(t, db, append([]string{cmd}, tc.args...)...)
				if err == nil {
					t.Fatalf("%s %v should have failed:\n%s", cmd, tc.args, out)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error %q should mention %q", err, tc.want)
				}
			})
		}
	}
	// --script dumps the whole book: it takes no scope at all.
	if _, err := tryRun(t, db, "export", "--script", "--account", "PEA Zephyr"); err == nil {
		t.Error("export --script --account should have failed")
	}
}
