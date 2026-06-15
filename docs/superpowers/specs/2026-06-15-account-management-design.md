# Account management (web + mobile) - design

**Goal:** Expose account create/list/edit/delete in the web UI and the Android app, matching the
CLI's `account add/edit/list/rm`.

**Context:** Accounts were CLI-only to mutate; the web auto-created them implicitly and mobile was
read-only for accounts (viewer + quick transaction entry). Both UIs now manage accounts directly.

## Data model (decided: no new field)

An account is exactly its four user-facing fields plus a generated id - `Name`, `Currency`,
`TaxRule` (`none` | `gains:N%` | `value:N%`), `Aliases`. There is **no "bank vs trading" /
"cash vs equities" type**: what an account holds is decided by its transactions, not a flag.
Adding such a type would be a `.fin` format change (Go store + Android format + `FORMAT.md` +
version bump + migration) and was rejected as unnecessary - aliases/name cover any labelling need.

## Web (Go) - new "Accounts" tab

Server-rendered, zero-JS, mirroring the Transactions page.

- Nav link in `internal/web/templates/base.html` (Overview · **Accounts** · Assets · Transactions · Import).
- `internal/web/accounts.go`: `GET /accounts` (list + create form), `POST /accounts` (create),
  `GET /accounts/{id}/edit`, `POST /accounts/{id}/edit`, `POST /accounts/{id}/delete`.
- Templates `accounts.html` (list + create) and `account-edit.html`.
- Tax entered as a mode `<select>` + a percentage field, recombined into the canonical
  `gains:17.2%` syntax and parsed by `domain.ParseTaxRule`. Aliases are a comma-separated field.
- Reuses the domain guards: `Book.AddAccount` / `CheckAccountRefs` (collision) and
  `Book.RemoveAccount` (refuses to orphan transactions). Edit applies onto the live pointer and
  restores it on validation/save failure (like `assetRename`).

## Mobile (Android) - under Settings, not the bottom bar

The bottom bar (Portfolio/Gains/Settings) is too scarce for a rarely-used feature, so account
management lives behind **Settings → Manage accounts**.

- **Net-new format-write** (the format layer could *read* `acct`/`acct-del` but not *write* them):
  `Ledger.putAccount(account)` (upsert → `acct` record, used for both create and edit) and
  `Ledger.deleteAccount(id)` (→ `acct-del`). Wired through `AppRepository`
  (new shared `mutateLocked` helper, also adopted by `addTransaction`) and `AppViewModel`.
- **Integrity rules** mirror Go in `domain/AccountRules.kt`: `checkRefs` (id/name/alias collision,
  self skipped by id) and `assertNoTxRefs` (delete guard). Enforced inside the ledger mutators, so
  they run against the post-pull book inside `Sync.mutate` (a thrown check can't corrupt the working
  copy - `mutate` applies `fn` before writing).
- **Screens:** `AccountListScreen` (cards; tap to edit; + to add; delete with a confirmation
  dialog) and `AccountEditorScreen` (name, currency, tax mode dropdown + rate, aliases). The
  shared `DropdownField` was extracted from `TxEntryScreen` so both forms reuse it.

## Testing

- Web: `internal/web/accounts_test.go` - list/create (with tax + aliases), edit pre-fill + update,
  collision rejection, and the delete guard (referenced vs clean).
- Android: `LedgerAccountTest` - write/edit/delete round-trips, collision, delete-guard.
- Byte-compat: `CrossImplProducerTest` now also writes an account; `scripts/crossimpl.sh` asserts
  the Go CLI lists that Android-written account.

## Decisions

- No account "type" flag (would force a format change for no behavioural gain).
- Mobile account management lives under Settings, not as a 4th bottom-bar destination.
- One create/edit ledger primitive (`putAccount`, upsert by id) since the format reconciles by id.
- Web edit replaces aliases wholesale (vs the CLI's add/remove flags) - simpler, same result.
