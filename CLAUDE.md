# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

finador is a personal, encrypted wealth tracker in a single pure-Go binary: one
encrypted `.fin` file holds accounts, assets and a transaction ledger, served
through a CLI and a zero-JavaScript web UI. The README is the complete user
manual; read the relevant recipe there before changing user-facing behaviour.

## Commands

```sh
make build          # go build -trimpath -o bin/finador ./cmd/finador
make test           # go test ./... -count=1
make race           # -race on the concurrency-sensitive packages (web, store)
make check          # full gate: fmt-check + vet + lint + test + race
make hooks          # install the pre-commit hook (runs the same gate)

go test ./internal/portfolio -run TestSeries -count=1    # one test
go test ./internal/store -run 'TestMerge/.+' -v           # one subtest
```

The pre-commit hook (`.githooks/pre-commit`) runs fmt-check, vet, lint and
tests; run `make check` before committing so it never surprises you.
`golangci-lint` is optional locally (the Makefile warns and skips), but don't
introduce warnings: `.golangci.yml` documents the few deliberate exclusions.

## Hard constraints

- **Pure Go, no CGo, no JavaScript toolchain.** The web UI is server-rendered
  `html/template` + static CSS, all embedded; no external resources, no CDN.
- **Dependency budget is deliberate**: cobra, shopspring/decimal, samber/lo,
  x/crypto, x/term, and `github.com/bpineau/pofo`. Do not add dependencies for
  things the stdlib does.
- **pofo is a tagged dependency** (`github.com/bpineau/pofo`). For joint
  development add a temporary `replace github.com/bpineau/pofo => ../pofo`,
  but NEVER commit it: a session that changes pofo ends by tagging a pofo
  release and pointing go.mod at it. Market data fetching, performance math
  and chart rendering live in pofo; finador's `market`, `perf` and `chart`
  packages are thin façades that own finador's conventions (domain types,
  0-instead-of-NaN, the house chart style). Fix generic math/fetching bugs
  in pofo, finador-flavor bugs in the façade.
- Everything user-visible (CLI output, errors, web) is **English**. Errors exit
  1 with a single `finador: …` line. Use plain hyphens, never em-dashes, in all
  writing (code, docs, commits).

## Architecture

Dependency direction (never import upward):

```
cmd/finador → cli ─┬→ store ──→ domain
             web ──┤   portfolio → domain          (valuation, series, replay)
                   ├→  perf → domain, pofo/metrics (pure math, no I/O)
                   ├→  market → domain, pofo/marketdata
                   ├→  chart → perf, pofo/chart
                   ├→  remote                      (GitHub sync, never decrypts)
                   └→  keyring                     (passwords: env → Keychain → prompt)
```

- **domain** is the pure model: `Book` (everything persisted), `Account` (a tax
  envelope), `Asset`, `Transaction`, reference resolution. No I/O, imports no
  other internal package. Start reading here (`internal/domain/doc.go`).
- **store** persists the Book as an append-only, AES-256-GCM encrypted,
  hash-chained line journal, with optimistic concurrency (`ErrConcurrent`) and
  atomic writes (tmp + fsync + rename, previous file kept as `.bak`).
  `docs/FORMAT.md` is the public, implementation-grade spec of this format
  (an independent Android client is built on it): **any change to the on-disk
  format must update FORMAT.md and stay readable by that spec**.
- **portfolio** derives everything from the ledger by replay: holdings, cash,
  valuations (`Value`), daily series (`Series`), CSV import/export. `Scope` is
  the uniform "what am I looking at" argument shared by value/perf/chart/web.
- **cli** and **web** are facades over the same engine and must stay
  behaviourally identical. `cli.mutate`/`mutateFile` is the single write path
  (local: open → apply → save; remote: pull → apply → push). The web server
  shares one `store.File` behind a RWMutex and pushes to the remote inside the
  write lock.

## Invariants - do not break

- **The ledger is the only source of truth.** Positions, cost bases, tax bases
  and series are always recomputed by replay. Never persist derived state;
  never mutate it except through a new/edited ledger record.
- **Transaction Quantity and Amount are always positive**; `Kind` carries the
  direction (Buy/Sell, Deposit/Withdraw).
- **decimal in the ledger, float64 in analytics.** Money amounts and quantities
  are `shopspring/decimal`; market quotes, valuations and performance math are
  float64.
- **A buy is never a gain - nor a loss.** External flows are neutralized in
  TWR; a declared holding enters the series at market value; a bought security
  nothing has observed yet (no quote, no statement) is valued at cost, never 0.
  The first `Statement` of an (account, asset) pair or of an account's cash is
  an *acquisition* (an external flow), not performance (decision D8) - except
  when the position was already bought in the ledger: its buys carried the
  flows, so statements are NAV observations, scaled per share (D27).
- **Tracked vs untracked cash**: an account's cash is tracked once it has any
  pure-cash Statement/Deposit/Withdraw (`portfolio.CashTracked`). Tracked:
  trades move cash and are value-neutral. Untracked: trades are external flows.
- **Reference resolution is tiered and must stay unambiguous**: ID → ticker →
  ISIN → alias → name, all case-insensitive, then unique prefix across all of
  them. `CheckAccountRefs`/`CheckAssetRefs` reject collisions at write time;
  keep it that way.
- **Wrong password and tampered file are indistinguishable** - both must yield
  `domain.ErrBadPassword`. Don't add error detail that breaks this.
- **The market cache never touches the synced ledger.** It lives in an
  encrypted local sidecar (`store/cache.go`, key derived per-purpose via HKDF)
  and is regenerable; no plaintext quote data on disk, ever (the ticker list is
  sensitive metadata).
- **Saves are append-mostly and byte-stable**: unchanged record lines are
  re-emitted byte-for-byte so a small change is a small git diff. Entry IDs are
  random and time-sortable (`domain.NewID`), which is what makes `merge` and
  the GitHub sync lossless (union + last-writer-wins by timestamp).
- Web mutations: save atomically, then 303 redirect. No cookies, no sessions,
  no auth - the server binds 127.0.0.1 and warns loudly otherwise.

## Testing conventions

- Tests are table-driven stdlib tests, colocated `_test.go`; no test framework.
- Never hit the network in tests: inject a fake `market.Source` via
  `cli.WithSource`, and a fake `remote.Backend` via `cli.WithRemoteBackend`.
- Point the sidecar cache at a temp dir with `FINADOR_CACHE_DIR` (see
  `store/cache.go`); CLI tests drive the real cobra command against a temp
  `--db` file with `FINADOR_PASSWORD` set.
- `internal/web` and `internal/store` are the race-sensitive packages; run
  `make race` after touching them.

## Where things are decided

- `README.md` - the full user manual; keep its recipes true when changing
  behaviour. Doc style is CLI examples with inline comments, not prose.
- `docs/FORMAT.md` - the normative file-format spec (v3). Code and spec must
  agree; the spec says "if they disagree, the code wins; report it".
- `docs/superpowers/DECISIONS.md` - journal of the non-obvious trade-offs
  (in French), e.g. D8 first-statement-is-a-flow, D10 optimistic locking.
  Add an entry when you make a similar judgement call.
- `docs/superpowers/plans/`, `specs/` - historical design docs, useful context.
- `docs/format-testdata/sample.ledger` - a committed sample file to validate
  independent format implementations, documented in
  `docs/format-testdata/README.md`. (`demo.fin` at the root is a local,
  gitignored scratch ledger - `*.fin` files are never committed.)
- Public examples and fixtures must use fictitious brokers/amounts (CTO Meridia,
  CW8.PA…), never real personal data.
