# Label Scope (`--label`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `--label <name>` to `perf` and `value` commands so they compute performance/value over only the positions carrying that label, composable with `--exclude`.

**Architecture:** A new `ByLabel` ScopeKind holds a `map[pairKey]bool` set built from `b.Labels` (case-insensitive name match). `HasAsset` returns true iff the (account, asset) pair is in the set. `HasCash` returns false (label scopes are position-only). Flow attribution in `series.go` adds `ByLabel` alongside the existing `ByGroup | ByAsset` cases so it is treated identically (buy/sell/dividend as external flows). The CLI flag builds the scope with `portfolio.LabelScope()` and rejects the combination of a positional arg and `--label`.

**Tech Stack:** Go, Cobra, `internal/portfolio`, `internal/cli`, `internal/domain`

---

## File map

| File | Change |
|------|--------|
| `internal/portfolio/scope.go` | Add `ByLabel` ScopeKind, `Pairs` field, `LabelScope()`, `HasAsset`/`HasCash` cases |
| `internal/portfolio/series.go` | Add `ByLabel` to all three `case ByGroup \|\| ByAsset` switches |
| `internal/cli/perf.go` | Add `--label` flag, mutual-exclusion guard, Example block |
| `internal/cli/value.go` | Add `--label` flag, mutual-exclusion guard, Example block |
| `internal/portfolio/scope_test.go` | New tests for `LabelScope` |
| `internal/portfolio/series_test.go` | New test for ByLabel flow attribution |
| `internal/cli/cli_test.go` | New end-to-end tests for `--label` |
| `README.md` | Add "Performance of a subset" recipe |

---

## Task 1: `ByLabel` scope kind and constructor (`internal/portfolio/scope.go`)

**Files:**
- Modify: `internal/portfolio/scope.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/portfolio/scope_test.go`:

```go
func labelBook(t *testing.T) *domain.Book {
	t.Helper()
	b := valuationBook(t)
	_ = b.AddLabel(&domain.Label{
		ID: "lbl1", Account: "pea", Asset: "cw8", Name: "retraite",
	})
	_ = b.AddLabel(&domain.Label{
		ID: "lbl2", Account: "cto", Asset: "cw8", Name: "retraite",
	})
	return b
}

func TestLabelScopeBuildsSet(t *testing.T) {
	b := labelBook(t)
	s, err := LabelScope(b, "retraite")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != ByLabel {
		t.Fatalf("Kind = %v, want ByLabel", s.Kind)
	}
	if len(s.Pairs) != 2 {
		t.Fatalf("Pairs = %d, want 2", len(s.Pairs))
	}
	if s.Label != "retraite" {
		t.Fatalf("Label = %q", s.Label)
	}
}

func TestLabelScopeUnknownLabel(t *testing.T) {
	b := labelBook(t)
	if _, err := LabelScope(b, "unknown"); err == nil {
		t.Fatal("expected error for unknown label")
	}
}

func TestLabelScopeCaseInsensitive(t *testing.T) {
	b := labelBook(t)
	if _, err := LabelScope(b, "RETRAITE"); err != nil {
		t.Fatal(err)
	}
}

func TestLabelScopeHasAsset(t *testing.T) {
	b := labelBook(t)
	s, _ := LabelScope(b, "retraite")
	pea, _ := b.Account("pea")
	cto, _ := b.Account("cto")
	cw8, _ := b.Asset("cw8")

	if !s.HasAsset(pea, cw8) {
		t.Error("pea/cw8 should be in retraite scope")
	}
	if !s.HasAsset(cto, cw8) {
		t.Error("cto/cw8 should be in retraite scope")
	}
	// A pair not in the label set returns false
	maison, _ := b.Asset("maison")
	immo, _ := b.Account("immo")
	if s.HasAsset(immo, maison) {
		t.Error("immo/maison should not be in retraite scope")
	}
}

func TestLabelScopeExcludedWins(t *testing.T) {
	b := labelBook(t)
	s, _ := LabelScope(b, "retraite")
	s.Excluded = map[domain.AssetID]bool{"cw8": true}
	pea, _ := b.Account("pea")
	cw8, _ := b.Asset("cw8")
	if s.HasAsset(pea, cw8) {
		t.Error("Excluded should override label membership")
	}
}

func TestLabelScopeHasCashFalse(t *testing.T) {
	b := labelBook(t)
	s, _ := LabelScope(b, "retraite")
	pea, _ := b.Account("pea")
	if s.HasCash(pea) {
		t.Error("ByLabel scope must not include cash")
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
cd /Users/ben/projects/finador && go test ./internal/portfolio/ -run 'TestLabelScope' -count=1
```

Expected: compile error (ByLabel, LabelScope, Pairs not defined).

- [ ] **Step 3: Implement in `internal/portfolio/scope.go`**

Add `ByLabel` to the `ScopeKind` enum (after `ByAccountGroup`):

```go
const (
	All ScopeKind = iota
	ByGroup
	ByAccount
	ByAsset
	ByAccountGroup
	ByLabel
)
```

Add `Pairs` field to `Scope`:

```go
type Scope struct {
	Kind     ScopeKind
	Group    string
	Account  *domain.Account
	Asset    *domain.Asset
	Label    string
	Pairs    map[pairKey]bool           // populated for ByLabel
	Excluded map[domain.AssetID]bool
}
```

Add constructor after `IntersectScope`:

```go
// LabelScope builds a scope limited to the (account, asset) pairs that carry
// the given label name (case-insensitive). Returns an error if no such pair exists.
func LabelScope(b *domain.Book, name string) (Scope, error) {
	pairs := map[pairKey]bool{}
	low := strings.ToLower(name)
	for _, l := range b.Labels {
		if strings.ToLower(l.Name) == low {
			pairs[pairKey{acc: l.Account, asset: l.Asset}] = true
		}
	}
	if len(pairs) == 0 {
		return Scope{}, fmt.Errorf("no positions carry label %q", name)
	}
	return Scope{
		Kind:     ByLabel,
		Label:    name,
		Pairs:    pairs,
		Excluded: map[domain.AssetID]bool{},
	}, nil
}
```

Add `ByLabel` case in `hasAsset` (after `ByAccountGroup`):

```go
	case ByLabel:
		return s.Pairs[pairKey{acc: acc.ID, asset: asset.ID}]
```

`hasCash` already falls through to `return false` for unrecognised kinds - no change needed (ByLabel is not `All` or `ByAccount`).

Add `ByLabel` to `lineLabel` (after `ByAccountGroup`):

```go
	case ByLabel:
		return asset.Name
```

- [ ] **Step 4: Run tests to confirm they pass**

```
cd /Users/ben/projects/finador && go test ./internal/portfolio/ -run 'TestLabelScope' -count=1
```

Expected: all PASS.

- [ ] **Step 5: Run full package tests**

```
cd /Users/ben/projects/finador && go test ./internal/portfolio/... -count=1
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/portfolio/scope.go internal/portfolio/scope_test.go
git commit -m "feat(portfolio): ByLabel scope kind and LabelScope constructor"
```

---

## Task 2: Flow attribution for `ByLabel` in `series.go`

**Files:**
- Modify: `internal/portfolio/series.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/portfolio/series_test.go`:

```go
func TestSeriesExternalFlowsLabelScope(t *testing.T) {
	b := valuationBook(t)
	// Tag pea/cw8 with label "retraite"; cto/cw8 has no label.
	_ = b.AddLabel(&domain.Label{ID: "lbl1", Account: "pea", Asset: "cw8", Name: "retraite"})

	scope, err := LabelScope(b, "retraite")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Series(b, scope, mustDate("2026-01-01"), mustDate("2026-06-05"), domain.EUR, fxStub{})
	if err != nil {
		t.Fatal(err)
	}
	// pea/cw8: buy 5000 on 01-15, buy 2750 on 02-15, sell -1800 on 03-15
	// cto/cw8 is NOT in the label set → its buy on 01-20 must NOT appear.
	wantFlows := []struct {
		date string
		amt  float64
	}{
		{"2026-01-15", 5000},
		{"2026-02-15", 2750},
		{"2026-03-15", -1800},
	}
	if len(res.Flows) != len(wantFlows) {
		t.Fatalf("flows = %+v, want %d flows", res.Flows, len(wantFlows))
	}
	for i, w := range wantFlows {
		if res.Flows[i].Date != mustDate(w.date) {
			t.Errorf("flow[%d].Date = %s, want %s", i, res.Flows[i].Date, w.date)
		}
		approx(t, fmt.Sprintf("flow[%d]", i), res.Flows[i].Amount, w.amt)
	}
}
```

Also add `"fmt"` import if not already present in the test file (the existing file doesn't import fmt - check and add only if needed).

- [ ] **Step 2: Run test to confirm it fails**

```
cd /Users/ben/projects/finador && go test ./internal/portfolio/ -run 'TestSeriesExternalFlowsLabelScope' -count=1
```

Expected: FAIL - ByLabel buy flows are not collected (falls into the `default` case).

- [ ] **Step 3: Add `ByLabel` to all three switches in `series.go`**

In `applyTx`, the Buy/Sell case switch (around line 265):
```go
		// Before:
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
		// After:
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset || w.scope.Kind == ByLabel:
```

In `applyTx`, the Dividend case switch (around line 301):
```go
		// Before:
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
		// After:
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset || w.scope.Kind == ByLabel:
```

In `applyDividends`, the auto-dividend switch (around line 403):
```go
		// Before:
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset:
		// After:
		switch {
		case w.scope.Kind == ByGroup || w.scope.Kind == ByAsset || w.scope.Kind == ByLabel:
```

In `valueAt`, the tax rule switch (around line 481):
```go
	// Before:
	if w.scope.Kind == All || w.scope.Kind == ByAccount {
	// After: ByLabel is per-position (not per-envelope), so no change needed here
	// (ByLabel does not satisfy the All/ByAccount condition, which is correct)
```

- [ ] **Step 4: Run test to confirm it passes**

```
cd /Users/ben/projects/finador && go test ./internal/portfolio/ -run 'TestSeriesExternalFlowsLabelScope' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run full package tests**

```
cd /Users/ben/projects/finador && go test ./internal/portfolio/... -count=1
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/portfolio/series.go internal/portfolio/series_test.go
git commit -m "feat(portfolio): ByLabel flows - add ByLabel alongside ByGroup/ByAsset in series switches"
```

---

## Task 3: `--label` CLI flag in `perf.go` and `value.go`

**Files:**
- Modify: `internal/cli/perf.go`
- Modify: `internal/cli/value.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/cli_test.go`:

```go
func labelDB(t *testing.T) string {
	t.Helper()
	db := newDB(t)
	run(t, db, "account", "add", "PEA Zephyr", "--tax", "gains:17.2%")
	run(t, db, "asset", "add", "CW8.PA", "--alias", "cw8", "--group", "actions/monde")
	run(t, db, "asset", "add", "Maison", "--kind", "property", "--group", "immo")
	run(t, db, "label", "add", "retraite", "--asset", "cw8", "--account", "PEA Zephyr")
	return db
}

func TestPerfByLabel(t *testing.T) {
	db := labelDB(t)
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "perf", "--label", "retraite", "--to", "2026-06-05")
	if !strings.Contains(out, "retraite") {
		t.Errorf("label name missing in output:\n%s", out)
	}
	if !strings.Contains(out, "inception") {
		t.Errorf("perf --label missing inception row:\n%s", out)
	}
}

func TestValueByLabel(t *testing.T) {
	db := labelDB(t)
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	out := runNet(t, db, "value", "--label", "retraite", "--at", "2026-06-05")
	// 10 × 560 = 5600, no cash (ByLabel has no cash)
	if !strings.Contains(out, "5600.00 EUR") {
		t.Errorf("value --label: expected 5600.00 EUR:\n%s", out)
	}
	if !strings.Contains(out, "retraite") {
		t.Errorf("label name missing in output:\n%s", out)
	}
}

func TestLabelAndScopeArgMutuallyExclusive(t *testing.T) {
	db := labelDB(t)
	run(t, db, "label", "add", "core", "--asset", "cw8", "--account", "PEA Zephyr")
	if _, err := tryRun(t, db, "perf", "actions/monde", "--label", "retraite"); err == nil {
		t.Fatal("perf with both scope arg and --label should fail")
	}
	if _, err := tryRun(t, db, "value", "actions/monde", "--label", "retraite"); err == nil {
		t.Fatal("value with both scope arg and --label should fail")
	}
}

func TestLabelUnknownErrors(t *testing.T) {
	db := newDB(t)
	if _, err := tryRun(t, db, "perf", "--label", "nonexistent"); err == nil {
		t.Fatal("perf --label with unknown label should fail")
	}
	if _, err := tryRun(t, db, "value", "--label", "nonexistent"); err == nil {
		t.Fatal("value --label with unknown label should fail")
	}
}

func TestLabelWithExclude(t *testing.T) {
	db := labelDB(t)
	run(t, db, "cash", "deposit", "PEA Zephyr", "5000", "2026-01-10")
	run(t, db, "asset", "buy", "cw8", "10", "@550", "2026-06-01")

	// --label retraite --exclude cw8 → cw8 is excluded, so the labeled position vanishes
	out := runNet(t, db, "value", "--label", "retraite", "--exclude", "cw8", "--at", "2026-06-05")
	if strings.Contains(out, "5600") {
		t.Errorf("cw8 should be excluded:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
cd /Users/ben/projects/finador && go test ./internal/cli/ -run 'TestPerfByLabel|TestValueByLabel|TestLabelAndScope|TestLabelUnknown|TestLabelWithExclude' -count=1
```

Expected: FAIL (flag `--label` unknown).

- [ ] **Step 3: Implement `--label` in `internal/cli/perf.go`**

Add `label` variable alongside `ccy`, `from`, `to`, `exclude`:

```go
func perfCmd(a *app) *cobra.Command {
	var ccy, from, to, label string
	var exclude []string
	cmd := &cobra.Command{
		Use:   "perf [scope]",
		Short: "Returns (TWR, XIRR) by period and risk metrics",
		Example: "  finador perf\n" +
			"  finador perf \"PEA Zephyr\"\n" +
			"  finador perf equities/world\n" +
			"  finador perf --label retraite\n" +
			"  finador perf --exclude CW8,AAPL",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
```

Replace the scope-building block (after `b := f.Book`) with:

```go
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			if ref != "" && label != "" {
				return fmt.Errorf("use either a [scope] argument or --label, not both")
			}
			var scope portfolio.Scope
			if label != "" {
				s, err := portfolio.LabelScope(b, label)
				if err != nil {
					return err
				}
				scope = s
			} else {
				s, err := portfolio.ParseScope(b, ref)
				if err != nil {
					return err
				}
				scope = s
			}
```

Add flag registration before the return:

```go
		cmd.Flags().StringVar(&label, "label", "", "restrict scope to positions carrying this label")
```

- [ ] **Step 4: Implement `--label` in `internal/cli/value.go`**

Add `label` variable alongside `ccy`, `at`, `by`, `net`, `exclude`, `whatIf`:

```go
func valueCmd(a *app) *cobra.Command {
	var ccy, at, by, label string
	var net bool
	var exclude, whatIf []string
	cmd := &cobra.Command{
		Use:   "value [scope]",
		Short: "Portfolio value - all, a group, an account or an asset",
		Example: "  finador value --net\n" +
			"  finador value --at 2024-12-31\n" +
			"  finador value equities/world\n" +
			"  finador value --label retraite\n" +
			"  finador value --exclude CW8",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
```

Replace the scope-building block with:

```go
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			if ref != "" && label != "" {
				return fmt.Errorf("use either a [scope] argument or --label, not both")
			}
			var scope portfolio.Scope
			if label != "" {
				s, err := portfolio.LabelScope(b, label)
				if err != nil {
					return err
				}
				scope = s
			} else {
				s, err := portfolio.ParseScope(b, ref)
				if err != nil {
					return err
				}
				scope = s
			}
```

Add flag registration before the return:

```go
		cmd.Flags().StringVar(&label, "label", "", "restrict scope to positions carrying this label")
```

- [ ] **Step 5: Run the new tests**

```
cd /Users/ben/projects/finador && go test ./internal/cli/ -run 'TestPerfByLabel|TestValueByLabel|TestLabelAndScope|TestLabelUnknown|TestLabelWithExclude' -count=1
```

Expected: all PASS.

- [ ] **Step 6: Run full CLI tests**

```
cd /Users/ben/projects/finador && go test ./internal/cli/... -count=1
```

Expected: all PASS.

- [ ] **Step 7: Verify help output**

```
cd /Users/ben/projects/finador && go run ./cmd/finador perf --help
cd /Users/ben/projects/finador && go run ./cmd/finador value --help
```

Expected: both show `--label` flag and the new Example lines.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/perf.go internal/cli/value.go internal/cli/cli_test.go
git commit -m "feat(cli): --label flag for perf and value; mutual-exclusion with scope arg"
```

---

## Task 4: README recipe

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add recipe to the "Comparing and isolating pockets" block**

Locate the block starting `**Comparing and isolating pockets.**` (around line 428 in the current file). Replace:

```markdown
**Comparing and isolating pockets.**

```sh
finador perf "PEA Zephyr"                  # one envelope
finador perf equities --exclude aapl,msft    # a group, without two of its lines
finador value --by account --net             # net worth, one line per envelope
finador chart equities --from 2025-01-01     # one pocket, custom window
```

Exclusions accept any asset reference, remove the assets *and their flows* from
TWR/XIRR, and label the output `(excluding …)`.
```

With:

```markdown
**Comparing and isolating pockets.**

```sh
finador perf "PEA Zephyr"                  # one envelope
finador perf equities/world                  # a group subtree
finador perf --label retraite                # all positions tagged with a label
finador perf --exclude CW8,AAPL             # whole portfolio minus two lines
finador value equities/world                 # group value
finador value --label retraite               # value of a label subset
finador value --exclude CW8                  # without one asset
finador value --by account --net             # net worth, one line per envelope
finador chart equities --from 2025-01-01     # one pocket, custom window
```

Compute performance or value of a subset by **envelope** (`"PEA Zephyr"`), by
**group** (`equities/world`), or by **label** (`--label retraite` - all positions
tagged with that label, regardless of envelope). Combine with `--exclude` to drop
specific assets: `finador perf --label retraite --exclude CW8` works. Labels are
attached to (account, asset) pairs via `finador label add`. Exclusions accept any
asset reference (ticker, ISIN, name) and remove the assets *and their flows* from
TWR/XIRR, labelling the output `(excluding …)`.
```

- [ ] **Step 2: Update "Scopes are uniform" paragraph**

Find (around line 212):

```markdown
**Scopes are uniform.** `value`, `perf` and `chart` all take the same optional
scope argument: nothing (whole portfolio), a group or group prefix
(`equities/us`), an account (`"PEA Zephyr"` or `pea`), or an asset (`cw8`).
Resolution order on a free reference: group first, then account, then asset.
```

Replace with:

```markdown
**Scopes are uniform.** `value`, `perf` and `chart` all take the same optional
scope argument: nothing (whole portfolio), a group or group prefix
(`equities/us`), an account (`"PEA Zephyr"` or `pea`), or an asset (`cw8`).
Resolution order on a free reference: group first, then account, then asset.
`perf` and `value` also accept `--label <name>` to restrict the scope to
positions carrying that label (cannot be combined with a positional scope argument).
```

- [ ] **Step 3: Verify build still clean**

```
cd /Users/ben/projects/finador && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document --label scope and full perf/value subset recipes in README"
```

---

## Task 5: Green commit (pre-commit hook validation)

- [ ] **Step 1: Run the full test suite**

```
cd /Users/ben/projects/finador && go vet ./... && go test ./... -count=1
```

Expected: all PASS.

- [ ] **Step 2: Build check**

```
cd /Users/ben/projects/finador && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Lint**

```
cd /Users/ben/projects/finador && golangci-lint run ./...
```

Expected: no issues.

- [ ] **Step 4: Final commit (squash into feature commit with the spec message)**

If all prior steps committed cleanly, create the final tagged commit:

```bash
git add -A   # should be nothing new
git log --oneline -6
```

If everything is already in separate commits, that's fine - the pre-commit hook will run on each. The task specification asks for a single commit with message `feat(perf,value): subset by --label; document group/account/exclude scopes`. If not already done as a single commit, amend or squash as desired, then verify the hook passes.

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|---|---|
| `ByLabel` ScopeKind + `Pairs` field | Task 1 |
| `LabelScope()` constructor + unknown label error | Task 1 |
| `HasAsset` ByLabel case | Task 1 |
| `HasCash` false for ByLabel | Task 1 |
| `lineLabel` for ByLabel | Task 1 |
| Flow attribution: ByLabel alongside ByGroup/ByAsset (3 switches) | Task 2 |
| `--label` flag on `perf` | Task 3 |
| `--label` flag on `value` | Task 3 |
| Mutual exclusion guard (scope arg + --label) | Task 3 |
| `--exclude` composes with `--label` | Task 3 |
| Example blocks on perf and value | Task 3 |
| scope_test.go: LabelScope builds set; unknown label errors; HasAsset; Excluded wins; HasCash false | Task 1 |
| series_test.go: ByLabel buy flows match expectations | Task 2 |
| cli_test.go: perf --label, value --label, mutual exclusion, unknown label, --label + --exclude | Task 3 |
| README recipe | Task 4 |

**Placeholder scan:** No TBD or placeholders - all steps have complete code.

**Type consistency:**
- `pairKey` is already defined in `series.go` as `struct{ acc domain.AccountID; asset domain.AssetID }` - used as-is in `LabelScope`.
- `Scope.Pairs` type is `map[pairKey]bool` - consistent across Task 1 and Task 3.
- `LabelScope` returns `(Scope, error)` - consistent across Task 1 (definition) and Tasks 2/3 (usage).
- `b.AddLabel` takes `*domain.Label` - used correctly in test helpers.
- Field names `ID`, `Account`, `Asset`, `Name` on `domain.Label` - verified against `internal/domain/label.go`.
