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
