# Format test data

This directory holds a committed sample ledger so an independent implementation of
the [finador file format](../FORMAT.md) can validate its reader end-to-end.

> The file is named `sample.ledger` (not `*.fin`) on purpose: finador's own
> `.gitignore` ignores `*.fin`, so the neutral name is committable. The format is
> independent of the filename.

## `sample.ledger`

- **Format version**: 3.
- **Passphrase**: `finador-format-spec-v3`
- **KDF**: Argon2id with the parameters carried in the file's header
  (`t=3`, `m=65536` KiB, `p=4`), then HKDF-SHA256 subkeys — see
  [`../FORMAT.md` §2](../FORMAT.md).

Decrypting it (header line → 7 records → head trailer) must reproduce exactly the
contents below. The ids are stable random ids embedded in the file (§4.2 of the
spec); your reader reads them, it does not regenerate them.

### Header (clear line 1)

```json
{"fmt":"finador-ledger","v":3,"kdf":"argon2id","t":3,"m":65536,"p":4,"salt":"1MXlXNrg6PKyOOgpQkSlmg==","id":"54AFhtcMfCY6IDENZrw6FA=="}
```

### Decoded records (in append order)

Each line below is the decrypted plaintext (the envelope `{"k","ts","d"}`; the `ts`
values are the real save-time stamps and will differ in any file you regenerate).

| seq | `k` | `d` (payload) |
|---|---|---|
| 1 | `acct` | `{"id":"06fc2cjx2bvtjjxmtmcj2wg","name":"PEA Zephyr","ccy":"EUR","tax":"gains:17.2%","aliases":["pea"]}` |
| 2 | `acct` | `{"id":"06fc2cjx8mhna9ngpakpvm0","name":"Livret A","ccy":"EUR","tax":"none"}` |
| 3 | `asset` | `{"id":"06fc2cjxf7702jynqpzgga8","kind":"property","name":"Appart Lyon","ccy":"EUR","group":"realestate"}` |
| 4 | `asset` | `{"id":"06fc2cjxndn8wez8qqhh0a0","kind":"security","name":"CW8.PA","ticker":"CW8.PA","aliases":["cw8"],"ccy":"EUR","group":"equities/world"}` |
| 5 | `tx` | `{"id":"06fc2cjy0zkkye41veyhr80","date":"2024-01-15","account":"06fc2cjx2bvtjjxmtmcj2wg","kind":"deposit","qty":"0","amount":{"amount":"10000","ccy":"EUR"}}` |
| 6 | `tx` | `{"id":"06fc2cjy7j4ncgb2p6mg0a0","date":"2024-01-20","account":"06fc2cjx2bvtjjxmtmcj2wg","asset":"06fc2cjxndn8wez8qqhh0a0","kind":"buy","qty":"20","amount":{"amount":"9000","ccy":"EUR"}}` |
| 7 | `tx` | `{"id":"06fc2cjye7137cmsvzjh4br","date":"2026-06-01","account":"06fc2cjx8mhna9ngpakpvm0","kind":"statement","qty":"0","amount":{"amount":"15000","ccy":"EUR"}}` |

The head trailer commits `count = 7` and the GCM tag of record 7.

### Folded result (after replaying the 7 records)

Two accounts:

| id | name | ccy | tax | aliases |
|---|---|---|---|---|
| `06fc2cjx2bvtjjxmtmcj2wg` | PEA Zephyr | EUR | gains:17.2% | `pea` |
| `06fc2cjx8mhna9ngpakpvm0` | Livret A | EUR | none | — |

Two assets:

| id | kind | name | ticker | group |
|---|---|---|---|---|
| `06fc2cjxf7702jynqpzgga8` | property | Appart Lyon | — | realestate |
| `06fc2cjxndn8wez8qqhh0a0` | security | CW8.PA | CW8.PA | equities/world (alias `cw8`) |

Three transactions:

| id | date | kind | account | asset | qty | amount |
|---|---|---|---|---|---|---|
| `06fc2cjy0zkkye41veyhr80` | 2024-01-15 | deposit | PEA Zephyr | — | 0 | 10000 EUR |
| `06fc2cjy7j4ncgb2p6mg0a0` | 2024-01-20 | buy | PEA Zephyr | CW8.PA | 20 | 9000 EUR |
| `06fc2cjye7137cmsvzjh4br` | 2026-06-01 | statement | Livret A | — | 0 | 15000 EUR |

(No `config`, `*-edit` or `*-del` records — this sample exercises only the create
path. Decoding it confirms the header parse, key derivation, the AAD chain, the
record envelope and payload schemas, and the head trailer.)

You can reproduce the same listing with the reference binary:

```sh
FINADOR_PASSWORD=finador-format-spec-v3 \
  finador --offline --db docs/format-testdata/sample.ledger account list
```
