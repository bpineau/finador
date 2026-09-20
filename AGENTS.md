# AGENTS.md - onboarding for coding agents

Read this first; it should be all you need before acting. Deeper truth lives in
three places: `README.md` (the complete user manual - its recipes must stay
true), `docs/FORMAT.md` (the normative file-format spec) and
`docs/superpowers/DECISIONS.md` (journal of non-obvious trade-offs, in French -
add an entry when you make one).

## What finador is, and what it is for

A personal, encrypted wealth tracker in one pure-Go binary. It answers, for one
household: what is everything worth today, gross / after latent tax / net; what
did it gain over each period and which line produced it; what will be owed if it
is sold. One encrypted `.fin` file holds accounts (tax envelopes), assets and an
append-only transaction ledger; positions, valuations, performance and tax are
never stored, always recomputed by replaying that ledger.

Consumers, and how changes propagate:

| Program | What it is | Direction |
|---|---|---|
| `cmd/finador` | the CLI, the primary interface | this repo |
| `finador serve` | a zero-JavaScript web UI, binds 127.0.0.1, same engine | this repo |
| `../finador-android` | an independent Kotlin phone client | mirrors THIS repo: format and numbers are specified here, ported there, `../finador-android/scripts/crossimpl.sh` proves the two read each other's files |
| `github.com/bpineau/pofo` (checkout `../pofo`) | the generic library: market data fetching (Yahoo -> FT -> Morningstar), performance math, chart rendering | finador DEPENDS on it, by TAG. A generic math or fetching bug is fixed in pofo, tagged there, and go.mod repointed here; a finador-flavour bug is fixed in the thin facade (`market`, `perf`, `chart`) |

The CLI and the phone client read and write the **same ledger in a private
GitHub repo** (Contents API, one commit per save). That is what forces the
unusual properties: byte-stable saves so git diffs stay small, a lossless
last-writer-wins merge, and an implementation-grade public format spec with a
committed sample file anyone can decode.

**Broker-statement import** lands in `internal/importer/<broker>`, one package
per broker, over the shared idempotent write path `portfolio.AddImported`.
Interactive Brokers activity statements are in `ibkr`, the only importer today.
The ledger's `importHash` field is the dedup key (FORMAT.md §4.5), namespaced
per broker AND per statement section (`ibkr:trades:…`), since a broker numbers
its sections from separate sequences. A hand-entered transaction has none, so
the broker-agnostic **already-booked guard** (`portfolio.ManualMatches` /
`portfolio.Adopt`, decision D37) matches a statement line against what the user
typed: `import` then skips it (default), adopts the manual entry
(`--reconcile`) or imports anyway (`--no-guard`), with `--since` and
`--dry-run` alongside.

**Deliberately NOT in scope**: multi-user or server-hosted operation (no
accounts, no auth, no backend); live brokerage connectivity or order placement;
tax filing or legal advice; any JavaScript toolchain; telemetry of any kind.
`./TODO` and `demo.fin` are gitignored local files; `*.fin` is never committed.

## Priorities and non-negotiables

Read these first when a trade-off is unclear; they decide it.

1. **A wrong number is worse than a missing feature.** Money figures are the
   product. A change that makes a valuation, a gain or a tax base subtly wrong
   is a critical bug: fix those before starting anything new.
2. **The ledger is the only source of truth.** Never persist derived state;
   never mutate state except by appending or editing a ledger record. Everything
   else is recomputed, so anything else can be thrown away and rebuilt.
3. **The format is a public contract.** Another implementation reads these
   files. Code and `docs/FORMAT.md` must agree, and the committed sample must
   stay decodable forever.
4. **No plaintext financial data on disk, ever.** Not the ledger, not the quote
   cache (the ticker list alone is sensitive metadata), not a log line.
5. **The dependency budget is deliberate**: cobra, shopspring/decimal,
   samber/lo, x/crypto, x/term, `github.com/bpineau/pofo`. Nothing the standard
   library already does. Pure Go, no CGo, no JavaScript toolchain.
6. **This repo is public: no personal data, anywhere.** Examples, fixtures,
   tests and docs use fictitious brokers, accounts and amounts (PEA Zephyr,
   CTO Meridia, CW8.PA), never a real holding, a real amount, a real name or a
   home path. This applies to commit messages too.
7. **CLI and web stay behaviourally identical.** They are two facades over one
   engine; a feature added to one is a bug in the other.
8. **English everywhere** in code, docs and commits, except
   `docs/superpowers/` which is French and stays French. Errors exit 1 with a
   single `finador: …` line. **Never a typographic dash** (no em-dash, no
   en-dash): a plain hyphen, a comma or parentheses. Doc style is CLI examples
   with inline comments, not prose.
9. **Never assume a market source is unreachable.** Verify live before
   deferring or building a fallback, and watch for anti-bot gates.

## Build, test, verify

```sh
make build       # go build -trimpath -o bin/finador ./cmd/finador
make test        # go test ./... -count=1
make race        # -race on web + store (the concurrency-sensitive packages)
make check       # THE GATE: fmt-check + vet + lint + test + race

go test ./internal/portfolio -run TestSeries -count=1     # one test
go test ./internal/store -run 'TestMerge/.+' -v           # one subtest
```

`make check` is the single completion gate. Green looks like `ok` (or
`no test files`) on every package and nothing else printed: any `FAIL`, any vet
or lint line, any gofmt diff is a failure. It takes a bit over a minute (the
`-race` pass is most of it). Run it before every commit; the pre-commit hook
(`.githooks/pre-commit`, installed by `make hooks`) runs the same gate.
`golangci-lint` is optional locally (the Makefile warns and skips) but do not
introduce warnings; `.golangci.yml` documents the few deliberate exclusions.
There is **no CI** on this repository: the local gate is all there is, so do not
push red.

Drive the real binary without touching real data - do this to verify any
behaviour change end to end, not just its tests:

```sh
export FINADOR_PASSWORD=pw FINADOR_CACHE_DIR=$(mktemp -d)
bin/finador --offline --no-keychain --db /tmp/t.fin init
bin/finador --offline --no-keychain --db /tmp/t.fin account add "CTO Meridia" --tax gains:31.4%
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
                   ├→  importer/* → portfolio      (broker statements)
                   ├→  perf → domain, pofo/metrics (pure math, no I/O)
                   ├→  market → domain, pofo/marketdata
                   ├→  chart → perf, pofo/chart
                   ├→  remote                      (GitHub sync, never decrypts)
                   ├→  keyring                     (passwords: env → Keychain → prompt)
                   └→  paths                       (XDG dirs + one-time migration)
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
- **Cash is declarative** (D29): an account's balance is what pure-cash
  Statement/Deposit/Withdraw records say, and nothing else. Buys, sells,
  dividends and fees never move it; every trade is an external flow, on every
  account. A fee is a positive flow that buys nothing, so it reads as a loss of
  exactly the fee. Value inclusion and flow emission share one predicate,
  `Scope.hasAsset` - never re-split them.
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
  (`domain.NewID`) - what makes merge and the GitHub sync lossless - and
  MONOTONIC within a process (decision D42), so a burst minted inside one
  millisecond replays in minting order instead of a random one.
- Web mutations: save atomically, then 303 redirect. No cookies, no sessions,
  no auth - the server binds 127.0.0.1 and warns loudly otherwise.

## Hard constraints

- **The web UI is server-rendered** `html/template` + embedded static CSS. No
  external resource, no CDN, no JavaScript build step.
- **pofo is a tagged dependency.** For joint development add a temporary
  `replace github.com/bpineau/pofo => ../pofo` but NEVER commit it: a change
  that touches pofo ends by tagging a pofo release (a `v*` tag on pofo is a
  publication) and repointing `go.mod` here.

## Traps - each has actually bitten

- **Record `ts` must be compared as parsed instants, never lexically.**
  RFC3339Nano renders a whole second without a fraction and `'Z' > '.'`, so a
  string compare elects the OLDER write in the last-writer-wins merge. See
  `store/merge.go tsInstant`; the Android client had the same bug.
- **An edit is not a re-import**: a `tx-edit` must carry `importHash` through
  unchanged, or replaying the same broker statement duplicates the corrected
  transaction. Read the other way round, that is what lets `--reconcile` stamp
  a hand-entered transaction with a statement's fingerprint and change nothing
  else: the store diffs the record's JSON, so an adoption must touch exactly
  one field.
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
- **A currency reaches the book three ways**: an account, an asset, and a
  RECORD (a fee in JPY, a deposit in CHF). `market.neededCurrencies` must
  collect all three, and the FX series is back-filled to a week before the
  oldest record (`fxHistoryFloor` + the `HistFrom` guard), because a rate is
  needed at the record's own DATE. A missing rate refuses the total in
  `Value()` and counts as 0 in `Series()`, both naming the record (D43).
- **`--exclude` / `--asset` narrow an envelope, so its latent tax stops being
  defined**: `Scope.wholeEnvelopes` gates the exact rule, and a narrowed scope
  falls back on the per-position estimate with a note (D44). Change `Value()`
  and `Series().valueAt` together, as always.
- **A caller that knows an entity never goes through `ParseScope`**: that
  parser answers a FREE reference and tries the group tier first, so an id
  that is also a group path resolves to the group. Use `AssetScope`,
  `AccountScope`, `GroupScope` (D45).
- **Never sum float64 while iterating a map**: the addition is not
  associative and Go randomizes the order, so the last digits of a total move
  between runs. Iterate `slices.Sorted(maps.Keys(...))` (D46).
- **An extended-hours print is shown, never stored** (D36). `value --extended`
  (or `config set extended-hours true`) routes the spot pass through
  `market.SpotRefreshExtended`, which reports a pre/post print in `Quotes` and
  merges it into no series; the valuation gets it as a `portfolio.PriceOverride`,
  labelled with its session. Keep both properties: never merge it, never print
  it unlabelled.
- **A restated history is a wrong POSITION, not just a wrong price** (D47).
  When a source rewrites its whole series (a split, a redenomination), the
  canary rebuilds the series, and `market.splitRatioFor` confronts the measured
  factor with the usual ratios (2:1, 3:1, 4:1, 3:2, their inverses) to 1 %. On a
  match, `Summary.Actions` prints the exact `finador tx edit … --qty` lines to
  paste: restate the QUANTITIES, leave the AMOUNTS alone, which keeps the cost
  basis and divides the implied unit price. On no match it names nothing, so a
  currency redenomination never becomes an imaginary split. The ledger has NO
  record kind that restates a quantity, and adding one would make an older
  reader reject the file, so that remains a version-bump decision.
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
| Broker-statement import | `internal/importer/<broker>/` (one package per broker; the book is written only through `portfolio.AddImported`) |
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
| Absurd TWR or gain | flow emission in `series.go applyTx`: every trade must emit its flow, and check the D8/D27/D29 rules above |
| Merge lost or misordered an edit | `ts` instant comparison in `store/merge.go` |
| Push conflicts / offline sync surprises | `remote/sync.go` (Dirty persists to disk before any network push); the state JSON sits next to the working copy |
| A test hits the network | missing `cli.WithSource` fake or `FINADOR_CACHE_DIR` |

## Definition of done

- [ ] `make check` green (fmt, vet, lint, tests, race). Nothing else counts.
- [ ] The real binary exercised end to end for any behaviour change, not just
      its unit tests (the `--offline --no-keychain --db /tmp/…` recipe above).
- [ ] Format touched? `docs/FORMAT.md` updated in the same change,
      `docs/format-testdata/sample.ledger` still decodable, and
      `../finador-android/scripts/crossimpl.sh` green.
- [ ] A non-obvious trade-off made? A numbered entry appended to
      `docs/superpowers/DECISIONS.md` (French), and referenced from the trap or
      invariant it creates here.
- [ ] Docs updated in the same commit: `README.md` if a command, a flag or an
      output changed (its recipes must stay true), this file if an invariant,
      a trap or the architecture moved.
- [ ] pofo changed too? pofo tagged, `go.mod` repointed at the tag, and no
      `replace` directive left in the diff.
- [ ] No personal data, no real broker, no real amount, no home path, no secret
      added anywhere, including the commit message.
- [ ] No typographic dash in the diff.
- [ ] Committed to `master` and **pushed**. There is no CI to catch what the
      local gate missed.
- [ ] Anything a human must run by hand (a re-import, a `finador refresh`, a
      migration of their own ledger) said explicitly in the final report.

## Where things are decided

- `README.md` - the full user manual; keep its recipes true.
- `docs/FORMAT.md` - the normative file-format spec (v3).
- `docs/superpowers/DECISIONS.md` - the decision journal (French): D8
  first-statement-is-a-flow, D10 optimistic locking, D26 importHash as the
  external reference, D27 cost fallback + per-share statements…
- `docs/superpowers/plans/`, `specs/` - historical design docs, useful context.
