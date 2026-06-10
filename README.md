# finador

Encrypted personal wealth tracker — CLI and web, single binary. Your data lives in
**one encrypted file** on your machine, accessible both **from the command line and
in a browser**.

```
$ finador value --net
portfolio — 2026-06-10
LINE      GROSS          TAX          NET
equities  18,050.00 EUR  361.20 EUR   17,688.80 EUR
property  450,000.00 EUR 9,000.00 EUR 441,000.00 EUR
cash      18,010.00 EUR  0.00 EUR     18,010.00 EUR
TOTAL     486,060.00 EUR 9,361.20 EUR 476,698.80 EUR
```

## Features

- **One file, encrypted.** All state (accounts, transactions, quote cache) is stored in
  a single `.fin` file sealed with Argon2id + AES-256-GCM. Password prompted at startup,
  optionally cached in the macOS Keychain (TTL 12 h; `finador lock` to purge).
- **Tax envelopes.** Each account carries a rule — `gains:17.2%` (gains above deposits
  are taxed) or `value:20%` (entire balance is taxed). Every view shows **gross,
  estimated latent tax, net** — including on curves.
- **Any asset.** Listed securities (Yahoo Finance quotes, automatic dividends, cross-FX
  via USD), cash balances via statements, and arbitrary properties (`"Maison a Acheres"`)
  via dated valuations.
- **True performance metrics.** TWR (strategy performance, flows neutralised) and XIRR
  (what your money actually earned) across periods (`1d`, `5d`, `1m`, `3m`, `ytd`, `1y`,
  `prev-yr`, `inception`), plus CAGR, volatility, Sharpe, Sortino, max drawdown.
- **Curves everywhere.** Braille in the terminal, SVG in the web — gross or net.
- **Uniform scopes.** Every command accepts the same scope: nothing (full portfolio),
  a hierarchical group (`equities/world`), an account, or a single asset.
- **What-if.** Disposable price hypotheses — `value --what-if btc=95000` shows the
  hypothetical value and delta vs actual. Never persisted.
- **Exclusions.** Any scope can exclude one or more assets —
  `perf --exclude btc,vizr`, `value equities --exclude aapl`.
- **Short references.** `add cw8` works if `cw8` is an unambiguous prefix of an id,
  ticker, ISIN, alias or name. Ambiguity lists candidates.

## Build

Go >= 1.26, nothing else (pure Go: no CGo, no JS toolchain).

```sh
go build -trimpath -o bin/finador ./cmd/finador
```

## Getting started

```sh
# Create the encrypted book
finador init                                           # creates ~/.finador.fin

# Add tax envelopes (accounts)
finador account add "PEA Zephyr" --tax gains:17.2%
finador account add "CTO IBKR"     --tax gains:30%
finador account add "Livret A"

# Add a listed security (resolved via Yahoo: name, currency)
finador asset add CW8.PA --id cw8 --group equities/world
finador add cw8 10 @550 2026-06-01 --account "PEA Zephyr"
finador deposit "PEA Zephyr" 5000           # external contribution (tax basis, XIRR)

# Cash account: record a balance statement
finador cash set "Livret A" 11250 --at 2026-06-01  # statement balance; gaps = performance

# Real property
finador asset add "Maison a Acheres" --id maison --kind property --group property
finador asset set maison 450000 --account "Livret A"

# Read it back
finador value --net                           # gross / estimated tax / net
finador value --by account                   # break down by account instead of group
finador perf equities                        # TWR, XIRR, CAGR, vol, Sharpe, Sortino, maxDD
finador chart --net                          # braille curve in the terminal
finador serve                                # http://127.0.0.1:8451 — zero-JS web app
```

**`deposit` vs `cash set`:** use `deposit`/`withdraw` for an external cash transfer
(it feeds the tax basis and the XIRR cashflow stream). Use `cash set` to record a
statement balance; the difference between successive statements counts as performance
(interest on a savings account, for example).

## CSV import

```sh
finador import transactions.csv
```

Columns matched by header, order free: `date, kind, account, asset, quantity, price,
amount, currency, group, note`. Either `price` (unit price) or `amount` (total) — the
other is inferred. Unknown accounts and assets are created on the fly. Import is
**idempotent**: re-importing the same file adds no duplicates.
`kind` values: `buy`, `sell`, `deposit`, `withdraw`, `dividend`, `fee`, `statement`.

## Web

`finador serve` unlocks the file in the terminal then serves the app on `127.0.0.1:8451`:
dashboard (net worth headline, curve, allocation, performance, holdings), views by
group / account / asset, entry creation and deletion, CSV import, quote refresh.
Zero JavaScript, zero external resources — everything is server-rendered and embedded
in the binary. No web authentication: do not expose beyond 127.0.0.1 (a warning is
displayed if you do).

The allocation panel has three tabs: **by group**, **by account**, **by asset**
(`?by=group|account|asset`). Each level is cross-linked and collapsible via native
`<details>/<summary>` — no JavaScript.

## Configuration

```sh
finador config set currency EUR             # display currency
finador config set risk-free 2.4%          # risk-free rate for Sharpe / Sortino
finador config set keychain-ttl 8h         # password cache duration
finador config set default-account pea-zephyr  # default account for entries
```

`--offline` (all commands) disables network access and works from the cache.
`finador refresh` forces a quote update.
`FINADOR_PASSWORD` supplies the password for scripting.
`FINADOR_DB` overrides the default file path.

## Data model & security

- **The transaction ledger is the single source of truth.** Positions, tax bases and
  performance series are all recomputed by replaying transactions. Transactions are
  editable (`finador tx list/edit/rm`).
- **File format:** `magic ‖ version ‖ Argon2id params ‖ salt ‖ nonce ‖
  AES-256-GCM(gzip(JSON))`, with authenticated header (AAD), atomic write via `.bak`.
  A wrong password and a corrupted file are indistinguishable by construction.
- **Quote cache lives inside the encrypted file.** Your ticker list is sensitive
  metadata.
- Design decisions are recorded in `docs/superpowers/DECISIONS.md`; spec and
  implementation plans are under `docs/superpowers/`.

## Known limits

Tax is approximated per envelope, broken down position by position; no benchmark
support; Yahoo Finance quotes are unofficial.
See `docs/superpowers/specs/2026-06-09-finador-design.md` §11.
