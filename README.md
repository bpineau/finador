# finador

A personal, encrypted wealth tracker in a single Go binary — think Finary or a
Yahoo Finance portfolio, but **yours**: every account, security, property and cash
balance lives in **one encrypted file** on your machine, usable from the **command
line and the web** alike.

```
$ finador value
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
finador account add "PEA Zephyr" --tax gains:17.2%
finador account add "Savings"

finador asset add CW8.PA --alias cw8 --group equities/world   # resolved via Yahoo
finador cash deposit "PEA Zephyr" 5000 2026-01-10           # external contribution
finador asset buy cw8 10 @550 2026-06-01 --account "PEA Zephyr"
finador cash set Savings 11250                                 # observed balance

finador asset add "Country house" --kind property --group property
finador asset set "Country house" 450000 --account Savings

finador value              # gross, estimated tax and net (the default)
finador perf equities      # TWR, XIRR, CAGR, vol, Sharpe, Sortino, maxDD
finador chart --net        # braille curve in the terminal
finador serve              # full web app on http://127.0.0.1:8451
```

## Recipes

Copy-pasteable sequences for the situations that actually come up. Everything is
noun-first (`account`, `asset`, `cash`, `tx`); run any command with `--help` to see
its flags and an example. For a full from-scratch setup of an existing portfolio, see
[tutorial.md](tutorial.md).

### Which command do I use?

The whole model fits in one table. The crux: **`deposit`/`withdraw` move *external*
cash in and out of an envelope (a contribution/withdrawal — neutral for
performance); `set` records an *observed* value or balance, and the change since the
previous `set` counts as performance.**

| You want to… | Command | Effect on performance |
|---|---|---|
| Buy/sell a quoted security (quantity + price) | `asset buy` / `asset sell` | a trade, builds the cost basis |
| A dividend received / a fee paid on a security | `asset dividend` / `asset fee` | income / cost |
| Record the **observed value** of a property or unlisted holding | `asset set` | change vs the previous value = **performance** |
| Move **external cash** in/out of an envelope | `cash deposit` / `cash withdraw` | a contribution / withdrawal — **neutral** |
| Record the **observed balance** of an envelope | `cash set` | change vs the previous balance = **performance** |

The first `asset set` / `cash set` on an account is treated as an *acquisition* (an
external flow), not as performance — only later changes count.

### Declaring a holding — two equivalent ways

There are **two interchangeable ways** to declare an asset and record activity on it.
They reach the **same end state** — pick whichever fits the moment.

**A. Declare, then record** — set the asset up once (alias, group, ISIN…), then trade it:

```sh
finador asset add CW8.PA --alias cw8 --group equities/world
finador asset buy cw8 100 90000 --account "PEA Zephyr" --label core
```

**B. One shot** — `buy` creates the security on the fly (Yahoo-resolved), accepting the
**same** `--alias`/`--group` at creation and `--label` for the position:

```sh
finador asset buy CW8.PA 100 90000 --account "PEA Zephyr" --group equities/world --alias cw8 --label core
```

Both leave you with the asset declared (alias `cw8`, group `equities/world`), the buy
recorded, and the position tagged `core` — they are equivalent. `asset dividend` and
`asset fee` auto-create the same way. Reach for **A** when setting several assets up
first, or for **ISIN-only funds** (no Yahoo ticker) and **properties**
(`--kind property`) — which only `asset add` can create; reach for **B** for quick entry
of a quoted security.

### Onboard a seasoned account (existing holdings, no history to backfill)

You're starting with a **full PEA**: ~150,000 € contributed over the years, now
worth ~170,000 €, fully invested in a couple of funds. You won't retrace every
trade. The cash didn't stay as cash — you bought shares — so declare today's
**positions**, not a cash balance. Your broker statement gives you, per line, the
**quantity** held and the **amount you invested** (its cost basis / PRU).

```sh
finador account add "PEA Zephyr" --tax gains:17.2%

# asset buy <asset> <shares held> <amount you invested> — straight off your statement.
# For ticker-quoted securities you don't need a separate `asset add`: buy creates them
# on the fly, resolving the name and currency from Yahoo. Tag a position inline with --label.
finador asset buy CW8.PA 100 90000 --account "PEA Zephyr" --group equities/world --alias cw8 --label core
finador asset buy "Convex et Expansion" 50 60000 --account "PEA Zephyr"

# Use `asset add` explicitly for ISIN-only funds (no Yahoo ticker), properties, or to
# set extra metadata (ISIN, withholding tax, aliases) before the first buy.
finador asset add "Convex et Expansion" --isin FR0011111111

finador refresh    # live prices → current value = shares × price
```

`finador value` then values your positions at the live price (~170,000 €) with
the envelope's taxable basis at what you invested (90k + 60k = **150,000 €**), so only
the ~20,000 € gain is taxed — and future growth is taxed on the new excess
automatically. You also get the real **composition**: per-fund allocation, live price
tracking, and `perf` per fund / `--label`.

- **Basis = what you put in.** A `TaxOnGains` envelope's basis is the sum of its `buy`
  costs. For a buy-and-hold PEA that equals your *versements*. If internal churn or
  dividend reinvestment made your cost basis differ from your real versements, anchor
  the exact figure with `finador cash deposit "PEA Zephyr" 150000` and enter the buys
  at cost (the cash nets to ~0) — the deposit then defines the basis whatever happened
  inside the envelope.
- **A fund with no Yahoo quote?** Value it with `finador asset set "<fund>" <current
  value>` instead of relying on `refresh` (the statement is the price fallback).

**Honest note:** you didn't backfill the trades, so the historical gain isn't
attributed to `perf` (finador has no history to compute it from) — only moves **after**
this declaration show up in performance. The tax is exact regardless. Buy dates are
approximate; for a euro account they don't change the basis.

### Buy a real-estate property

A property is valued by dated statements (the Statement model). The **first**
valuation is the acquisition (an external contribution); **later** ones are
performance.

```sh
finador account add "Patrimoine immo" --tax gains:36.2%
finador asset add "Appart Lyon" --kind property --group realestate

finador asset set "Appart Lyon" 250000 --account "Patrimoine immo" --at 2022-06-01  # acquisition
finador asset set "Appart Lyon" 270000 --at 2024-01-01                              # revaluation = performance
```

Once the asset is attached to an account, later `asset set` calls don't need
`--account` again.

### Sell a property (cash decoupled — the real-life flow)

In real life the sale and the money landing on your bank account happen weeks apart,
often on a different account. finador models exactly that: close the position when
it's sold, record the cash when it actually arrives.

```sh
finador asset set "Appart Lyon" 0 --at 2025-09-15        # position closed at the sale

# weeks later, when the proceeds land on the real account:
finador cash set "Compte Épargne" 295000 --at 2025-11-02   # or: cash deposit, if you track that account's flows
```

**Honest note:** between those two dates your net worth reflects the money *in
transit* — the property already at 0, the cash not yet recorded. That gap is real,
not a bug: it's the time your money spent at the notary. History is **not** deleted —
the property's whole valuation trail stays in the ledger.

### Cash

```sh
finador cash set "Livret A" 15000 --at 2026-06-01    # declare an observed balance
finador tx rm <id-prefix>                            # remove an erroneous declaration (id from `tx list`)
finador cash set "Livret A" 0                         # "no more cash" — declares an empty balance
```

Use `cash deposit "Livret A" 500` (not `set`) when you actually *added* €500 from
outside — that's a contribution, neutral for performance, while a `set` 500 higher
than the last balance would be counted as a gain.

### Securities

```sh
finador asset buy CW8 20 @450 --account "PEA Zephyr"   # 20 shares at 450 each
finador asset sell CW8 5 @520                            # sell 5 at 520
finador asset dividend CW8 42.50                         # a dividend received
finador asset fee CW8 9.90                               # a broker fee
```

`@450` is a unit price (total = qty × price); a bare number is the total. The date
defaults to today; add it as a positional `2024-01-20` if needed. If `--account` is
omitted, finador reuses the asset's last account (or the only account / the
`default-account` config).

### Correct the past

Everything is replayed from the ledger, so any past line can be edited or deleted
and every figure recomputes instantly.

```sh
finador tx list --account "PEA Zephyr"             # find the line; copy a unique id prefix
finador tx edit <id-prefix> --qty 100 --total 4567.80  # only the flags you pass change
finador tx rm <id-prefix>                            # delete a line entirely
```

Ids resolve by **unique prefix**, like short git SHAs (`tx edit 8x3k …`). A
correction appends a small record rather than rewriting the file — friendly to
git-synced storage. `finador compact` rewrites a minimal journal dropping the
superseded records; rarely needed.

### Reconcile two machines

If you record on two machines and they drift apart, fold one copy back into the
other:

```sh
finador merge ../laptop2/finador.fin
```

It expects **copies of the same ledger** (same passphrase, same file id) and
refuses to merge unrelated files. Random per-entry ids make this lossless:
additions, deletions and edits of distinct entries union with no loss; when both
copies edited the *same* entry, the **last edit wins by timestamp**; a true tie
(same entry, same instant, different values) prompts you to choose. The merged
ledger is re-sealed in place; the previous file is kept as `.bak`.

## Concepts

**The file is the database.** Everything — accounts, assets, the transaction
ledger, the quote cache — lives in one encrypted `.fin` file (default
`~/.finador.fin`, override with `--db` or `FINADOR_DB`). Copying that file *is*
your backup. All derived state (positions, cost bases, series) is recomputed by
replaying the ledger, so transactions can always be edited or deleted safely.

**Accounts are tax envelopes.** `--tax gains:17.2%` taxes
`max(0, value − contribution basis)`; the basis is what you put in
(`cash deposit` − `cash withdraw`) when the account's cash is tracked, or
`asset buy − asset sell` when it is not. `--tax value:20%` taxes the whole value.
`--tax none` (default) taxes nothing. Estimated tax shows up in `value`, on
net curves and on the web.

**Tracked vs untracked cash.** An account's cash is *tracked* as soon as it has at
least one pure-cash `statement`, `cash deposit` or `cash withdraw`. In a tracked
account, trades move the cash (a buy is value-neutral, like in real life). In an
untracked account, finador assumes you don't care about its cash: buys and sells are
treated as external flows in performance.

**`cash deposit` ≠ `cash set`.** A *contribution* is entered with
`cash deposit`/`cash withdraw` (it feeds the tax basis and XIRR). `cash set`
records an *observed balance*, and the gap between two statements counts as
performance — that's how savings-account interest is captured. The first statement
of an account or property is treated as an *acquisition* (an external flow), not as
performance.

**Asset references.** Anywhere an asset is expected you may use its id, ticker,
ISIN, any alias, or its full name — case-insensitive. If every exact match fails,
a **unique prefix** wins: `finador value cw8` finds `cw8-pa`. An ambiguous prefix
fails and lists the candidates. The same applies to account names.

**Groups are paths.** `--group equities/us/tech` builds a hierarchy; every command
accepts a group prefix as scope and aggregates the subtree.

**Scopes are uniform.** `value`, `perf` and `chart` all take the same optional
scope argument: nothing (whole portfolio), a group or group prefix
(`equities/us`), an account (`"PEA Zephyr"` or `pea`), or an asset (`cw8`).
Resolution order on a free reference: group first, then account, then asset.
`perf` and `value` also accept `--label <name>` to restrict the scope to
positions carrying that label (cannot be combined with a positional scope argument).

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
finador account add <name> [--tax none|gains:N%|value:N%] [--ccy EUR] [--alias a]...
finador account list
```

Account names are free-form (`"CTO IBKR"`); use `--alias` to add short names
that resolve everywhere.

### Assets

```sh
finador asset add <ticker|name> [--kind security|property] [--name n]
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
finador asset buy     <asset> <qty> [@unit-price|total] [date] [--account acc] [--ccy c] [--note n]
finador asset sell    <asset> <qty> [@unit-price|total] [date] [--account acc] [--ccy c] [--note n]
finador asset dividend <asset> <amount> [date] [--account acc] [--ccy c] [--note n]
finador asset fee     <asset> <amount> [date] [--account acc] [--ccy c] [--note n]
finador cash deposit  <account> <amount> [date] [--ccy c] [--note n]
finador cash withdraw <account> <amount> [date] [--ccy c] [--note n]
finador cash set      <account> <balance> [--at YYYY-MM-DD] [--ccy c]
```

Price syntax: `@550` is a unit price (total = qty × 550); a bare `5500` is the
total; the date defaults to today; the order of the optional arguments is free.
A negative quantity on `asset buy` records a sell, but the shell flag parser needs a
`--` first: `finador asset buy -- cw8 -2 @577`.

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
finador value [scope] [--at YYYY-MM-DD] [--ccy USD] [--gross]
              [--by group|account] [--exclude refs]... [--what-if asset=price]...
```

- By default `value` shows gross, estimated tax and net; `--gross` shows the gross value only.
- `--at` values the portfolio at any past date (quotes are forward-filled).
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
row when `--from` is given) — with three complementary measures:

- **TWR** chains daily returns with external flows neutralized: the performance of
  the *strategy*, comparable across scopes.
- **XIRR** is the money-weighted annual rate of *your* euros, contributions
  included. Shown only for windows ≥ 30 days (annualizing a daily move is noise).
- **GAIN** is the money made or lost over the window, *net of contributions* — the
  value change your deposits and onboarding declarations don't explain. Declaring
  "I hold 1000 € of X" is not a 1000 € gain; only what it earns afterwards counts.

Periods that predate your first transaction are omitted (a fresh portfolio has no
"1y") — the `inception` row always shows the real, full span.

Below the table: `tracking since <date> (N d)`, then the annualized figures, each
gated by how much history backs it — **vol, Sharpe, Sortino** appear from ~90 days,
**CAGR** only from a full year (annualizing a few weeks compounds noise into
nonsense). Max drawdown shows whenever there's a dip. Risk-free rate comes from
`config set risk-free 2.4%`; `--to` moves the evaluation date (periods are relative
to it), which makes output reproducible in scripts.

A holding declared at its *average cost* (the onboarding recipe) enters the
performance series at its **market value** on the day you record it, not at cost —
so the latent gain you built up before tracking isn't mistaken for a one-day spike.
Likewise, a property (or any hand-valued holding) is priced by *declaration*, not a
market: each `asset set` re-bases its value as an adjustment, so entering an old
acquisition price and today's value never books years of appreciation as a single
day's return.

### Charts: `chart`

```sh
finador chart [scope] [--net] [--from d] [--to d] [--ccy c]
              [--width 70] [--height 12] [--exclude refs]...
```

Renders the daily value curve as braille in the terminal, `--net` for the
after-tax curve.

### Export: `export`

```sh
finador export [--at YYYY-MM-DD] [--ccy USD] > assets.csv
```

Writes a CSV to stdout, one row per held asset — `ticker,name,isin,gross,net,currency` —
each position valued (gross and after estimated latent tax) like `value`, in the display
currency. The web app serves the same file from the **export CSV** link at the bottom of
the assets tab (`GET /assets.csv`).

### Quotes: `refresh` and `--offline`

```sh
finador refresh        # force-refresh quotes, FX and dividends from Yahoo
```

`value`, `perf` and `chart` refresh stale series automatically (at most once a
day) before computing; network failures degrade to warnings and the cache keeps
working. `--offline` skips all of it. The quote cache lives **inside** the
encrypted file — your ticker list is sensitive metadata.

### Atypical assets (funds by ISIN)

Yahoo Finance is the primary quote source (ticker-based). When Yahoo doesn't cover an asset, finador automatically falls back — **by ISIN** — to two additional providers on every `finador refresh`:

1. **Financial Times** (`markets.ft.com`) — covers a wide range of European funds (SICAV/OPCVM).
2. **Morningstar via Boursorama** — resolves the ISIN to a Morningstar `0P…` id through Boursorama's fund search, then fetches the daily NAV from `tools.morningstar.fr`.

The chain is: **Yahoo → FT → Morningstar**. The first provider that returns data wins; a provider that can't find the asset signals `ErrNotCovered` and the chain falls through transparently.

**Typical usage — a French/Luxembourg fund:**

```sh
finador asset add "Convex AM Europe Small" --isin LU1111111111
finador refresh    # priced via FT or Morningstar automatically
```

**Honest limitation — French employee-savings funds (FCPE/PEE).** Funds distributed through employer plans (e.g. an Selia Sélection fund) are identified by an internal AMF code that is _not_ a real ISIN and is not listed on any public quote source. No provider in the chain covers them. Value them manually:

```sh
finador asset set "Selia Sélection Équilibre" 4250.00 --account "PEE Entreprise"
```

All three providers are implemented with no extra dependency — stdlib HTTP and `regexp` only.

### Web: `serve`

```sh
finador serve [--addr 127.0.0.1:8451]
```

Unlocks the file in the terminal, then serves the whole app — dashboard with
allocation trees (by group / by account), an allocation donut at the bottom of
the overview showing top-level group weights and cash with a muted palette and
an HTML legend, an **assets tab** showing every holding on one dense row with
1W/1M/1Y sparklines and gross/net amounts (with an **export CSV** download),
drill-down scope pages with curves and performance (charts default to full
history, with quiet 1m/3m/1y range links), transaction entry/edit/delete
(the edit page also renames the entry's asset everywhere, by its stable ID),
CSV import, quote refresh.
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
| `default-account` | account used when `--account` is omitted | `pea-zephyr` |

### Remote sync: `remote` and `sync`

Keep the encrypted ledger in a private GitHub repo (full walkthrough in
[*Use a private GitHub repo*](#use-a-private-github-repo-optional)). Local file mode stays the
default; these commands are only for GitHub mode.

```sh
finador remote set <owner>/<repo> [--path finador.fin] [--branch master]  # switch to GitHub mode
finador remote login                                                       # store the token (Keychain)
finador remote adopt                                                       # upload an existing ~/.finador.fin (migration)
finador remote show                                                        # mode, repo, sync state (never the token)
finador remote off                                                         # back to local file mode
finador sync                                                               # force pull, push pending changes
```

| Command | What it does |
|---|---|
| `remote set <owner>/<repo>` | Writes `~/.config/finador/config.json` with `source: github`. `--path` (default `finador.fin`) is the file's path inside the repo; `--branch` (default **`main`** — pass `--branch master` if that's your repo's default branch, finador doesn't auto-detect it). |
| `remote login` | Prompts for the fine-grained PAT, stores it in the macOS Keychain (re-run to rotate), and **verifies it can reach the repo** (a bad/expired token is reported now, not at the next sync). `GITHUB_TOKEN` overrides it. |
| `remote adopt` | Uploads an existing local `.fin` (`--from`, default `~/.finador.fin`) to the remote as-is — a one-time migration. Refuses to overwrite an existing remote file unless `--force`. |
| `remote show` | Prints the active mode, the repo/path/branch and the sync state (last pull, unpushed changes). Never prints the token. |
| `remote off` | Sets `source: local` — commands use `~/.finador.fin` again. |
| `sync` | Forces a pull now (don't wait the hourly refresh) and pushes pending offline changes, reconciling via `merge` if the remote moved. |

**Config file** `~/.config/finador/config.json` (plain JSON, hand-editable):
```json
{
  "source": "github",
  "github": { "owner": "you", "repo": "finador-data", "path": "finador.fin", "branch": "main" },
  "readPullAfter": "1h"
}
```
`source: "local"` (or a missing file) uses the local default. `readPullAfter` is how stale the
local copy may be before a read pulls (default `1h`). `finador --db <path>` or `FINADOR_DB` forces
local mode for a single invocation, whatever the config says.

**Behaviour & errors**
- **Reads** use the local working copy, pulling from GitHub only when it's older than
  `readPullAfter`. **Writes** always pull first, then push — **one commit per save**.
- **Offline:** reads use the local copy; a write succeeds locally, is marked pending, and is
  pushed on the next online command or `sync` — never lost.
- **Concurrent change** (another machine pushed): the push is reconciled automatically (union +
  last-writer-wins by timestamp); a genuine same-field/same-instant clash prompts you to choose.
- **Bad/missing token** → `github authentication failed` (run `finador remote login`) — reported
  distinctly from being offline.
- The GitHub Contents API caps a file at ~1 MB; the ledger (market cache excluded) stays well
  under it. `finador lock` forgets both cached passwords and the GitHub token.
- **Already have a local file?** `init` starts fresh and `sync` only pulls an existing remote —
  neither imports a pre-existing `~/.finador.fin`. Migrate it with **`finador remote adopt`**,
  which uploads the encrypted file as-is (no password needed) and installs it as the working
  copy.
- **The file's name in the repo** is `finador.fin` by default; name it whatever you like with
  `--path my-wallet.fin`. The one rule: the config `--path` must match the actual file name in
  the repo, or finador won't find it (the usual first-time snag).
- **No file found at the remote?** Reads and `sync` report it and show the path/branch they
  looked at — a wrong `--path` or `--branch` (finador defaults to `main`) is the usual cause;
  check `finador remote show`. For a genuinely new repo, run `init` or `remote adopt`.

## CSV import

```sh
finador import transactions.csv
```

Columns are matched by header, in any order:

```csv
date,kind,account,asset,quantity,price,amount,currency,group,note
2026-01-15,buy,PEA Zephyr,CW8.PA,10,550,,EUR,equities/world,first buy
2026-01-20,deposit,PEA Zephyr,,,,5000,EUR,,
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
**group** (`equities/world`), or by **label** (`--label retraite` — all positions
tagged with that label, regardless of envelope). Combine with `--exclude` to drop
specific assets: `finador perf --label retraite --exclude CW8` works. Labels are
attached to (account, asset) pairs via `finador label add`. Exclusions accept any
asset reference (ticker, ISIN, name) and remove the assets *and their flows* from
TWR/XIRR, labelling the output `(excluding …)`.

**What-if analysis.**

```sh
finador value --what-if vizr=280 --net
finador value --what-if cw8=600 --what-if country=520000
```

**Unlisted holdings.** Declare a `security` with no usable ticker, then maintain
dated `asset set` valuations; perf treats the first statement as an acquisition
and later changes as performance. The same mechanics value properties.

**Fixing history.** `tx list --asset cw8` → `tx edit <id-prefix> --qty 12 --total 6600` →
done; every figure recomputes. `asset edit` renames, regroups, retickers or
realiases without touching the ledger. Reference collisions (an alias equal to
another asset's ticker, …) are rejected to keep resolution unambiguous. The file
is an append-only journal, so a correction (`tx edit`, `tx rm`, even on an old
entry) appends a small record rather than rewriting the file — friendly to
git-synced storage. `finador compact` rewrites a minimal journal, dropping the
superseded records; rarely needed.

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

## Use a private GitHub repo (optional)

By default your data is a local file (`~/.finador.fin`). Optionally, finador keeps it in a
**private GitHub repository** and syncs transparently, so you use the same portfolio from
several machines. The repo holds only the **encrypted** file, and the token is scoped to that
one repo. Local mode stays the default and the fallback.

**One-time setup**

1. Create a **private** repo on GitHub, e.g. `you/finador-data` (empty is fine).
2. Create a **fine-grained personal access token** at
   <https://github.com/settings/personal-access-tokens> (Settings → Developer settings →
   Fine-grained tokens → Generate new token). Set **Repository access → Only select
   repositories →** your repo, and the single permission **Contents: Read and write**
   (*Metadata: Read* is added automatically). A free account is enough. The token carries an
   expiry; renew it later by regenerating it on GitHub and re-running `finador remote login`.
3. Point finador at it and store the token (macOS Keychain):
   ```sh
   finador remote set you/finador-data --path finador.fin
   finador remote login          # paste the token  (or: export GITHUB_TOKEN=…)
   finador init                  # creates and pushes the encrypted file
   ```
   **If your repo's default branch isn't `main`** (e.g. `master`), add `--branch master` to
   `remote set` — finador defaults to `main` and does **not** auto-detect the repo's branch.

   **Already have a populated `~/.finador.fin`?** Don't run `init` (it would start empty).
   After `remote set` + `remote login`, migrate it in one command:
   ```sh
   finador remote adopt          # uploads ~/.finador.fin as-is (still encrypted), then reads it
   ```
   On another machine, repeat steps 1–3 with the same repo/token (and matching `--branch`),
   then run any command — it pulls the existing `finador.fin`.

**How sync works**

- **Reads** use a local working copy, refreshed from GitHub when it's older than an hour
  (configurable via `readPullAfter`). `finador sync` forces a refresh now.
- **Writes** pull the latest first, apply the change, then push immediately — **each save is a
  commit**, so GitHub shows your history with the small per-change append-log diffs.
- **Offline**, a write succeeds locally and is pushed on the next online command (or `sync`).
- If another machine pushed meanwhile, finador **reconciles automatically** — the same
  union / last-writer-wins / merge as [`finador merge`](#reconcile-two-machines); random ids +
  timestamps make it lossless for independent changes, and a true same-instant conflict asks
  you to choose.

**Good to know**

- The repo contains only the **AES-256-GCM-encrypted** ledger — a leaked repo reveals nothing.
  The token lives in the Keychain (never in the config or logs); `finador lock` forgets it.
- The **market cache stays local** (it's regenerable) — only the ledger travels, keeping the
  repo small and the diffs clean.
- `finador remote show` prints the mode and sync state; `finador remote off` returns to local;
  `finador --db <path> <cmd>` forces local for a single command.
- Config lives in `~/.config/finador/config.json` (plain JSON, editable by hand).

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
