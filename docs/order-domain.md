# Order Domain

This document describes the order domain model implemented in Step 2.
It is the foundation the matching engine will build on in later phases.

## Design Decisions

### Integer price representation

Prices are always integers representing the **smallest currency unit**:

```
₹100.25  →  10025  (paise)
```

Rationale:

- Floating-point numbers (`float32`/`float64`) cannot represent all decimal
  values exactly, which causes rounding errors in matching and clearing.
- Integer arithmetic is exact, deterministic, and fast.
- The display layer is responsible for converting back to decimal form.

`Price` is a named `int64` type so the domain cannot accidentally mix prices
with raw quantities or IDs without an explicit conversion.

### Integer quantity

Quantities are integer units (`Quantity` as named `int64`). Fractional
quantities are not supported in this phase. `quantity <= 0` is rejected.

### Typed enums

`Side`, `Type`, and `Status` are typed integer enums (`int` based), not raw
strings. Invalid values are detectable: any value outside the defined constants
is rejected by validation. Parse helpers (`ParseSide`, `ParseType`) reject
unrecognized strings with sentinel errors. String conversions exist via
`String()` methods for display/debugging but are never used as identity.

## Order Lifecycle

```
New
 ↓
Open
 ├──→ PartiallyFilled
 │          ↓
 │       Filled
 │
 ├──→ Filled
 │
 └──→ Cancelled
```

Transitions allowed:

| From               | To                        |
|--------------------|---------------------------|
| `New`              | `Open`                    |
| `Open`             | `PartiallyFilled`, `Filled`, `Cancelled` |
| `PartiallyFilled`  | `PartiallyFilled`, `Filled`, `Cancelled` |
| `Filled`           | (terminal)                |
| `Cancelled`        | (terminal)                |

`Fill` requires the order to be `Open` or `PartiallyFilled`. A `New` order
must first transition to `Open` (e.g. when it is accepted into the book).

## Validation Rules

| Rule                              | Rejected with       |
|-----------------------------------|---------------------|
| `ID == 0`                         | `ErrInvalidOrderID` |
| `Symbol == ""`                    | `ErrEmptySymbol`    |
| `Side` outside `Buy`/`Sell`       | `ErrInvalidSide`    |
| `Type` outside `Limit`/`Market`   | `ErrInvalidType`    |
| `Qty <= 0`                        | `ErrInvalidQuantity`|
| `Type == Limit && Price <= 0`     | `ErrInvalidPrice`   |
| `Type == Market && Price != 0`    | `ErrMarketPriceSet` |

### Market-order price handling

A market order has **no limit price**. Validation therefore distinguishes:

- **Limit** → a positive price is required.
- **Market** → price must be zero; a market order carrying a price is invalid.

This keeps the model explicit: the matching layer later knows a market order
should be matched against the best available price, while a limit order is
constrained by its price.

## Fill Semantics

`Fill(qty)` mutates the order only on success:

- Requires the order to be `Open` or `PartiallyFilled`.
- Requires `qty > 0`.
- Requires `qty <= Remaining()`, else `ErrOverfill`.
- Appends to `Filled` and recomputes status:
  - `Filled == Qty` → `Filled`
  - otherwise → `PartiallyFilled`

Invariant maintained:

```
Filled + Remaining == Qty
Filled ≤ Qty
```

Overfill attempts return `ErrOverfill` and leave the order unmodified.

## Cancellation Semantics

`Cancel()` is allowed only from `Open` or `PartiallyFilled`. Cancelling a
`Filled` or already `Cancelled` order returns `ErrCannotCancel`. A `New` order
is not yet in the book and cannot be cancelled until it is `Open`.

## Package layout

```
internal/order/
├── order.go        — types, Order struct, lifecycle & fill methods
├── errors.go       — sentinel errors
└── order_test.go   — unit tests
```

## Out of scope (later phases)

Order book, price levels, FIFO queues, matching algorithms, trade generation,
concurrency, networking, persistence, and event buses are **not** part of the
order domain as implemented here.