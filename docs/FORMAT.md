# The finador file format (v3)

This is the open, implementation-grade specification of the encrypted file finador
reads and writes. It is precise enough to build an independent reader/writer (for
example a native Android client) without looking at finador's source. Every detail
below was checked against the reference implementation in
`internal/store/{header,record,log,store,cache}.go` and `internal/domain/id.go`.

The format uses only universal primitives — **AES-256-GCM**, **Argon2id**,
**HKDF-SHA256**, **base64**, **gzip**, **UTF-8 JSON** — so it is implementable on
any platform (Android/Kotlin, iOS/Swift, web/WASM, …).

If anything below disagrees with the code, the code wins; please report it.

---

## 1. Overview

A finador ledger is an **append-only, encrypted, line-oriented text journal**:

- **Line 1** is a clear (unencrypted) JSON header carrying the format version, the
  Argon2id parameters, the salt and a random file id. It is authenticated (its
  bytes feed every record's AAD) but not secret.
- **Lines 2 … N+1** are records, one per line, each a base64 string of
  `nonce ‖ AES-256-GCM(plaintext, AAD)`. Records are **chained**: each record's AAD
  includes the previous record's authentication tag, so reordering, dropping or
  splicing records breaks decryption.
- **The last line** is an authenticated head/trailer that commits the record count
  and the final tag, so truncating the file (dropping trailing records) is detected.

The file is **git-sync-friendly**: a writer re-emits unchanged record lines
byte-for-byte and only appends new records, so a small logical change is a small
diff on disk (see §6).

The **market quote cache** is **not** part of this file. It lives in a separate,
regenerable local sidecar (§7) and must never be written into the synced ledger.

The reference default path is `~/.finador.fin`, but the format is independent of the
filename. (finador's own tooling gitignores `*.fin`; the committed sample in this
spec is named `sample.ledger` for that reason.)

---

## 2. Cryptography

### 2.1 Key derivation — Argon2id + HKDF-SHA256

A single master key is derived from the passphrase with **Argon2id**, then split
into two independent 32-byte subkeys with **HKDF-SHA256**.

```
master   = Argon2id(password, salt, t, m, p, keyLen=32)
keyLog   = HKDF-SHA256(ikm=master, salt=nil, info="finador-ledger-v2", L=32)
keyCache = HKDF-SHA256(ikm=master, salt=nil, info="finador-cache-v2",  L=32)
```

- `password` is the UTF-8 bytes of the passphrase.
- `salt` is the 16-byte `salt` field from the header.
- `t`, `m` (in **KiB**), `p` are the header's `t`, `m`, `p` fields.
- Argon2id output length is **32 bytes**.
- HKDF uses **SHA-256**, an **empty (nil) salt**, and the literal info strings
  exactly as shown — bytes of the ASCII strings **`finador-ledger-v2`** and
  **`finador-cache-v2`** (note: the `-v2` suffix is historical and did not change
  with the v3 record schema). Output length **32 bytes** each.

`keyLog` encrypts/decrypts the ledger records and head. `keyCache` encrypts the
sidecar cache only (§7).

### 2.2 Argon2id parameters and bounds

The default parameters a fresh file is created with:

| Param | Header field | Default | Meaning |
|---|---|---|---|
| time / iterations | `t` | `3` | Argon2 passes |
| memory | `m` | `65536` | KiB (= 64 MiB) |
| parallelism | `p` | `min(4, NumCPU)` | lanes |

The parameters **travel inside the header** of every file, so any file is
self-describing and decryptable with only the passphrase.

A reader **must enforce these bounds before deriving the key** (the parameters are
read from an unauthenticated header; strict bounds stop a forged header from causing
a panic or a memory bomb):

```
kdf  == "argon2id"
1     <= t <= 16
8     <= m <= 1048576        (m in KiB, i.e. <= 1 GiB)
1     <= p <= 16
len(salt) == 16
len(id)   == 16
```

A file whose parameters fall outside these bounds must be rejected without
attempting key derivation.

### 2.3 Symmetric cipher

**AES-256-GCM** (96-bit nonce, 128-bit tag). Each sealed line is
`base64( nonce[12] ‖ ciphertext ‖ tag[16] )` where the Go GCM convention appends the
16-byte tag to the ciphertext (so the trailing 16 bytes of the GCM output are the
tag). Nonces are fresh random 12 bytes per record.

A wrong passphrase and a tampered file are **indistinguishable by design**: both
surface as the same "bad password / corrupt file" error.

---

## 3. File layout & framing

### 3.1 Header (line 1, clear JSON)

```json
{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":65536,"p":4,"salt":"<base64 16 bytes>","id":"<base64 16 bytes>"}
```

| Field | Type | Notes |
|---|---|---|
| `fmt` | string | always `"finador-ledger"`; a file whose `fmt` differs is not a finador file |
| `v` | int | format version, currently **3**; a reader MUST refuse an unknown `v` (§8) |
| `kdf` | string | always `"argon2id"` |
| `t` | int | Argon2id time/iterations |
| `m` | int | Argon2id memory in **KiB** |
| `p` | int | Argon2id parallelism |
| `salt` | string | base64 (standard, padded) of 16 random bytes |
| `id` | string | base64 (standard, padded) of 16 random bytes — the file's stable identity, used only to name the sidecar cache |

`salt` and `id` are standard base64 (the default of Go's `encoding/json` for
`[]byte`): standard alphabet, **with** `=` padding.

The **exact bytes of this line** (the UTF-8 JSON, no trailing newline) are hashed
with SHA-256 to produce `hdrHash`, the 32-byte AAD prefix used by every record and
the head. Editing any byte of the header therefore breaks decryption of the whole
file.

> Implementation note: the reference writer emits the header keys in the order
> above. Readers must not rely on key order — parse it as JSON. The `hdrHash` is
> computed over the **literal on-disk line bytes**, so a reader must hash the raw
> first line rather than a re-serialized form.

### 3.2 Record lines (lines 2 … N+1)

Each record line is:

```
base64_std( nonce[12] ‖ AES-256-GCM(plaintext, AAD) )
```

- `nonce` — 12 random bytes.
- The GCM output is `ciphertext ‖ tag[16]`.
- `plaintext` — the UTF-8 JSON of one record envelope (§4).
- base64 is **standard alphabet, padded**.

**Record AAD** (these are the *exact bytes*, concatenated, no separators):

```
AAD_record(seq) = hdrHash[32] ‖ uint64_big_endian(seq) ‖ prevTag[16]
```

where:

- `hdrHash` = `SHA-256(header-line-bytes)` (32 bytes).
- `seq` = the record's 1-based position (the first record is `seq = 1`), encoded as
  an 8-byte big-endian unsigned integer.
- `prevTag` = the 16-byte GCM tag of the **previous** record. For the **first**
  record (`seq = 1`), `prevTag` is **16 zero bytes**.

The "tag" used for chaining is the trailing 16 bytes of the previous record's GCM
output (i.e. the last 16 bytes of its `ciphertext ‖ tag`).

### 3.3 Head / trailer (last line)

The last line is an authenticated trailer:

```
base64_std( nonce[12] ‖ AES-256-GCM(headPlaintext, AAD_head) )
```

**Head plaintext** (JSON):

```json
{"count":N,"head":"<base64 of tag of record N>"}
```

- `count` = the number of records `N`.
- `head` = the 16-byte GCM tag of the **last** record (`seq = N`), base64-encoded
  (standard, padded). When the file has **zero** records, `head` is the 16 zero
  bytes (`lastTagOrZero`).

**Head AAD** (exact bytes):

```
AAD_head = hdrHash[32] ‖ "finador-head" ‖ uint64_big_endian(count)
```

where `"finador-head"` is the 12 ASCII bytes `66 69 6e 61 64 6f 72 2d 68 65 61 64`,
and `count` is the same `N` as in the plaintext, big-endian uint64.

On read, a reader recomputes everything and verifies all three of:
the head decrypts under `AAD_head`, its `count` equals the number of records it
actually read, and its `head` equals the tag of the last record. Any mismatch ⇒
reject (truncation / tampering detected).

### 3.4 Whitespace

Lines are separated by a single `\n`. The reference reader trims trailing newlines
before splitting. There is no BOM. The whole file is UTF-8.

---

## 4. Record envelope & kinds

### 4.1 Envelope

Every decrypted record plaintext is a JSON object:

```json
{"k":"<kind>","ts":"<RFC3339Nano>","d":{ … }}
```

| Field | Type | Notes |
|---|---|---|
| `k` | string | record kind (enumerated below) |
| `ts` | string | creation timestamp, RFC 3339 with nanoseconds, UTC (e.g. `2026-06-13T13:36:03.896575Z`). Stamped when the record is first written; part of the sealed plaintext. Used as the ordering / last-writer-wins key for merge. |
| `d` | object | the payload, schema depends on `k` |

The entity's own `id` lives **inside `d`**, not in the envelope.

`k` is one of: **`acct`**, **`acct-del`**, **`asset`**, **`asset-del`**,
**`config`**, **`tx`**, **`tx-edit`**, **`tx-del`**.

### 4.2 Identifiers (`id`)

Account, asset and transaction ids are produced by `domain.NewID`:

```
raw[14] = uint48_big_endian(unixMillis)  ‖  rand[8]
id      = crockfordBase32_lowercase_nopad(raw)
```

- `unixMillis` is the current Unix time in **milliseconds**; only its **low 6 bytes**
  are kept (a 48-bit big-endian prefix). Concretely: take the 8-byte big-endian
  encoding of the uint64 millis, drop the top 2 bytes, keep the next 6.
- `rand[8]` is 8 cryptographically random bytes.
- The 14-byte buffer is encoded with **Crockford base32**, **lowercase**, **no
  padding**, using the alphabet:

  ```
  0123456789abcdefghjkmnpqrstvwxyz
  ```

  (Crockford base32 excludes `i`, `l`, `o`, `u`.)
- 14 bytes → **23 characters** (`ceil(112 / 5)`).
- The time prefix makes ids **lexicographically sortable by creation time**; the 64
  random bits make them collision-free across machines, with no shared sequence to
  reimplement.

Example real id: `06fc2cjx2bvtjjxmtmcj2wg`.

References (a transaction's `account` / `asset`) store the **id**. Display resolves
id → human name; input accepts a name, alias, ticker, ISIN or **id prefix**.

### 4.3 Payload (`d`) schemas

These reflect the actual JSON produced by the Go domain types (custom
`MarshalText` means several enums and the tax rule serialize as **strings**, and
decimals serialize as **strings**).

#### `acct` — create/update an account (folds an upsert by `id`)

```json
{"id":"<id>","name":"PEA BforBank","ccy":"EUR","tax":"gains:17.2%","aliases":["pea"]}
```

| Key | Type | Notes |
|---|---|---|
| `id` | string | account id |
| `name` | string | free-form display name |
| `ccy` | string | 3-letter currency code |
| `tax` | string | tax rule (see below); always present |
| `aliases` | string[] | optional; omitted when empty |

**`tax`** is a **string**, one of: `"none"`, `"gains:N%"`, `"value:N%"` (e.g.
`"gains:17.2%"`, `"value:20%"`). `gains` taxes `max(0, value − contribution basis)`;
`value` taxes the whole value; `none` taxes nothing. The percentage is the rate
times 100 (the stored rate is a fraction, rendered back as a percentage; e.g. a
17.2% rule round-trips as `"gains:17.2%"`).

#### `acct-del` — delete an account

```json
{"id":"<account-id>"}
```

#### `asset` — create/update an asset (upsert by `id`)

```json
{"id":"<id>","kind":"security","name":"CW8.PA","ticker":"CW8.PA","isin":"LU…","aliases":["cw8"],"ccy":"EUR","group":"equities/world","withholding":0.15}
```

| Key | Type | Notes |
|---|---|---|
| `id` | string | asset id |
| `kind` | string | **`"security"`** or **`"property"`** |
| `name` | string | display name |
| `ticker` | string | optional; Yahoo symbol for a security (e.g. `CW8.PA`) |
| `isin` | string | optional |
| `aliases` | string[] | optional |
| `ccy` | string | quote/value currency |
| `group` | string | optional hierarchical path, e.g. `equities/us/tech` |
| `withholding` | number | optional float in `[0,1]`; source-tax rate applied to **automatic** dividends |

`kind` is a **string**, not a number. (Internally `security = 1`, `property = 2`,
but the wire form is the text via `MarshalText`.)

#### `asset-del` — delete an asset

```json
{"id":"<asset-id>"}
```

#### `config` — set a config key (folds into a key→value map)

```json
{"key":"default-account","value":"pea-bforbank"}
```

Both `key` and `value` are strings. Known keys include `currency`, `risk-free`,
`keychain-ttl`, `default-account`, but the map is open.

#### `tx` — create a transaction · `tx-edit` — replace it (both upsert by `id`)

`tx` and `tx-edit` carry the **same payload schema**; the kind only signals to a
reader whether this is a first write or a correction. The fold treats both as an
upsert by `id`.

```json
{"id":"<id>","date":"2024-01-20","account":"<account-id>","asset":"<asset-id>","kind":"buy","qty":"20","amount":{"amount":"9000","ccy":"EUR"},"note":"…","importHash":"…"}
```

| Key | Type | Notes |
|---|---|---|
| `id` | string | transaction id |
| `date` | string | civil day `YYYY-MM-DD` (no time, no zone). This is the **business** date; ordering for display uses it. |
| `account` | string | account **id** |
| `asset` | string | asset **id**; optional — **omitted for pure cash** (deposit/withdraw/cash statement) |
| `kind` | string | one of **`buy`, `sell`, `dividend`, `fee`, `deposit`, `withdraw`, `statement`** |
| `qty` | string | decimal as string; `"0"` when not applicable (cash, statement). Always non-negative — `kind` carries direction. |
| `amount` | object | `{"amount":"<decimal string>","ccy":"<3-letter>"}` — always non-negative |
| `note` | string | optional |
| `importHash` | string | optional; CSV-import idempotency fingerprint |

`kind` is a **string**. The CLI's `cash deposit/withdraw` map to `deposit/withdraw`;
`cash set` and `asset set` both record a `statement` (a `statement` with an `asset`
is a property/holding valuation; without an `asset` it is an account cash balance).

#### `tx-del` — delete a transaction

```json
{"id":"<tx-id>"}
```

### 4.4 Decimal & money encoding

Monetary amounts and quantities are exact decimals serialized as **JSON strings**
(`shopspring/decimal`): `"9000"`, `"20"`, `"42.50"`. A reader should parse them as
arbitrary-precision decimals, not floats. `Money` is the object
`{"amount":"<decimal>","ccy":"<currency>"}`.

---

## 5. Replay (fold) semantics

A reader reconstructs the materialized portfolio ("Book") by folding the records
**in file order** (= append order = the `seq` order from §3). State is three
id-keyed collections (accounts, assets, transactions) plus a config map:

| `k` | effect |
|---|---|
| `acct` | upsert account by `id` (replace if present, else append) |
| `acct-del` | remove account with that `id` |
| `asset` | upsert asset by `id` |
| `asset-del` | remove asset with that `id` |
| `config` | set `config[key] = value` |
| `tx` | upsert transaction by `id` |
| `tx-edit` | upsert transaction by `id` (same as `tx`) |
| `tx-del` | remove transaction with that `id` |

There is **no derived numbering**: ids are self-assigned at creation, so nothing
else is reconstructed during the fold. All higher-level state (positions, cost
bases, tax bases, value series) is recomputed afterward from the folded
transactions — it is never stored.

Because every correction is just another record that supersedes or tombstones an
earlier one, history is never rewritten in place; editing or deleting an old entry
appends a small record.

An unknown `k` must be treated as a hard error (§8) — never silently skipped.

---

## 6. Writing

### 6.1 Diff-on-save (append-only, byte-stable prefix)

A writer keeps the verbatim base64 line of every record it has already persisted.
On save it:

1. Computes the **diff** between the last-persisted snapshot and the current Book,
   producing the minimal set of new records (a created/changed entity ⇒ a fresh
   record; a removed entity ⇒ a `*-del` record). Order within a save: `config`,
   then accounts (and account deletes), then assets (and asset deletes), then
   transactions (`tx` for new, `tx-edit` for changed) and transaction deletes —
   definitions before references.
2. **Re-emits all existing record lines byte-for-byte**, then appends the new
   sealed lines continuing the hash chain (each new record's `seq` continues from
   the existing count; its `prevTag` is the running last tag).
3. Each **new** record is stamped with the current UTC time in `ts` at save time.
4. Re-seals the head/trailer for the new total count.

This is what makes the on-disk file an append-only journal: unchanged lines are
identical across saves, so syncing (e.g. git) sees a minimal diff.

### 6.2 Atomic write

The file is written atomically: write to `<path>.tmp`, `fsync`, `close`, rotate the
existing file to `<path>.bak`, then `rename(tmp, path)`. The `.bak` is the previous
full version.

### 6.3 Optimistic concurrency + lock

- A short **advisory file lock** (`flock(LOCK_EX)` on `<path>.lock`) serializes the
  critical section of a save across processes (no-op on platforms without flock).
- **Optimistic concurrency**: a reader records the file's `(size, mtime)` at open;
  on save, if the current `(size, mtime)` differs, the save is refused with a
  "modified by another process — retry" error rather than overwriting.

### 6.4 Compaction

A writer may **compact**: rewrite a minimal log from the current Book (dropping
superseded and tombstoned records) with a fresh chain and fresh `ts` on every
record. This is a full-file rewrite and is rarely needed; it changes ids of nothing
(entity ids are stable) but does discard dead history.

---

## 7. Sidecar cache

The market quote cache is **never** stored in the ledger. It is a separate local
file:

- **Directory**: `os.UserCacheDir()/finador/`, i.e. on macOS
  `~/Library/Caches/finador/`, on Linux `~/.cache/finador/`. The environment
  variable **`FINADOR_CACHE_DIR`** overrides the base directory (used by tests).
- **Filename**: `<id>.cache`, where `<id>` is the header `id` (16 bytes) encoded as
  **base64 RawURL** (URL-safe alphabet, **no padding**). This makes the path
  deterministic and stable across machines for the same ledger.

**File format** (binary, not line-oriented):

```
"FINCACHE2"  ‖  nonce[12]  ‖  AES-256-GCM( gzip( JSON(MarketData) ), AAD="FINCACHE2" )
```

- Magic prefix is the 9 ASCII bytes `FINCACHE2`.
- The cipher is AES-256-GCM under **`keyCache`** (§2.1).
- The AAD is exactly the 9 magic bytes `FINCACHE2`.
- The plaintext is `gzip(UTF-8 JSON of MarketData)`.

`MarketData` JSON shape (all maps keyed by id/currency; everything refetchable):

```json
{
  "prices":    {"<asset-id>": {"points":[{"d":"2024-01-20","c":450.0}], "fetchedAt":"2024-01-21"}},
  "fx":        {"USD":        {"points":[{"d":"…","c":1.08}],            "fetchedAt":"…"}},
  "dividends": {"<asset-id>": [{"exDate":"2024-03-10","amount":1.25}]}
}
```

The cache is fully **regenerable**: a missing, unreadable or stale sidecar is not an
error — a quote refresh rebuilds it. An alternate implementation can ignore the
cache entirely and refetch quotes itself.

---

## 8. Versioning & extensibility policy

The header `v` is the format version (**currently 3**). The rules, in order of
importance for financial safety:

1. **A reader MUST refuse an unknown `v`.** A file with a version the reader does
   not implement is rejected outright — never partially read.
2. **Within a known `v`, a reader MUST ignore unknown fields of a known `k`.** New
   optional fields may be added to an existing record kind **without** a version
   bump (additive, forward/backward-compatible evolution). Implementations should
   therefore tolerate, and round-trip when re-emitting verbatim, fields they do not
   recognize.
3. **A new `k` (or a changed meaning of an existing `k`) REQUIRES a version bump.**
   A reader MUST treat an unknown `k` as a hard error. A financial ledger must never
   silently skip a record it does not understand — doing so could hide money.

The primitives are deliberately universal (AES-256-GCM, Argon2id, HKDF-SHA256,
base64, gzip, UTF-8 JSON), so a conforming reader/writer can be built on Android,
iOS or the web without finador-specific dependencies.

---

## 9. Test vectors

Real, computed values — use them to validate an independent implementation.

### 9.1 KDF vector

Fixed inputs:

```
password (UTF-8)   = correct horse battery staple
salt (hex)         = 000102030405060708090a0b0c0d0e0f
t = 3   m = 65536 (KiB)   p = 4
```

Expected outputs (hex):

```
master   = 853b272a44db1421c02962669a55eb0994f3cab385ed1c4c79253eee19bab49e
keyLog   = 156457f5a4060765068beda9f37d0fa8257deb767190905231e1fc1e4327167b
keyCache = 7c39ddca718165d3a72ccd023957c2af4814c198ce871c0dda490d54e1b00b3a
```

where `master = Argon2id(password, salt, t=3, m=65536, p=4, 32)`,
`keyLog = HKDF-SHA256(master, nil, "finador-ledger-v2", 32)` and
`keyCache = HKDF-SHA256(master, nil, "finador-cache-v2", 32)`.

> Note: these subkeys depend only on the master and the info strings — they are
> independent of `p` beyond its effect on the master. An alternate implementation
> should recompute all three and match exactly.

### 9.2 Sample ledger

A complete, decryptable sample ledger is committed at
**`docs/format-testdata/sample.ledger`**, with its passphrase and expected decoded
contents documented in **`docs/format-testdata/README.md`**. An independent reader
should be able to decrypt it end-to-end (header → records → head) and reproduce the
listed accounts, assets and transactions.
