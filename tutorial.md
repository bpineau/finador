# finador tutorials

Hands-on walkthroughs. (Names and amounts below are fictional, adapt them to yours.)

## Initializing your portfolio with existing assets

You already have accounts, funds, shares, some cash and maybe a property. You won't
replay years of transactions - you'll declare **today's positions** and what you paid for
them. The model in four ideas:

- Each **account** is a tax envelope: it carries its **tax rule**, and finador shows your
  wealth **gross, estimated latent tax, and net**, converted to your default currency.
- You can pre-declare assets (with "finador asset add ...") to register them with aliases
  or disambiguate them by providing the associated ISIN.
- You declare each holding with `asset buy <asset> <quantity> @<average buy price>`. For a
  `gains` envelope (PEA, CTO, PEE…), the **taxable basis is the sum of your buys** - i.e.
  what you contributed - so only the gain above it is taxed. (For a buy-and-hold envelope
  that equals your *versements*.)
- Quoted holdings are priced live (refreshed once per hour): finador resolves **ETFs and
  shares via Yahoo (by ticker)**, and **funds via Financial Times then Morningstar (by ISIN)**
  when Yahoo doesn't list them. Holdings with no public quote (some employee funds, real
  estate) you value by hand with `asset set`.

### 0. Create the encrypted file (GitHub-private from the start)

Finador can use a local file as ledger, but most people want **GitHub mode** (private repo +
auto-sync) from the very first command.

One-time on GitHub (web UI): create an **empty private repo**, and a **fine-grained PAT**:
Settings → Developer settings → Personal access tokens → Fine-grained; *Repository
access* = that one repo; *Permissions → Contents: Read and write*.

```sh
# point at the private repo (these are the defaults)
finador remote set <owner>/<repo> --path finador.fin --branch master

# paste the PAT (hidden input; access is verified on the spot)
finador remote login

# asks the wallet password (×2) → creates the .fin AND first-pushes it
finador init

# mode, repo, sync state (never the token)
finador remote show

# from here, every command pulls-before / pushes-after transparently.
```

Two distinct secrets: the **wallet password** (asked by `init`) encrypts the file; the
**PAT** only moves that encrypted file to/from GitHub. The repo never holds either in
clear. Password cached in the macOS Keychain (`finador lock` to forget; `finador config
set keychain-ttl 8h` to change the TTL) when available.

```sh
# Local-only instead (no GitHub):
finador init                       # creates ~/.finador.fin
# Already local and want GitHub later? Migrate in one shot:
finador remote set <owner>/<repo>  # + finador remote login, then:
finador remote adopt               # uploads your existing .fin as-is
```

### 1. Investment accounts - each with its tax rule

Those are off course fictional accounts and assets, should be replaced by your own.

```sh
# Flat tax / PFU = 31.4 % on gains; PEA & PEE social levies = 18.6 %.
finador account add "CTO Saxo"              --tax gains:31.4%  --alias saxo
finador account add "PEA Fortuneo"          --tax gains:18.6%  --alias pea --alias fortuneo
finador account add "PEA-PME Bourse Direct" --tax gains:18.6%  --alias pea-pme --alias pme
finador account add "Assurance Vie Linxea"  --tax gains:31.4%  --alias av --alias linxea
finador account add "PEE"                   --tax gains:18.6%  --alias pee

# example with free shares (taxes applies to the whole "value" not just "gains").
finador account add "RSU (Morgan Stanley)"  --tax value:31.4%    --alias rsu
```

`--alias` gives an account short, case-insensitive names you can use anywhere; add as many
as you like.

### 2. Cash & bank accounts - no tax

```sh
finador account add "Livret A"   --alias livreta
finador account add "LDDS"       --alias ldds
finador account add "Checking"   --alias checking
finador account add "Wise USD" --ccy USD --alias wiseusd # this one holds dollars
```

### 3. A real-estate envelope (to hold the property)

```sh
finador account add "Real Estate" --tax gains:37.6% --alias immo   # 19 % + 18.6 % social, on the gain
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

Tip: for ticker-quoted securities you don't even need this step - `asset buy CW8.PA …`
creates the asset on the fly. Declare explicitly (as above) for ISIN-only funds, properties,
or to set the alias/group up front.

### 5. Your holdings - quantity @ average buy price

```sh
# PEA Fortuneo - its taxable basis becomes 40×380 + 5×900 = 19 700 € (≈ your contributions)
# means: "I bought 40 'world' at 380 € (currency for the account) each in my account 'pea'"
finador asset buy world 40 @380   --account pea 
finador asset buy smallcap 5 @900 --account pea

# CTO  Saxo 
finador asset buy apple 30 @170 --account saxo

# Free RSUs,  acquired at ~no cost; the value:31.4% rule taxes the whole value
# (that's an approximation to simplify), so the buy price is a placeholder.
finador asset buy apple 50 @0.01 --account rsu

# PEE employee fund - bought 100 units at 50 $currency; value comes from step 7 (no public quote)
finador asset buy empfund 100 @50 --account pee
```

The same asset (here `apple`) can be held in several envelopes - each position is taxed by
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
finador asset set studio 260000                                 # current value → 40 000 € gain, taxed at 37.6 %
```

For a property, the **first** `asset set` is the acquisition (the basis); **later** ones are
the current value. The same goes for any holding valued by statement.

### 8. Pull live prices and look at the result

```sh
finador refresh             # ETF/shares via Yahoo, the Euro Small-Cap fund via Financial Times (by ISIN)
finador value               # gross, estimated tax and net (the default)
finador value --by group    # allocation
finador perf                # TWR, XIRR, CAGR, vol, Sharpe… (scope it: `finador perf pea`, `finador perf --label core`)
finador export > assets.csv # every holding incl. cash: kind, ticker, name, ISIN, gross, net (also a button on the web Assets tab)
```

After `refresh`, any holding still showing **“counted as 0”** is one no source could quote
(a typo'd ISIN, or an employee/FCPE fund) - value it with `asset set` as in step 7.

## Everyday operations (after setup)

### Invest cash you already have

Key rule: **a declared holding is never a gain** - a `buy` enters the series at that day's
market value, so it reads 0 %, not +100 %. Say you invest 30 000 € of cash via your CTO:

```sh
# MANDATORY - just record the buys. This IS the whole operation.
finador asset buy world 200 @100 --account saxo    # 20 000 € into the world ETF
finador asset buy apple 60 @166  --account saxo     # ~10 000 € into Apple

# OPTIONAL - the cash side, only if you want to see idle / uninvested balances.
# A move between accounts = a pair (no "transfer" verb), neutral for performance:
finador cash withdraw livreta 30000   # leaves the Livret A
finador cash deposit  saxo    30000    # lands on the CTO; the buys above spend it down to ~0
# Don't `cash set` to empty an account: a 30000→0 drop would be booked as a 30 000 € loss.
```

### Switch one holding for another (arbitrage)

Same account = a `sell` then a `buy`.

```sh
finador asset sell world 3 @100 --account saxo   # 3 units out → 300 €
finador asset buy  apple 2 @150  --account saxo   # 2 shares in → 300 €; neutral for perf (a swap, not money in/out)
finador asset fee  world 1.50    --account saxo    # any trading cost - this one DOES weigh on performance
# the sell trims the world ETF's basis proportionally; the buy sets Apple's basis, tracked from there.
# the sell also CREDITS the proceeds as cash on the account (unlike Finary/Yahoo, where a declared
# sale just closes the position) - so a sale you don't reinvest stays visible as cash to redeploy later.
```

### After selling a property (a house)

Close the position when sold; record the cash when it actually lands (often weeks later,
on another account).

```sh
finador asset set studio 0 --at 2026-05-20         # sold → close the position (value goes to 0)
finador cash set checking 285000 --at 2026-06-10   # proceeds when they land (or: cash deposit, if you track that account's flows)
# Between the two dates net worth shows the money in transit (at the notary) - real, not a bug.
# Don't `asset rm studio`: leaving it at 0 keeps its whole 220k→260k→0 trail; value/perf recompute from it.
# Once closed, the envelope's latent-tax estimate drops to 0 (it estimates latent tax on what you still hold).
```

## Notes & good to know

- **Basis = what you put in.** A `gains` envelope's taxable basis is the sum of its `buy`
  costs; only the excess (and future growth) is taxed. If internal churn or reinvested
  dividends made your real *versements* differ from that sum, anchor them with
  `finador cash deposit "<envelope>" <amount>` and enter the buys at cost (cash nets to ~0).
- **Free shares (RSU / ESPP).** Modeled here as `--tax value:31.4%` (the whole value is
  flat-taxed, basis ≈ 0). If you have a real acquisition basis (the vesting value you were
  taxed on), use `--tax gains:31.4%` and `asset buy … @<vesting price>` instead - then only the
  post-vesting gain is flat-taxed.
- **Funds Yahoo doesn't list** are fetched **by ISIN** from Financial Times, then Morningstar
  - so declare such funds with `--isin`. Employee funds identified by an internal AMF code
  (not a real ISIN) have no public quote: value them by hand (step 7).
- **Honest note on performance.** Because you didn't backfill the trades, the *historical*
  gain isn't attributed to `perf` (finador has no history to compute it from) - only moves
  **after** this setup count. Each position enters the performance series at its market value
  on the day you declare it, so the latent gain isn't booked as a one-day spike. Annualized
  figures stay hidden until there's enough history (vol/Sharpe from ~90 days, CAGR from a
  year); until then you see the cumulative return since you started (`tracking since …`). The
  tax estimate is exact regardless.
- **Sync across machines.** To keep this encrypted file in a private GitHub repo and sync it
  automatically, see *Use a private GitHub repo* in the [README](README.md).
- **Tidy up.** After a bulk setup, `finador compact` rewrites a minimal ledger.
