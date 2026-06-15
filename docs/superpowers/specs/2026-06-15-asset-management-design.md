# Asset management (web + mobile) — design

**Goal:** Expose asset create/list/edit/delete in the web UI and the Android app, matching the
CLI's `asset add/edit/list/rm`. Mirrors the just-shipped account-management feature.

**Context:** Assets could be created implicitly (the web/CLI auto-create an asset named in a
transaction) but only the CLI could edit their metadata; mobile was read-only. Both UIs now manage
assets directly, alongside accounts.

## Data model (no new field)

An asset is `Kind` (`security` | `property`), `Name`, `Ticker`, `ISIN`, `Aliases`, `Currency`,
`Group`, `Withholding` (a source-tax fraction on automatic dividends) plus a generated id. The
editor surfaces every field; the id is generated, never edited.

## Web (Go) — full CRUD on the existing Assets tab

Server-rendered, zero-JS, mirroring the Accounts page.

- The list stays at `GET /assets` (per-asset valuation table + sparklines). The **plural** namespace
  carries the CRUD so it doesn't collide with the singular `GET /asset/{ref}` valuation scope view:
  `POST /assets` (create), `GET /assets/{id}/edit`, `POST /assets/{id}/edit`,
  `POST /assets/{id}/delete`.
- `assets.html` gains a create form and a "manage assets" table (every asset, with edit/delete);
  new `asset-edit.html` is the full editor. Kind is a `<select>`; withholding is a `%` field parsed
  by `domain.ParsePercent` ("15%"/"15"); aliases are comma-separated.
- Reuses the domain guards: `Book.AddAsset` / `CheckAssetRefs` (collision) and `Book.RemoveAsset`
  (refuses to orphan a transaction AND purges the asset's market cache). Edit applies onto the live
  pointer and restores it on validation/save failure, like `assetRename`.
- The existing per-transaction `POST /asset/{id}/rename` (`assetRename`) is kept — it's the
  ID-based rename reached from the transaction-edit page.

## Mobile (Android) — under Settings, next to "Manage accounts"

- **Format-write** (the layer could read `asset`/`asset-del` but not write them):
  `Ledger.putAsset(asset)` (upsert → `asset` record, create and edit) and `Ledger.deleteAsset(id)`
  (→ `asset-del`). Wired through `AppRepository` (`mutateLocked`) and `AppViewModel`.
- **Integrity rules** mirror Go in `domain/AssetRules.kt`: `checkRefs`
  (id/ticker/isin/name/alias collision, self skipped by id) and `assertNoTxRefs` (delete guard),
  enforced inside the ledger mutators so they run against the post-pull book in `Sync.mutate`.
- **Screens:** `AssetListScreen` (cards; tap to edit; + to add; delete with confirmation) and
  `AssetEditorScreen` (name, kind dropdown, ticker, ISIN, currency, group, aliases, withholding %
  shown for securities). Reuses the shared `DropdownField`.
- The mobile market cache (sidecar) is regenerable, so delete only emits `asset-del` — no purge.

## Testing

- Web: `internal/web/assets_test.go` — list/create (isin + aliases + group + withholding), edit
  pre-fill + update, collision rejection, and the delete guard (referenced vs clean).
- Android: `LedgerAssetTest` — write/edit/delete round-trips, collision, delete-guard.
- Byte-compat: `CrossImplProducerTest` now also writes an asset; `scripts/crossimpl.sh` asserts the
  Go CLI lists that Android-written asset.

## Decisions

- CRUD lives under the plural `/assets/...` namespace to avoid the singular `/asset/{ref}` view.
- One create/edit ledger primitive (`putAsset`, upsert by id) since the format reconciles by id.
- Web edit replaces aliases wholesale (vs the CLI's add/remove flags) — simpler, same result.
- Mobile asset management lives under Settings, not as a bottom-bar destination.
