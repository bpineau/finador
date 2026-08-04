# Declarative cash - design

**Goal:** an account's cash balance is exactly what the user declared, and
nothing else. Buys, sells, dividends and fees never move it. This removes the
`CashTracked` concept from both implementations (Go and Kotlin) and with it a
class of silent, unrecoverable performance corruption.

Reviewed adversarially before implementation; the findings are folded in and
noted as *amended* where they changed the design.

## The problem

One inferred boolean answers two unrelated questions:

- *does this account display a cash balance?*
- *does a trade debit that balance?*

`portfolio.CashTracked` (replay.go:86) returns true as soon as an account
carries any pure-cash `Statement`, `Deposit` or `Withdraw` - and it is evaluated
over the whole ledger, not as of a date. Consequences observed on a real ledger:

1. **Silent negative cash.** An account whose cash was tracked, then traded
   without a matching deposit, shows a negative cash line (`cashValue` has no
   floor, value.go:418). The position it funded is silently cancelled out of the
   account's value.
2. **Retroactive contamination.** Declaring cash on an account *today* makes the
   *entire* replay treat it as tracked (`series.go:144` computes `tracked` once
   per account). Past buys stop being external flows and become drains on a
   balance that starts at zero: the account's historical value collapses and its
   TWR becomes noise.
3. **A trap with no safe exit.** `cash set` cannot repair a negative balance:
   only the first pure-cash statement is an adoption (D8), so every later one
   books the gap as performance. Users who try to zero the balance mint a
   phantom gain or loss.

All three are the same root cause: cash moves by inference, so a *missing*
declaration is indistinguishable from a *real* movement.

## The rule

> An account's cash balance is what the user declared, and nothing else.

| Record | Cash | Performance |
|---|---|---|
| `deposit` / `withdraw` | +/- amount | external flow, neutral |
| pure-cash `statement` | sets the balance | first one: neutral adoption (D8); later ones: the gap is performance |
| `buy` / `sell` | **unchanged** | external flow at market value, neutral |
| `fee` | **unchanged** | positive external flow buying nothing: reads as a loss of exactly the fee |
| `dividend` (manual or Yahoo-known) | **unchanged** | negative external flow (income leaves the pocket) |
| security/property `statement` | unchanged | unchanged (D8, D27) |

### Fees (*amended*)

Today `asset fee` does exactly one thing: debit tracked cash (series.go:332,
"never a flow: a cost must weigh on performance"). On an untracked account a fee
is therefore **already** a complete no-op, and generalising untracked would make
that universal - fees would silently stop weighing on anything.

The fix falls out of the model rather than adding a rule: **a fee is a positive
external flow that buys nothing.** Capital of `fee` enters the envelope and no
value appears against it, so `r_t = V_t/(V_{t-1}+fee) < 1` books a loss of
exactly the fee (D28 start-of-day convention). It works identically with or
without declared cash, needs no special case, and puts the fee in the cost basis
- which is also the correct French tax treatment of brokerage fees.

Rejected: making fees debit *declared* cash. It reintroduces a per-account
special case and does nothing at all when no cash is declared.

### The robustness property (*amended - the first draft overclaimed*)

Per-event neutrality holds: every flow matches its own value delta on the day it
occurs, so the **order of the buy and the matching withdrawal does not matter**,
and omitting the withdrawal costs nothing *on the day of the buy*.

It does cost something afterwards. The undeclared cash stays in `V_t` every
subsequent day and dilutes returns: a +10% move on 10 000 really invested reads
`(11000+5000)/(10000+5000)` = +6.7% if 5 000 of phantom cash sits alongside. It
is the same distortion as genuinely holding idle cash, it is bounded, and it
disappears the moment the withdrawal is declared - as opposed to today's
behaviour, where the same omission mints a negative cash line and destroys the
account's TWR irrecoverably. The failure mode moves from *invisible and
catastrophic* to *visible and proportionate*.

Note also that "cancel exactly" is false in the strict sense: a buy's flow is
valued at the shares' **market close** (series.go:259-267), the withdrawal at
the **cash amount**. They coincide for an at-market buy and differ otherwise;
per-event neutrality is what the model actually guarantees.

### Deliberately kept: D8, plus a guard rail (*amended*)

The gap between two pure-cash statements still counts as performance - that is
what captures interest on a savings account.

The review landed a fair hit here: on an account that declares cash *and*
trades, the new model makes the declared balance drift from broker reality by
design (`cash set 10000`, then `buy 8000`: finador still says 10 000, the broker
says 2 000). The natural repair, `cash set 2000`, is the poisoned one - it books
-8 000 as performance. And the old house rule ("withdraw when money leaves")
misleads, because no money left the broker.

Rather than add a per-account rule, the model keeps D8 and adds a guard where
the mistake is made: **`cash set` warns when the account has trades after the
previous cash statement**, naming `cash withdraw` as the likely intent. The
guidance becomes: *`withdraw` whenever the pocket shrinks - spent, invested or
transferred; `set` only to record a freshly observed balance.*

### Deliberately out of scope: adjusted closes

finador fetches **raw** closes (`market/pofo.go:85`, `Raw: true`) and accounts
for dividends separately. Dropping the cash credit turns account TWR into a
price return rather than a total return; it does *not* book the ex-date drop as
a loss, because `applyDividends` already emits a compensating negative flow for
untracked accounts (series.go:436) and that path becomes universal. Switching to
adjusted closes would restore total return and delete dividend accounting
outright, but it invalidates the whole quote cache and breaks sources that
cannot adjust (Stooq). Separate decision, separate change.

## What does not change

**The on-disk format is untouched.** `docs/FORMAT.md` never mentions cash
tracking: it is a replay convention, not a stored property. No new field, no new
record kind, no version bump, no migration, no cross-implementation format gate.
Existing `.fin` files decode byte-for-byte identically; only the numbers derived
from them change. `docs/format-testdata/sample.ledger` stays valid as-is.

## Changes - Go (`internal/portfolio`)

| Site | Change |
|---|---|
| `replay.go:86` | delete `CashTracked` |
| `value.go:121` | drop the `CashTracked` guard; the existing `gross == 0` test already hides accounts with no declared cash |
| `value.go:418` `cashValue` | keep only `Deposit`/`Withdraw` in the sign switch (drop `Sell`, `Dividend`, `Buy`, `Fee`); drop the `autoDividends` credit |
| `value.go:463` `autoDividends` | delete (its only caller was `cashValue`) |
| `value.go:364` `accountBasis` | see *Tax basis* below |
| `series.go:130,144` | drop the `tracked` field from `accountState` and its initialisation |
| `series.go:270` | delete the cash mutation on `Buy`/`Sell` |
| `series.go:285-294` | single flow predicate, see *Flow predicate* below |
| `series.go:317` | delete the cash credit on `Dividend` |
| `series.go:332` `Fee` | delete the cash debit; emit `+disp` as a flow and add it to `flowBasis` |
| `series.go:345` | a pure-cash statement always applies (adoption logic unchanged) |
| `series.go:425` | delete the cash credit in `applyDividends`; always emit the flow |
| `breakdown.go:49`, `export.go:51` | drop the `CashTracked` guard |
| stale comments | `value.go:54` (Value godoc), `:348`, `:390-395`, `:499`, `export.go` CashRows |

`manualDividendAssets` stays: `series.go:140` still uses it to skip Yahoo-known
dividends on assets with a manual `Dividend` record.

### Flow predicate (*amended*)

The first draft kept `inCash` (`scope.hasCash`) as the gate for trade flows. The
review is right that the correct predicate is `scope.hasAsset` - **value
inclusion and flow emission must share one test**, and `hasAsset` is already the
one used for statements (series.go:393). This closes two real holes, one of them
a pre-existing bug:

- **`ByAccountGroup`** (the crossed account × group rows of `perf --tree` and the
  web trees) falls into the `default` branch with `hasCash` = false
  (scope.go:177-185 has no `ByAccountGroup` case), so trades emit **no flow**
  today while `valueAt` counts the position: a buy reads as a phantom gain in
  those views. Pre-existing, fixed by this change.
- **`--exclude`d assets**: `hasCash` is true under `All` but `hasAsset` is false
  (scope.go:157), so the naive rewrite would emit a flow for a position that
  contributes no value.

For `All` and `ByAccount` the two predicates agree on trades, so nothing else
moves.

### Tax basis

Today the basis is one of two mutually exclusive rules: `deposits - withdrawals`
when cash is tracked, `buys - sells` otherwise. With cash and positions now
independent, the basis is their sum:

```
basis = (buys - sells + fees) + current declared cash + first statements of properties
```

so that `gross - basis` reduces to the latent gain on positions - declared cash
appears on both sides and cancels. Concretely:

- `value.go accountBasis`: accumulate `buys - sells + fees` unconditionally,
  then add `cashValue(acc)`.
- `series.go`: `flowBasis` accumulates `buys - sells + fees` only (drop the
  `Deposit`/`Withdraw` accumulation at line 305), and `valueAt` uses
  `flowBasis + convF(accSt.cash, acc.Currency, w.ccy, d)` (line 526).
  **The conversion is mandatory**: `accSt.cash` is in account currency and
  `flowBasis` in display currency (series.go:130), so a non-EUR account breaks
  endpoint equality without it.

**Documented approximation:** gains on cash held inside a tax-on-gains envelope
are not taxed, since the basis follows the balance. This is negligible for
brokerage crumbs but *not* for a yield-bearing balance: cash that earns must be
modelled as an asset, not as declared cash, if its gain is to be taxed. Stated
in the README and the decision entry.

`Value()` and `Series()` must keep agreeing pointwise; the endpoint-equality
tests pin it.

## Changes - Kotlin (`../finador-android`)

The Android client re-implements the same rule and must change in the same
commit window, or the two clients disagree on the same ledger:

- `valuation/Valuator.kt`: `cashTracked` (251), the cash line guard (142), the
  contribution rule (375-386), **`cashValue` (402-420)** and **`autoDividends`
  (429-445)** - the last two were missing from the first draft and would have
  left Android crediting sells and Yahoo dividends into cash while Go does not.
- `valuation/Perf.kt`: `cashTracked` (348, 365) and every `acc.tracked` branch
  (428, 433, 446, 450, 454, 464, 515, 517, 549), including the `Fee` branch,
  which gains the new flow emission.

Same semantics, same basis formula, same flow predicate. The
cross-implementation script (`../finador-android/scripts/crossimpl.sh`) still
passes unchanged, since the format is untouched; it does not compare valuations,
so parity is verified by porting the Go table tests.

## Tests

- Delete `TestCashTracked` (`replay_test.go:99`). Many existing tests in
  `series_test.go`, `value_test.go`, `breakdown_test.go` and `export_test.go`
  encode tracked-cash behaviour and are rewritten, not deleted.
- New table tests in `internal/portfolio`:
  - a buy leaves a declared cash balance untouched;
  - a buy on an account with declared cash emits an external flow;
  - **the robustness property, honestly pinned**: `deposit` + `buy` + `withdraw`
    in any order give the same TWR; omitting the withdrawal leaves the buy day
    neutral **and dilutes subsequent returns** - the fixture must have post-buy
    price movement, otherwise the test passes where it cannot fail;
  - a fee reads as a loss of exactly the fee, on an account with no declared
    cash and on one with;
  - a dividend never credits cash and is emitted as a negative flow;
  - a buy under `ByAccountGroup` scope emits its flow (regression);
  - basis: an envelope holding both cash and positions is taxed only on the
    positions' latent gain, in a non-EUR account too (currency regression).
- Endpoint equality `Value()` / `Series()` on a book mixing declared cash,
  trades, fees and dividends.
- `make check` (fmt, vet, lint, test, race) and the CLI end-to-end drive.

## Validation on real data

A throwaway `.fin` is rebuilt from the author's own exported portfolio
(`finador export --script`) in the scratchpad, never in the repo and never in
git. Baselines captured before the change (`value`, `perf`, `perf --tree`) are
diffed after it. Expected differences, all intentional:

- the `cash -38787` line on the brokerage account disappears;
- accounts that were tracked have their basis and TWR recomputed;
- dividend-paying positions lose their income from both TWR and the Gain column
  (`pofo/pkg/metrics/report.go` computes Gain = ΔV - Σflows, and the dividend
  flow now cancels the ex-date drop everywhere);
- pure-cash accounts (Livret A, CEL, current accounts) are unchanged, having no
  trades.

Measured on 2026-08-04: the whole-portfolio cash line goes from 1 212.69 (the
three real pockets minus the phantom -38 787.31) to 40 000.00, exactly the three
pockets. The brokerage envelope's estimated tax drops by 5 796, from a basis of
"deposits 319 142" against a gross deflated by the negative cash, to
"buys 376 441 + fee 21.81" against its real 381 060 - a latent gain of 4 597
instead of 23 056. The remaining distortion on that account's 1m/3m windows is
not from the change but from the ledger scars below; removing them brings its 3m
from +0.90 % to +3.67 %, consistent with its two positions (+3.94 % and +0.40 %).

## Ledger cleanup (author's data, outside this repo)

The author's ledger carries scars of the old model on the brokerage account:
four `cash set` records written to fight the negative balance, which minted a
-1 539.78 phantom loss and a +19 971.36 phantom gain, plus four deposits whose
purpose was only to feed the auto-debit. Under the new rule those deposits would
stand as cash that no longer exists.

Recommended cleanup, applied by hand after the code lands: delete the four
`cash set` records **and** the four deposits on that account. It then becomes a
pure securities envelope - buys carry the flows, basis is `buys - sells + fees`,
no cash line - and a single `cash set <real balance>` can be added later if the
idle crumbs ever matter (being the first statement, it is a neutral adoption).
Windowed Gain figures shift, because capital is now dated at the buy dates and
valued at market rather than dated at the deposits and valued at cash.

## Documentation

- `README.md`: the cash section of *Invest cash into the market*; the arbitrage
  recipe's claim that a sell credits cash (line 201-202); the new pairing recipe
  (`sell` + `deposit`, `buy` + `withdraw`) for accounts that carry declared
  cash; the warning that yield-bearing cash belongs in an asset; the command
  reference.
- `internal/cli/cash.go`: the `deposit` help text ("external cash entering an
  envelope") is no longer the whole truth once it is also the way to record sale
  proceeds; reword. Add the `cash set` guard-rail warning.
- `AGENTS.md` (symlinked as `CLAUDE.md`): replace the *Tracked vs untracked
  cash* invariant with the declarative rule; update the troubleshooting row on
  absurd TWR.
- `docs/superpowers/DECISIONS.md`: new entry **D29 - le cash est déclaratif**,
  in French like its neighbours, recording what was rejected (explicit per
  account flag, fees debiting declared cash, always-adoption statements,
  adjusted closes).
- `docs/FORMAT.md`: verify no valuation semantics leaked into the spec. No
  format change.

## Follow-up, not in this change

The planned broker-statement importer (IBKR Flex Query) must synthesise the
mirror cash records itself - a trade no longer moves cash, so an imported buy
needs its matching `withdraw` emitted from the statement's cash section if the
account carries declared cash.
