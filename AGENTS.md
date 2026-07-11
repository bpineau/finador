# AGENTS.md - onboarding for coding agents

Read this first; it should be all you need before acting. Deeper truth lives in
three places: `README.md` (the complete user manual - its recipes must stay
true), `docs/FORMAT.md` (the normative file-format spec) and
`docs/superpowers/DECISIONS.md` (journal of non-obvious trade-offs, in French -
add an entry when you make one).

## What finador is, and why it is shaped this way

A personal, encrypted wealth tracker in one pure-Go binary. One encrypted
`.fin` file holds accounts (tax envelopes), assets and an append-only
transaction ledger; positions, valuations, performance and tax are always
recomputed by replaying it. Served through a CLI and a zero-JavaScript web UI
(`finador serve`, binds 127.0.0.1).

Context that explains most design choices:

- The author uses it daily: this CLI on a laptop, plus **`../finador-android`**
  (an independent Kotlin client) on a phone, both reading/writing the **same
  ledger in a private GitHub repo** (Contents API, one commit per save). Hence
  byte-stable saves (small git diffs), a lossless merge, and a public
  implementation-grade format spec with a committed sample file.
- **`github.com/bpineau/pofo`** (sibling checkout `../pofo`) owns everything
  generic: market data fetching (Yahoo → FT → Morningstar), performance math,
  chart rendering. finador's `market`, `perf` and `chart` packages are thin
  facades owning only finador's conventions (domain types, 0-instead-of-NaN,
  the house chart style). Fix generic math/fetching bugs in pofo,
  finador-flavor bugs in the facade.
- Next planned feature: importing broker statements (Interactive Brokers,
  Saxo…) with idempotent replays. The ledger's `importHash` field is the dedup
  key, already specified (FORMAT.md §4.5) and supported end to end.
- `./TODO` and `demo.fin` are gitignored personal files (roadmap, scratch
  ledger); `*.fin` files are never committed.

## Working rules

- Solo repo: commit to **master**, and **commit + push at the end of every
  session of changes**. Fix critical bugs before starting new work.
- `make check` before every commit; the pre-commit hook (`.githooks/pre-commit`,
  installed by `make hooks`) runs the same gate.
- Everything user-visible and in code/docs is **English**. Errors exit 1 with a
  single `finador: …` line. Plain hyphens only - **never an em-dash** - in
  code, docs and commits. Doc style is CLI examples with inline comments, not
  prose.
- Never assume market sources (Yahoo, etc.) are unreachable; verify live before
  deferring or building fallbacks - watch for anti-bot gates.
- Public examples and fixtures use fictitious brokers/tickers/amounts
  (PEA Zephyr, CTO Meridia, CW8.PA…), never real personal data.

## Build, test, verify

```sh
make build       # go build -trimpath -o bin/finador ./cmd/finador
make test        # go test ./... -count=1
make race        # -race on web + store (the concurrency-sensitive packages)
make check       # full gate: fmt-check + vet + lint + test + race

go test ./internal/portfolio -run TestSeries -count=1     # one test
go test ./internal/store -run 'TestMerge/.+' -v           # one subtest
```

`golangci-lint` is optional locally (the Makefile warns and skips) but do not
introduce warnings; `.golangci.yml` documents the few deliberate exclusions.

Drive the real binary without touching real data - do this to verify any
behaviour change end to end, not just its tests:

```sh
export FINADOR_PASSWORD=pw FINADOR_CACHE_DIR=$(mktemp -d)
bin/finador --offline --no-keychain --db /tmp/t.fin init
bin/finador --offline --no-keychain --db /tmp/t.fin account add "CTO Meridia" --tax gains:30%
bin/finador --offline --no-keychain --db /tmp/t.fin value
```

**If you touched the on-disk format**: update `docs/FORMAT.md` in the same
change (code and spec must agree; on drift the code wins - report it), keep
`docs/format-testdata/sample.ledger` decodable (pinned, with the KDF vectors,
by `internal/store/format_test.go`), and run the cross-implementation gate
against the Android client: `../finador-android/scripts/crossimpl.sh`
(needs `make build` first).

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

- **domain** - the pure model, no I/O, imports no internal package. Start
  reading at `internal/domain/doc.go`: `Book` (all persisted state), `Account`,
  `Asset`, `Transaction`, tiered reference resolution, `NewID`.
- **store** - the encrypted, hash-chained, append-only line journal (`log.go`),
  atomic writes and optimistic concurrency (`store.go`), the lossless merge
  (`merge.go`), the encrypted sidecar quote cache (`cache.go`).
- **portfolio** - the replay engine: `Holdings`/`Quantity` (replay.go),
  `Value` (value.go), daily `Series` + external flows (series.go), CSV
  import/export. `Scope` (scope.go) is the uniform "what am I looking at"
  argument shared by value/perf/chart/web.
- **cli** and **web** are facades over the same engine and must stay
  behaviourally identical. `cli.mutate`/`mutateFile` (cli.go) is the single
  write path: local = open → apply → save; remote = pull → apply → push. The
  web server shares one `store.File` behind a RWMutex and pushes to the remote
  inside the write lock.
- **remote** - GitHub sync of the encrypted bytes (never decrypts). The working
  copy and its state sidecar live under `<user-cache>/finador/checkout/`.

## Invariants - do not break

- **The ledger is the only source of truth.** Never persist derived state;
  never mutate state except through a new/edited ledger record.
- **Transaction Quantity and Amount are always positive**; `Kind` carries the
  direction (Buy/Sell, Deposit/Withdraw).
- **decimal in the ledger, float64 in analytics.** Ledger amounts/quantities
  are `shopspring/decimal`; quotes, valuations and performance are float64.
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
- **Reference resolution stays unambiguous**: ID → ticker → ISIN → alias →
  name, case-insensitive, then unique prefix across all of them.
  `CheckAccountRefs`/`CheckAssetRefs` reject collisions at write time; they
  skip *self by pointer identity*, so edits must mutate entities in place.
- **Wrong password and tampered file are indistinguishable** - both must yield
  `domain.ErrBadPassword`. Never add error detail that breaks this.
- **The market cache never touches the synced ledger.** It lives in an
  encrypted local sidecar (`store/cache.go`, key derived per purpose via HKDF)
  and is regenerable; no plaintext quote data on disk, ever (the ticker list
  is sensitive metadata).
- **Saves are append-mostly and byte-stable**: unchanged record lines are
  re-emitted byte-for-byte. Entity IDs are random and time-sortable
  (`domain.NewID`) - what makes merge and the GitHub sync lossless.
- Web mutations: save atomically, then 303 redirect. No cookies, no sessions,
  no auth - the server binds 127.0.0.1 and warns loudly otherwise.

## Hard constraints

- **Pure Go, no CGo, no JavaScript toolchain.** The web UI is server-rendered
  `html/template` + embedded static CSS; no external resources, no CDN.
- **Dependency budget is deliberate**: cobra, shopspring/decimal, samber/lo,
  x/crypto, x/term, `github.com/bpineau/pofo`. Nothing the stdlib already does.
- **pofo is a tagged dependency.** For joint development add a temporary
  `replace github.com/bpineau/pofo => ../pofo` but NEVER commit it: a session
  that changes pofo ends by tagging a pofo release and repointing go.mod.

## Traps - each has actually bitten

- **Record `ts` must be compared as parsed instants, never lexically.**
  RFC3339Nano renders a whole second without a fraction and `'Z' > '.'`, so a
  string compare elects the OLDER write in the last-writer-wins merge. See
  `store/merge.go tsInstant`; the Android client had the same bug.
- **An edit is not a re-import**: a `tx-edit` must carry `importHash` through
  unchanged, or replaying the same broker statement duplicates the corrected
  transaction.
- **A security statement declares the pair's TOTAL value at that date.** Use it
  per share (total / qty-at-statement × current qty) or later buys and sells
  make it lie. Property statements are whole-estimate re-declarations: every
  re-base is an external flow, never performance.
- **`Value()` and `Series()` must agree pointwise** (same fallback chain, same
  tax rules) - endpoint-equality tests pin it; change both together.
- **Never re-serialize existing record lines on save** - byte-stability keeps
  git diffs small and the hash chain intact. Only append.
- Period windows anchor on **the last real close**, not calendar today
  (`perf.CloseAnchor`), or "1d" measures FX drift against a stale close.
- Quote series must stay in the asset's declared currency: refresh drops
  off-currency answers instead of merging them (`market/refresh.go`).
- Unit/identifier bugs in market data are critical: always test the exact
  identifiers the user provides (ISINs, `.PA` tickers…), not lookalikes.

## Where to change what

| Task | Where |
|---|---|
| Valuation logic, tax bases | `portfolio/value.go` (mirror in `series.go`) |
| Daily series, external flows, TWR inputs | `portfolio/series.go` |
| Performance windows/metrics facade | `internal/perf/` (math itself in pofo/metrics) |
| File format, crypto, merge | `internal/store/` + `docs/FORMAT.md` + cross-impl gate |
| Market fetch policy (what/when to fetch) | `market/refresh.go` (fetching itself in pofo) |
| New CLI command | `internal/cli/` (one file per command family; writes go through `a.mutate`) |
| Web page or handler | `internal/web/` (embedded templates; keep CLI parity) |
| GitHub sync behaviour | `remote/sync.go` (the state machine is documented inline) |
| Model, IDs, reference resolution | `internal/domain/` |

## Testing conventions

- Table-driven stdlib tests, colocated `_test.go`, no test framework.
- **Never hit the network in tests**: inject a fake `market.Source` via
  `cli.WithSource` and a fake `remote.Backend` via `cli.WithRemoteBackend`.
- Point the sidecar cache at a temp dir with `FINADOR_CACHE_DIR`; CLI tests
  drive the real cobra command against a temp `--db` with `FINADOR_PASSWORD`.
- `internal/web` and `internal/store` are race-sensitive: run `make race`
  after touching them.
- `docs/format-testdata/sample.ledger` is the committed reference fixture
  (passphrase in `docs/format-testdata/README.md`); independent readers must
  be able to decode it forever.

## Troubleshooting map

| Symptom | Look at |
|---|---|
| "bad password or corrupted file" with a good password | truncation/tampering or format drift: `store/log.go` (AAD chain, head trailer); the previous version survives as `<db>.bak` |
| "modified by another process - retry" | the optimistic lock (`store.ErrConcurrent`) doing its job: retry the command |
| Wrong valuation | the fallback chain in `portfolio/value.go` (price → per-share statement → cost); stale or missing quotes (`finador refresh`, sidecar cache) |
| Absurd TWR or gain | flow emission in `series.go applyTx`: check tracked vs untracked cash and the D8/D27 rules above |
| Merge lost or misordered an edit | `ts` instant comparison in `store/merge.go` |
| Push conflicts / offline sync surprises | `remote/sync.go` (Dirty persists to disk before any network push); the state JSON sits next to the working copy |
| A test hits the network | missing `cli.WithSource` fake or `FINADOR_CACHE_DIR` |

## Where things are decided

- `README.md` - the full user manual; keep its recipes true.
- `docs/FORMAT.md` - the normative file-format spec (v3).
- `docs/superpowers/DECISIONS.md` - the decision journal (French): D8
  first-statement-is-a-flow, D10 optimistic locking, D26 importHash as the
  external reference, D27 cost fallback + per-share statements…
- `docs/superpowers/plans/`, `specs/` - historical design docs, useful context.
