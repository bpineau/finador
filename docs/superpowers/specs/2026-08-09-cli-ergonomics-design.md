# CLI ergonomics - design

**Goal:** five independent ergonomic fixes to the command line: XDG paths (with
migration of existing data), a `config get` that tells the whole truth, an
`--asset` filter on the read commands, and plural command names.

Each section is self-contained; they share no state and can land in any order.

## 1. XDG paths, with migration

### Today

| What | Resolver | macOS today | Linux today |
|---|---|---|---|
| default ledger | `cli.defaultDB()` | `~/.finador.fin` | `~/.finador.fin` |
| `config.json` | `os.UserConfigDir()` | `~/Library/Application Support/finador/` | `~/.config/finador/` |
| quote cache, GitHub checkout | `os.UserCacheDir()` | `~/Library/Caches/finador/` | `~/.cache/finador/` |

The README already documents `~/.config/finador/config.json`, which is a lie on
macOS. FORMAT.md §11 documents both platform paths.

### Decision

One resolver package, `internal/paths`, is the single answer to "where does
finador keep things":

```go
func Config() (string, error) // <base>/finador  - config.json
func Cache() (string, error)  // <base>/finador  - *.cache, checkout/
func Data() (string, error)   // <base>/finador  - finador.fin
```

Resolution order per directory, first hit wins:

1. `FINADOR_CONFIG_DIR` / `FINADOR_CACHE_DIR` / `FINADOR_DATA_DIR` (the first two
   already exist and keep their exact meaning: they name the finador directory
   itself, not a base, and disable migration).
2. `XDG_CONFIG_HOME` / `XDG_CACHE_HOME` / `XDG_DATA_HOME` when set and absolute.
3. `~/.config`, `~/.cache`, `~/.local/share`.

Linux behaviour is unchanged (this is what `os.UserConfigDir` already returns).
macOS moves out of `~/Library`. The default ledger moves from `~/.finador.fin`
to `~/.local/share/finador/finador.fin` on every platform.

### Migration

Lazy, at the first resolution of each directory in a process, and independent
per item. Each migration runs only when the destination does **not** exist and
the legacy location does:

| Legacy | New | Moves |
|---|---|---|
| `~/Library/Application Support/finador/` | `<config>/finador/` | whole directory |
| `~/Library/Caches/finador/` | `<cache>/finador/` | whole directory |
| `~/.finador.fin` (+ `.bak`) | `<data>/finador/finador.fin` (+ `.bak`) | the two files |

Rules:

- `os.Rename` only, never a copy: same volume in every realistic case, atomic,
  and it cannot leave two divergent ledgers behind. If `os.Rename` fails
  (cross-device, permissions), the migration is abandoned and the **legacy path
  is used for this run**, with one warning on stderr. Never a partial state.
- One `migrated <old> -> <new>` line on stderr per migration, once.
- A `FINADOR_*_DIR` or `FINADOR_DB` override, or an explicit `--db`, skips the
  corresponding migration entirely: the user named a path, we obey it.
- The ledger migration also moves `<db>.bak` when present, after the ledger.
- Consequence, documented in the release note: the keychain entry is keyed by
  path (`keyring.Key`), so the password is prompted once after the move.

### `--db` help text

In GitHub mode the file actually opened is the working copy under
`<cache>/finador/checkout/<hash>.fin`, so `(default "~/.finador.fin")` was wrong
twice over. `New()` resolves the effective ledger path once (remote working copy
when `config.json` says `source: github`, otherwise the local default) and uses
it as the flag's default value:

```
--db string   encrypted data file, and forces local mode
              (default "/Users/ben/.cache/finador/checkout/ab12cd.fin")
```

`a.dbPath` keeps its current role: `dataSource()` still keys local-vs-remote off
`Changed("db")` and `FINADOR_DB`, never off the value, so a default that happens
to equal the working copy changes no behaviour. If resolving the remote path
fails, the local default is shown.

## 2. `config get` shows everything

A registry of known keys lives next to the code that consumes them, as a table
in `internal/cli/config.go`:

| key | default | meaning |
|---|---|---|
| `currency` | `EUR` | display currency |
| `default-account` | *(none)* | envelope used when a command omits `--account` |
| `keychain-ttl` | `12h` | how long the password stays in the keychain |
| `risk-free` | `0%` | risk-free rate for Sharpe/Sortino |

`finador config get` with no argument prints, in this order:

```
# ledger: /Users/ben/.cache/finador/checkout/ab12cd.fin
# config: /Users/ben/.config/finador/config.json   (source = github)
currency        = EUR              # default
default-account = CTO Meridia
keychain-ttl    = 12h              # default
risk-free       = 2.5%
unknown-key     = whatever         # unknown
```

- The two comment lines come first and carry the real resolved paths; the
  `config.json` line names the current `source`, which is where `finador remote`
  writes (see §4).
- Every known key is listed even when unset, with its effective value and a
  `# default` marker when the value comes from the table rather than the ledger.
- Keys present in the ledger but absent from the table are listed after, marked
  `# unknown`, so a typo is visible.
- Keys are aligned on `=`, sorted, `# default` / `# unknown` comments aligned.

`finador config get <key>` keeps printing one raw value on stdout (scriptable),
but now prints the effective value, so `keychain-ttl` answers `12h` instead of
an empty line. An unknown key still prints an empty line, exit 0.

`finador config set <key> <value>` writes as it does today, and additionally
warns on stderr `warning: unknown config key "keychain_ttl"` when the key is not
in the table. It never refuses: forward compatibility with keys a newer version
may read.

## 3. `--asset` on value, perf and chart

### Decision

No new `ScopeKind`. `portfolio.Scope` grows one field, the exact twin of the
existing `Excluded`:

```go
Only map[domain.AssetID]bool // when non-nil, the only assets in scope (CLI --asset)
```

Both predicates change, and nowhere else:

```go
func (s Scope) hasAsset(acc *domain.Account, asset *domain.Asset) bool {
    if s.Excluded[asset.ID] || (s.Only != nil && !s.Only[asset.ID]) {
        return false
    }
    ...
}

func (s Scope) hasCash(acc *domain.Account) bool {
    if s.Only != nil {
        return false // an asset filter excludes cash: it is not an asset
    }
    ...
}
```

Because value, series, breakdown, the trees and the web all route through
`hasAsset`/`hasCash` (invariant: "value inclusion and flow emission share one
predicate"), the filter reaches every consumer at once, and flows stay
consistent with values.

`Only` must be propagated like `Excluded` by the derived-scope constructors:
`EnvelopeScope`, `IntersectScope` and `PairScope` carry it through.

### Surface

`--asset` on `value`, `perf` and `chart`, repeatable and accepting comma lists,
resolved through `b.Asset()` exactly like `--exclude`:

```sh
finador value --asset CW8                    # one asset, all envelopes
finador perf --asset CW8,AAPL                # compounded: one figure for the pair
finador chart --asset CW8 --asset AAPL       # one curve for the pair
finador perf "PEA Zephyr" --asset CW8        # intersection with a [scope]
finador value --label retraite --asset CW8   # intersection with a label
```

Composition, not exclusion: `--asset` narrows whatever scope the `[scope]`
argument and `--label` produced. `--asset` with `--exclude` on the same asset
yields an empty scope, which is not an error (a zero valuation) - `--exclude`
wins by construction.

Resolution happens in `cli.resolveScope`, which already owns the
`[scope]`/`--label`/`--exclude` triple; it gains an `only []string` parameter.
The scope label becomes:

- no `[scope]`, no `--label`: the joined asset names, e.g. `CW8, AAPL`.
- otherwise: `PEA Zephyr › CW8, AAPL`.
- `--exclude` keeps appending its ` (excluding …)` suffix after that.

`--assets` is accepted as a spelling of `--asset` through the flag normalizer of
§5. The web UI is not touched.

## 4. `remote` stays a top-level command

Rejected: making `remote` a subcommand of `config`. They are two different
stores with two different lifetimes - `config` keys live *inside* the encrypted
ledger, while `config.json` must be readable *before* finador knows where the
ledger is - and `remote` also talks to the network and the keychain. The real
complaint (nothing shows you the whole picture) is answered by §2: `config get`
now prints the `config.json` path and its active source. No alias is added.

## 5. Plural command names and plural flags

### Commands

An explicit alias table, not a mechanical `+ "s"` (which would produce
`refreshs`, `cashs`, `inits`):

| command | aliases |
|---|---|
| `account` | `accounts` |
| `asset` | `assets` |
| `label` | `labels` |
| `tx` | `txs`, `transactions` |
| `value` | `values` |
| `perf` | `perfs` |
| `chart` | `charts` |

The deposit/withdraw/buy/sell families are verbs with no sensible plural and are
left alone.

Implemented with cobra's `Aliases` field on the command, so `finador perf
--help` documents them under "Aliases:" and completion picks them up.

### Flags

`root.SetGlobalNormalizationFunc` maps a plural flag name to its canonical
singular for the repeatable flags: `--assets` → `--asset`, `--excludes` →
`--exclude`, `--what-ifs` → `--what-if`. The normalizer is inherited by every
subcommand's flag set, so the rule is stated once.

## Testing

- `internal/paths`: table-driven resolution (env override > XDG > home fallback)
  and migration (destination exists → no move; source missing → no move; both
  present → destination wins; rename failure → legacy path plus warning), all on
  `t.TempDir()` with `HOME`/`XDG_*` set per case. No macOS-only test: the legacy
  locations are parameters of the migration function, not constants inside it.
- `internal/cli`: `config get` golden output on a temp ledger (paths pointed at
  temp dirs through the env overrides), `config get <key>` effective value,
  `config set` unknown-key warning.
- `internal/portfolio`: `Only` filters positions and drops cash, in `Value`,
  `Series` (flows too, not just values) and `Breakdown`; `EnvelopeScope` carries
  `Only` through.
- `internal/cli`: `value/perf/chart --asset` end to end on a two-asset fixture,
  including the intersection with a `[scope]` and with `--label`, and the
  endpoint equality between `value --asset X` and the last point of
  `chart --asset X`.
- Plurals: one test asserting each alias resolves to the same command, plus
  `--assets` reaching the `--asset` flag.

## Documentation

- `README.md`: the paths section, the `--db` line, the `remote adopt --from`
  default, the config section (new `config get` output), and `--asset` in the
  value/perf/chart recipes.
- `docs/FORMAT.md`: §7 (sidecar cache directory: XDG, not `~/Library`) and §1
  (the reference default ledger path, currently `~/.finador.fin`).
- `docs/superpowers/DECISIONS.md`: one entry for the XDG move plus migration
  policy (rename-only, destination-wins), one for `Scope.Only` as a filter
  rather than a new `ScopeKind`.
