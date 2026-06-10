# finador

A personal, encrypted wealth tracker in a single Go binary — think Finary or a
Yahoo Finance portfolio, but **yours**: every account, security, property and cash
balance lives in **one encrypted file** on your machine, usable from the **command
line and the web** alike.

```
$ finador value --net
portfolio — 2026-06-10
LINE      GROSS           TAX          NET
equities  18050.00 EUR    361.20 EUR   17688.80 EUR
property  450000.00 EUR   9000.00 EUR  441000.00 EUR
cash      18010.00 EUR    0.00 EUR     18010.00 EUR
TOTAL     486060.00 EUR   9361.20 EUR  476698.80 EUR
```

- **One encrypted file.** Argon2id + AES-256-GCM, atomic writes with a `.bak`,
  password prompted once and optionally cached in the macOS Keychain per terminal.
- **Tax envelopes.** Every account carries its tax rule — `gains:17.2%` (only the
  gain beyond your contributions is taxed, à la PEA/CTO) or `value:20%` (the whole
  value is taxed, à la PER). Everything can be shown **gross, estimated tax, net**.
- **Any asset.** Listed securities (Yahoo quotes, automatic dividends, FX crossed
  through USD), cash balances by dated statements, and arbitrary properties by
  dated estimates.
- **Real performance.** TWR (strategy performance, flows neutralized) and XIRR
  (what your money actually earned), CAGR, volatility, Sharpe, Sortino, max
  drawdown — per period, per scope.
- **Curves everywhere.** Braille charts in the terminal, server-rendered SVG on the
  web. Zero JavaScript, zero external resources.

Build (Go ≥ 1.26, nothing else — pure Go, no CGo, no JS toolchain):

```sh
go build -trimpath -o bin/finador ./cmd/finador
```

## Quick start

```sh
finador init                                            # creates ~/.finador.fin
finador account add "PEA BforBank" --tax gains:17.2%
finador account add "Savings"

finador asset add CW8.PA --id cw8 --group equities/world   # resolved via Yahoo
finador deposit "PEA BforBank" 5000 2026-01-10             # external contribution
finador add cw8 10 @550 2026-06-01 --account "PEA BforBank"
finador cash set Savings 11250                             # observed balance

finador asset add "Country house" --kind property --group property
finador asset set "Country house" 450000 --account Savings

finador value --net        # gross / estimated tax / net
finador perf equities      # TWR, XIRR, CAGR, vol, Sharpe, Sortino, maxDD
finador chart --net        # braille curve in the terminal
finador serve              # full web app on http://127.0.0.1:8451
```

## Concepts

**The file is the database.** Everything — accounts, assets, the transaction
ledger, the quote cache — lives in one encrypted `.fin` file (default
`~/.finador.fin`, override with `--db` or `FINADOR_DB`). Copying that file *is*
your backup. All derived state (positions, cost bases, series) is recomputed by
replaying the ledger, so transactions can always be edited or deleted safely.

**Accounts are tax envelopes.** `--tax gains:17.2%` taxes
`max(0, value − contribution basis)`; the basis is what you put in
(`deposit` − `withdraw`) when the account's cash is tracked, or `buys − sells`
when it is not. `--tax value:20%` taxes the whole value. `--tax none` (default)
taxes nothing. Estimated tax shows up in `value --net`, on net curves and on the
web.

**Tracked vs untracked cash.** An account's cash is *tracked* as soon as it has at
least one pure-cash `statement`, `deposit` or `withdraw`. In a tracked account,
trades move the cash (a buy is value-neutral, like in real life). In an untracked
account, finador assumes you don't care about its cash: buys and sells are treated
as external flows in performance.

**`deposit` ≠ `cash set`.** A *contribution* is entered with `deposit`/`withdraw`
(it feeds the tax basis and XIRR). `cash set` records an *observed balance*, and
the gap between two statements counts as performance — that's how savings-account
interest is captured. The first statement of an account or property is treated as
an *acquisition* (an external flow), not as performance.

**Asset references.** Anywhere an asset is expected you may use its id, ticker,
ISIN, any alias, or its full name — case-insensitive. If every exact match fails,
a **unique prefix** wins: `finador value cw8` finds `cw8-pa`. An ambiguous prefix
fails and lists the candidates. The same applies to account names.

**Groups are paths.** `--group equities/us/tech` builds a hierarchy; every command
accepts a group prefix as scope and aggregates the subtree.

**Scopes are uniform.** `value`, `perf` and `chart` all take the same optional
scope argument: nothing (whole portfolio), a group or group prefix
(`equities/us`), an account (`"PEA BforBank"` or `pea`), or an asset (`cw8`).
Resolution order on a free reference: group first, then account, then asset.

## Command reference

Global flags, valid on every command:

```
--db <path>      encrypted data file (default ~/.finador.fin, or $FINADOR_DB)
--offline        never touch the network; work from the quote cache
--no-keychain    do not store the password in the macOS Keychain
--no-color       disable ANSI colors (also honored: $NO_COLOR)
```

### Setup and accounts

```sh
finador init                          # create the file; password asked twice
finador account add <name> [--tax none|gains:N%|value:N%] [--ccy EUR] [--id slug]
finador account list
```

Account names are free-form (`"CTO IBKR"`); the id defaults to a slug
(`cto-ibkr`) and both work everywhere.

### Assets

```sh
finador asset add <ticker|name> [--kind security|property] [--id x] [--name n]
                  [--isin LU…] [--alias a]... [--ccy EUR] [--group path]
finador asset edit <asset> [--name n] [--ticker t] [--isin i] [--group g] [--ccy c]
                   [--add-alias a]... [--rm-alias a]... [--withholding 15%]
finador asset set <asset> <value> [--at YYYY-MM-DD] [--account acc] [--ccy c]
finador asset list
finador asset rm <asset>              # refused while transactions reference it
```

For a `security`, the argument is the Yahoo ticker; unless `--offline`, finador
resolves it (canonical symbol, full name, quote currency) through Yahoo search.
For a `property`, the argument is just a name. `asset set` records a dated
valuation statement: it is how properties — and securities that Yahoo doesn't
quote (unlisted funds, private equity) — get their value. When an asset has both
market quotes and statements, quotes win.

### Recording activity

```sh
finador add  <asset> <qty> [@unit-price|total] [date] [--account acc] [--ccy c] [--note n]
finador sell <asset> <qty> [@unit-price|total] [date] [--account acc] [--ccy c] [--note n]
finador deposit  <account> <amount> [date] [--ccy c] [--note n]
finador withdraw <account> <amount> [date] [--ccy c] [--note n]
finador cash set <account> <balance> [--at YYYY-MM-DD] [--ccy c]
```

Price syntax: `@550` is a unit price (total = qty × 550); a bare `5500` is the
total; the date defaults to today; the order of the optional arguments is free.
A negative quantity on `add` records a sell, but the shell flag parser needs a
`--` first: `finador add -- cw8 -2 @577`.

If `--account` is omitted, finador picks, in order: the account of the asset's
latest transaction, the `default-account` config key, or the only account if there
is exactly one.

### The ledger

```sh
finador tx list [--account a] [--asset x] [--kind buy|sell|deposit|withdraw|dividend|fee|statement]
finador tx edit <id> [--date d] [--account a] [--asset x] [--kind k] [--qty q] [--total t] [--note n]
finador tx rm <id>
```

Only the flags you pass change; everything else is preserved. Since all derived
state is replayed from the ledger, editing history is always safe — values, bases
and performance recompute instantly.

### Valuation: `value`

```sh
finador value [scope] [--at YYYY-MM-DD] [--ccy USD] [--net]
              [--by group|account] [--exclude refs]... [--what-if asset=price]...
```

- `--at` values the portfolio at any past date (quotes are forward-filled).
- `--net` adds estimated-tax and net columns.
- `--by account` breaks lines down by envelope (cash included) instead of group.
- `--what-if cw8=600 --what-if country=520000` revalues with throwaway price
  hypotheses (in the asset's quote currency; a property override replaces its
  whole estimate), prints `what-if:` markers and a final
  `vs actual: gross … · net …` delta. Nothing is ever stored.
- Lines marked `≈` are approximations: stale quotes (older than 5 days), assets
  valued from statements, or failed currency conversions.

### Performance: `perf`

```sh
finador perf [scope] [--to YYYY-MM-DD] [--from YYYY-MM-DD] [--ccy c] [--exclude refs]...
```

Prints a period table — `1d 5d 1m 3m ytd 1y prev-yr inception` (plus a `window`
row when `--from` is given) — with two complementary measures:

- **TWR** chains daily returns with external flows neutralized: the performance of
  the *strategy*, comparable across scopes.
- **XIRR** is the money-weighted annual rate of *your* euros, contributions
  included. Shown only for windows ≥ 30 days (annualizing a daily move is noise).

Below the table: CAGR, annualized volatility, Sharpe and Sortino (risk-free rate
from `config set risk-free 2.4%`), and max drawdown with peak/trough/recovery
dates. `--to` moves the evaluation date (periods are relative to it), which makes
output reproducible in scripts.

### Charts: `chart`

```sh
finador chart [scope] [--net] [--from d] [--to d] [--ccy c]
              [--width 70] [--height 12] [--exclude refs]...
```

Renders the daily value curve as braille in the terminal, `--net` for the
after-tax curve.

### Quotes: `refresh` and `--offline`

```sh
finador refresh        # force-refresh quotes, FX and dividends from Yahoo
```

`value`, `perf` and `chart` refresh stale series automatically (at most once a
day) before computing; network failures degrade to warnings and the cache keeps
working. `--offline` skips all of it. The quote cache lives **inside** the
encrypted file — your ticker list is sensitive metadata.

### Web: `serve`

```sh
finador serve [--addr 127.0.0.1:8451]
```

Unlocks the file in the terminal, then serves the whole app — dashboard with
allocation trees (by group / by account / by asset), an **assets tab** showing
every holding on one dense row with 1W/1M/1Y sparklines and gross/net amounts,
drill-down scope pages with curves and performance (charts default to full
history, with quiet 1m/3m/1y range links), transaction entry/edit/delete, CSV
import, quote refresh.
No web authentication: keep it on 127.0.0.1 (a loud warning is printed
otherwise).

### Settings: `config` and `lock`

```sh
finador config set <key> <value>
finador config get [key]
finador lock                # purge cached passwords from the Keychain
```

| Key | Effect | Example |
|---|---|---|
| `currency` | default display currency | `EUR` |
| `risk-free` | annual risk-free rate for Sharpe/Sortino | `2.4%` |
| `keychain-ttl` | how long a typed password is cached, per terminal | `8h` |
| `default-account` | account used when `--account` is omitted | `pea-bforbank` |

## CSV import

```sh
finador import transactions.csv
```

Columns are matched by header, in any order:

```csv
date,kind,account,asset,quantity,price,amount,currency,group,note
2026-01-15,buy,PEA BforBank,CW8.PA,10,550,,EUR,equities/world,first buy
2026-01-20,deposit,PEA BforBank,,,,5000,EUR,,
2026-02-01,statement,Savings,,,,12000,EUR,,
2026-03-10,dividend,CTO IBKR,AAPL,,,7.50,USD,,net of withholding
2026-03-12,fee,CTO IBKR,,,,1.20,USD,,broker fee
```

- `kind` ∈ `buy, sell, deposit, withdraw, dividend, fee, statement`.
- Give `price` (unit) or `amount` (total); the other is derived.
- Unknown accounts and assets are created on the fly (assets as securities with
  the reference as ticker).
- **Idempotent**: every row is fingerprinted; re-importing the same file adds
  nothing. Two genuinely identical same-day rows must differ by their `note`.
  Rows you later edit with `tx edit` keep their fingerprint and stay skipped.
- An error on any line aborts the whole import; nothing is written.

## Advanced usage

**Scripting.** Set `FINADOR_PASSWORD` to skip the prompt (less secure — prefer the
Keychain interactively), `FINADOR_DB` for the file path. Output is plain
tab-aligned text, colors disappear automatically when piping. All errors exit 1
with a single-line `finador: …` message.

```sh
FINADOR_PASSWORD=… finador --db work.fin value --ccy USD | grep TOTAL
```

**Multi-currency.** Each account and asset has its own currency; ledger amounts
keep theirs. Conversions cross through USD with daily Yahoo FX, *at each flow's
own date* for bases and *at the valuation date* for values. Display anything in
any currency with `--ccy` — the needed FX series is fetched on demand.

**Dividends.** Listed assets get their dividends automatically from Yahoo
(quantity held at ex-date × gross amount, credited to tracked cash). Set a
per-asset withholding tax with `asset edit aapl --withholding 15%` to credit net
amounts. Recording any *manual* `dividend` transaction for an asset disables the
automatic ones for that asset (no double counting); manual dividends are assumed
already net.

**Comparing and isolating pockets.**

```sh
finador perf "PEA BforBank"                  # one envelope
finador perf equities --exclude aapl,msft    # a group, without two of its lines
finador value --by account --net             # net worth, one line per envelope
finador chart equities --from 2025-01-01     # one pocket, custom window
```

Exclusions accept any asset reference, remove the assets *and their flows* from
TWR/XIRR, and label the output `(excluding …)`.

**What-if analysis.**

```sh
finador value --what-if ddog=280 --net
finador value --what-if cw8=600 --what-if country=520000
```

**Unlisted holdings.** Declare a `security` with no usable ticker, then maintain
dated `asset set` valuations; perf treats the first statement as an acquisition
and later changes as performance. The same mechanics value properties.

**Fixing history.** `tx list --asset cw8` → `tx edit 17 --qty 12 --total 6600` →
done; every figure recomputes. `asset edit` renames, regroups, retickers or
realiases without touching the ledger. Reference collisions (an alias equal to
another asset's ticker, …) are rejected to keep resolution unambiguous.

**Concurrent access.** The web server and the CLI can share the file: writes are
protected by optimistic locking. If another process wrote since you opened, the
save fails with *"file modified by another process since it was opened — retry
the command"* instead of silently overwriting. (A long-running `serve` whose file
was modified by the CLI will refuse further web writes until restarted.)

**Backup & recovery.** The previous version of the file is kept next to it as
`.bak` on every save. Both are fully self-contained — password parameters live in
the authenticated header. A wrong password and a corrupted file are
indistinguishable by design.

**Keychain behavior (macOS).** A typed password is cached *after* a successful
decrypt, per (file, terminal) pair, for `keychain-ttl` (12 h default). `finador
lock` forgets everything; `--no-keychain` never stores. On other platforms (or
with no terminal), use `FINADOR_PASSWORD`.

## Data model & security

- The transaction ledger is the single source of truth; positions, tax bases and
  series are replays. Transaction ids are stable and never reused.
- File layout: `magic ‖ version ‖ Argon2id parameters ‖ salt ‖ nonce ‖
  AES-256-GCM(gzip(JSON))` — the clear header is authenticated (AAD), so any
  tampered byte fails decryption. Atomic writes (tmp + fsync + rename).
- KDF: Argon2id, time=3, memory=64 MiB. Parameters are bounds-checked before
  derivation, so a forged file cannot OOM the process.
- No telemetry, no external resources; the only network calls are Yahoo quote
  endpoints, and only when you allow them.

Implementation notes and trade-offs are journaled in
`docs/superpowers/DECISIONS.md`; the original spec and the phase-by-phase
implementation plans live under `docs/superpowers/`.

## Assumed limits

Yahoo quotes are unofficial. Automatic dividends are gross unless you set a
per-asset withholding. Per-line tax in breakdowns is a per-position approximation
(the total follows the exact per-envelope rule — a footnote says so when they
differ). No benchmark comparison yet. Last-writer wins across processes (with
conflict detection, see above). Exit code is always 1 on error.
