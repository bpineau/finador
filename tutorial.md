# finador — tutorials

Hands-on walkthroughs. (Names and amounts below are fictional — adapt them to yours.)

## Initializing your portfolio with existing assets

You already have accounts, funds, shares, some cash and maybe a property. You won't
replay years of transactions — you'll declare **today's positions** and what you paid for
them. The model in three ideas:

- Each **account** is a tax envelope: it carries its **tax rule**, and finador shows your
  wealth **gross, estimated latent tax, and net**.
- You declare each holding with `asset buy <asset> <quantity> @<average buy price>`. For a
  `gains` envelope (PEA, CTO, PEE…), the **taxable basis is the sum of your buys** — i.e.
  what you contributed — so only the gain above it is taxed. (For a buy-and-hold envelope
  that equals your *versements*.)
- Quoted holdings are priced live: finador resolves **ETFs and shares via Yahoo (by
  ticker)**, and **funds via Financial Times then Morningstar (by ISIN)** when Yahoo
  doesn't list them. Holdings with no public quote (some employee funds, real estate) you
  value by hand with `asset set`.

### 0. Create the encrypted file

```sh
finador init        # asks for a password (twice), creates ~/.finador.fin
```

The password is then cached in the macOS Keychain for ~12 h per terminal
(`finador config set keychain-ttl 8h` to change it; `finador lock` to forget it now).

### 1. Investment accounts — each with its tax rule

Those are off course fictional accounts and assets, should be replaced by your own.

```sh
# Flat tax / PFU = 30 % on gains; PEA & PEE social levies = 17.2 %.
finador account add "CTO Meridia"            --tax gains:30%    --alias meridia
finador account add "PEA Fortuneo"          --tax gains:17.2%  --alias pea --alias fortuneo
finador account add "PEA-PME Bourse Direct" --tax gains:17.2%  --alias pea-pme --alias pme
finador account add "Assurance Vie Linxea"  --tax gains:30%    --alias av --alias linxea
finador account add "RSU (Morgan Stanley)"  --tax value:30%    --alias rsu     # free shares — see note below
finador account add "PEE"                   --tax gains:17.2%  --alias pee
```

`--alias` gives an account short, case-insensitive names you can use anywhere; add as many
as you like.

### 2. Cash & bank accounts — no tax

```sh
finador account add "Livret A"   --alias livreta
finador account add "LDDS"       --alias ldds
finador account add "Checking"   --alias checking
finador account add "Wise USD" --ccy USD --alias wiseusd # this one holds dollars
```

### 3. A real-estate envelope (to hold the property)

```sh
finador account add "Real Estate" --tax gains:36.2% --alias immo   # 19 % + 17.2 % social, on the gain
```

### 4. Declare your assets

```sh
# ETFs/shares by ticker (Yahoo); funds by ISIN (Financial Times). --group powers allocation & per-group perf.
finador asset add CW8.PA --alias world --group equities/world        # an MSCI World ETF
finador asset add AAPL   --alias apple --group equities/us/tech      # a US share (quoted in USD)
finador asset add "Euro Small-Cap Fund" --isin LU0131510165 --alias smallcap --group equities/europe-small
#   ^ an actively-managed fund with no Yahoo ticker → finador prices it by ISIN via Financial Times
finador asset add "Employer Stock Fund" --isin 990000000000 --alias empfund --group equities/us
#   ^ an employee fund with no public quote → you'll value it by hand (step 7)

finador asset add "Studio Nantes" --kind property --alias studio --group realestate
```

Tip: for ticker-quoted securities you don't even need this step — `asset buy CW8.PA …`
creates the asset on the fly. Declare explicitly (as above) for ISIN-only funds, properties,
or to set the alias/group up front.

### 5. Your holdings — quantity @ average buy price

```sh
# PEA Fortuneo — its taxable basis becomes 40×380 + 5×900 = 19 700 € (≈ your contributions)
# means: "I bought 40 'world' at 380 € (currency for the account) each in my account 'pea'"
finador asset buy world 40 @380   --account pea 
finador asset buy smallcap 5 @900 --account pea

# CTO  Meridia 
finador asset buy apple 30 @170 --account meridia
# Free RSUs,  acquired at ~no cost; the value:30% rule taxes the whole value
# (that's an approximation to simplify), so the buy price is a placeholder.
finador asset buy apple 50 @0.01 --account rsu
# PEE employee fund — bought 100 units at 50 $currency; value comes from step 7 (no public quote)
finador asset buy empfund 100 @50 --account pee
```

The same asset (here `apple`) can be held in several envelopes — each position is taxed by
its own envelope's rule.

### 6. Cash balances

```sh
finador cash set livreta 8000
finador cash set ldds 5000
finador cash set checking 3200
finador cash set wiseusd 12000        # USD account → 12 000 $
```

### 7. Value what has no public quote, and the property

```sh
finador asset set empfund 9000 --account pee                    # employee fund: current value, by hand
finador asset set studio 220000 --account immo --at 2022-03-10  # purchase (+ works) = the acquisition basis
finador asset set studio 260000                                 # current value → 40 000 € gain, taxed at 36.2 %
```

For a property, the **first** `asset set` is the acquisition (the basis); **later** ones are
the current value. The same goes for any holding valued by statement.

### 8. Pull live prices and look at the result

```sh
finador refresh             # ETF/shares via Yahoo, the Euro Small-Cap fund via Financial Times (by ISIN)
finador value               # gross, estimated tax and net (the default)
finador value --by group    # allocation
finador perf                # TWR, XIRR, CAGR, vol, Sharpe… (scope it: `finador perf pea`, `finador perf --label core`)
finador export > assets.csv # one row per holding: ticker, name, ISIN, gross, net (also a button on the web Assets tab)
```

After `refresh`, any holding still showing **“counted as 0”** is one no source could quote
(a typo'd ISIN, or an employee/FCPE fund) — value it with `asset set` as in step 7.

## Everyday operations (after setup)

### Invest cash you already have

The move you'll make most once set up: you have cash and you put it into the market.
The one rule to internalize: **declaring a holding is never a gain.** A `buy` enters the
performance series at that day's *market value* (capital deployed), so a position you
just declared reads **0 %**, not +100 % — only its later moves count.

Say you have 30 000 € sitting on your Livret A and you invest it through your CTO:

```sh
# Mandatory — just record the buys. This IS the whole operation.
finador asset buy world 200 @100 --account meridia    # 20 000 € into the world ETF
finador asset buy apple 60 @166  --account meridia     # ~10 000 € into Apple
```

Each buy builds the position's cost basis and starts tracking its performance from the
market value that day. Nothing else is required.

**The cash side is optional** — only worth it if you want to *see idle, uninvested
balances* (cash waiting at a broker, a savings account's interest). If you do, record
the move as a **pair** (there's no “transfer” verb), which keeps both balances exact and
stays neutral for performance:

```sh
finador cash withdraw livreta 30000   # leaves the Livret A
finador cash deposit  meridia    30000    # lands on the CTO; the buys above then spend it down to ~0
```

Don't use `cash set` to empty the account here: `set` is an *observed balance*, so going
from 30 000 to 0 would be booked as a 30 000 € loss. `withdraw` is the neutral move.

### Switch one holding for another (arbitrage)

Selling one position to buy another **in the same account** is just two transactions —
a `sell` then a `buy` on that account. Say you rotate 3 units of the world ETF into
2 Apple shares inside your CTO (fake prices, ~300 € each side):

```sh
finador asset sell world 3 @100 --account meridia   # 3 units out → 300 €
finador asset buy  apple 2 @150  --account meridia    # 2 shares in → 300 €
```

- **It's neither a gain nor a loss.** You didn't add or remove money — you swapped one
  holding for another — so the arbitrage is neutral for performance; only the *future*
  moves of the new position count. (Under the hood the sell and the buy are equal,
  opposite capital flows that cancel when you reinvest the full proceeds.)
- **Cost basis follows.** The sell trims the world ETF's basis proportionally; the buy
  sets Apple's basis at 300 €, tracked from there.
- **No cash step needed** — both legs are on the same account. If that account's cash is
  tracked, the sell credits it and the buy debits it (any small leftover stays as cash);
  if it's untracked, there's nothing else to record.
- **Trading fees**, if any: `finador asset fee world 1.50 --account meridia` — a cost that
  *does* weigh on performance (unlike the neutral swap itself).

### After selling a property (a house)

In real life the sale and the cash landing on your bank account happen weeks apart,
often on a different account — finador models exactly that: close the position when it's
sold, record the cash when it actually arrives.

```sh
# 1. Close the position the day it's sold (its declared value goes to 0).
finador asset set studio 0 --at 2026-05-20

# 2. When the proceeds land, record the cash on the receiving account —
#    `cash set` if you track that account by observed balance:
finador cash set checking 285000 --at 2026-06-10
#    …or `cash deposit checking 285000` if you track its flows instead (a neutral contribution).
```

- **The gap is real, not a bug.** Between the two dates your net worth shows the money
  *in transit* — the property already at 0, the cash not yet recorded. That's the time it
  spent at the notary.
- **History is kept.** The property's whole valuation trail (220 000 → 260 000 → 0) stays
  in the ledger, so `value` and `perf` recompute from it. Don't `asset rm` it — leaving it
  at 0 preserves the record (and `rm` only works once it has no transactions).
- **Tax.** Once the position is closed its envelope's latent-tax estimate drops to 0 —
  finador estimates *latent* tax on what you still hold, not the tax actually due on a sale.

## Notes & good to know

- **Basis = what you put in.** A `gains` envelope's taxable basis is the sum of its `buy`
  costs; only the excess (and future growth) is taxed. If internal churn or reinvested
  dividends made your real *versements* differ from that sum, anchor them with
  `finador cash deposit "<envelope>" <amount>` and enter the buys at cost (cash nets to ~0).
- **Free shares (RSU / ESPP).** Modeled here as `--tax value:30%` (the whole value is
  flat-taxed, basis ≈ 0). If you have a real acquisition basis (the vesting value you were
  taxed on), use `--tax gains:30%` and `asset buy … @<vesting price>` instead — then only the
  post-vesting gain is flat-taxed.
- **Funds Yahoo doesn't list** are fetched **by ISIN** from Financial Times, then Morningstar
  — so declare such funds with `--isin`. Employee funds identified by an internal AMF code
  (not a real ISIN) have no public quote: value them by hand (step 7).
- **Honest note on performance.** Because you didn't backfill the trades, the *historical*
  gain isn't attributed to `perf` (finador has no history to compute it from) — only moves
  **after** this setup count. Each position enters the performance series at its market value
  on the day you declare it, so the latent gain isn't booked as a one-day spike. Annualized
  figures stay hidden until there's enough history (vol/Sharpe from ~90 days, CAGR from a
  year); until then you see the cumulative return since you started (`tracking since …`). The
  tax estimate is exact regardless.
- **Sync across machines.** To keep this encrypted file in a private GitHub repo and sync it
  automatically, see *Use a private GitHub repo* in the [README](README.md).
- **Tidy up.** After a bulk setup, `finador compact` rewrites a minimal ledger.
