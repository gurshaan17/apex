# Apex Architecture

## Current Phase

**V1 - Step 2: Order Domain**

The repository foundation (Step 1) and the order domain model
(`internal/order`) are in place. Order types, validation, and lifecycle are
implemented. No matching-engine functionality has been implemented.

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
├── internal/              — Further domain packages (future)
├── docs/                  — Architecture and design documents
├── tests/                 — Integration and end-to-end tests (future)
├── go.mod
├── Makefile
├── .gitignore
└── README.md
```
