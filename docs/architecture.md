# Apex Architecture

## Current Phase

**V1 - Steps 5–7: Limit Matching, Partial Fills & Cancellation**

The order domain model (`internal/order`), the FIFO queue + price level data
structures, the order book (`internal/book`), and a matching engine
(`internal/engine`) are all in place. The engine matches limit orders with
price-time priority, generates trades at the resting order's price, handles
partial fills, and supports cancellation.

---

## Intended System Architecture

The following diagram describes the eventual high-level design.
All components below are **future phases** — none are implemented yet.

```
Order Gateway
      ↓
Risk Engine
      ↓
Matching Engine
      ↓
Trade Events
   ↙       ↘
Clearing   Market Data
      ↓
 Event Log / Recovery
```

### Components

- **Order Gateway** — Accepts orders from clients, handles protocol encoding/decoding,
  and forwards them to the risk engine.
- **Risk Engine** — Validates orders against pre-trade risk limits before they reach the
  matching engine.
- **Matching Engine** — Maintains the order book and executes price-time-priority matching.
- **Trade Events** — Output of the matching engine: fills, partial fills, and order state
  changes.
- **Clearing** — Processes trade events to update positions and balances.
- **Market Data** — Distributes real-time market data (order book snapshots, trades) to
  subscribers.
- **Event Log / Recovery** — Persists events for crash recovery and audit.

---

## Build and Project Layout

```
apex/
├── cmd/matching-engine/   — Application entry point
├── internal/order/        — Order domain model (implemented)
├── internal/book/         — FIFO queue, price level, order book (implemented)
├── internal/engine/       — Matching engine: submit, cancel, trades (implemented)
├── internal/              — Further domain packages (future)
├── docs/                  — Architecture and design documents
├── tests/                 — Integration and end-to-end tests (future)
├── go.mod
├── Makefile
├── .gitignore
└── README.md
```
