// Package domain is finador's pure model: no I/O, no other internal imports.
//
// The center is the [Book] - exactly what the encrypted file persists:
//
//   - [Account]: a tax envelope where assets and cash are held. Its [TaxRule]
//     estimates latent tax, either on gains beyond the contribution basis
//     (PEA/CTO-style) or on the whole value (PER-style).
//   - [Asset]: anything owned - a quoted [Security] (valued at market price)
//     or a [Property] (valued from dated Statement estimates). Cash is not an
//     asset; it belongs to each account.
//   - [Transaction]: one line of the append-only ledger. The ledger is the
//     single source of truth: positions, cost bases, tax bases and value
//     series are always recomputed by replaying it (package portfolio), so
//     editing or deleting history is always safe.
//   - [MarketData]: the refetchable quote/FX/dividend cache. It rides along in
//     the Book in memory but is persisted separately (an encrypted local
//     sidecar, see package store) - losing it costs one refresh, never data.
//
// Conventions that hold everywhere:
//
//   - Ledger amounts and quantities are exact decimals; market quotes and
//     analytics are float64. Exactness lives in the ledger, not in prices.
//   - Transaction Quantity and Amount are always positive; [TxKind] carries
//     the direction (Buy/Sell, Deposit/Withdraw).
//   - Entity IDs come from [NewID]: random, time-sortable, never reused -
//     what makes two diverged copies of a ledger mergeable without loss.
//   - User references (an account or asset named on the command line) resolve
//     through tiers - ID, ticker, ISIN, alias, name, then unique prefix - all
//     case-insensitive; see [Book.Account] and [Book.Asset]. Write paths
//     reject reference collisions so resolution stays unambiguous.
//   - [Date] is a civil day (no clock, no zone); day precision is enough for
//     a personal ledger and keeps series alignment trivial.
package domain
