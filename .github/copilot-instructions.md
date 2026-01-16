# Copilot Instructions for balefire

Purpose: Give AI coding agents the minimal, project-specific context to be productive in this repo.

## Big Picture
- Go + Kratos v2 shell; entry in [main.go](main.go#L1-L35) builds `kratos.App` with logging hooks but no servers wired.
- Goal: reliable task/message transport resilient to unstable networks; design requirements captured in [api/design/design.md](api/design/design.md).
- Public surface: [api/transport/transport.go](api/transport/transport.go#L1-L36) defines `Transport`, `Handler`, `Message`, `Kind` abstractions; consumers must stay on this interface.
- Concrete transport lives under [transport/](transport); current `p2pTransport` skeleton in [transport/transport.go](transport/transport.go#L1-L33) is unimplemented and should hide libp2p types.

## Build & Run
- Dependencies: `go mod tidy`
- Build: `go build ./...`
- Run: `go run . -mode=leaf` (flag declared in [main.go](main.go#L8-L23)).

## Implementation Guardrails
- Keep libp2p or other network deps confined to `transport/`; do not leak types across `api/transport`.
- All transport operations are context-first and should be cancel-aware (`Start`, `Stop`, `Send`, `Handler`).
- `Start/Stop` fit Kratos lifecycle; when used as a server, register with `kratos.Server(...)` in `main.go`-style app wiring.
- Message contract: implement `Kind()`, `ID()`, `Payload()`; use enums `KindPing`, `KindPong`, `KindData`, `KindACK`.
- Logging: prefer Kratos `log` (`log.Infof`, `log.Errorf`); avoid `fmt.Println`.

## Reliability / Behavior expectations (from design doc)
- Outbox WAL for pending sends with replay on reconnect; TTL/capacity bounds for local store.
- Idempotency via stable message IDs and dedup on receive.
- Retry policy uses exponential backoff with jitter: $t = \min(cap, base \cdot 2^{n}) + jitter$.
- Support chunking for large payloads, cumulative ACK handling, DLQ after max retries, and optional keepalive/priority queues.
- P2P discovery/relay assumed via libp2p; keep these details encapsulated beneath the `Transport` interface.

## Quick wiring example
- Pattern: construct transport (e.g., `NewP2PTransport(cfg)`), call `OnReceive` with handler, and register it with `kratos.New(..., kratos.Server(tr))` similar to the conceptual snippet already in the instructions.

## Open areas
- `p2pTransport` methods are unimplemented; add constructors/config surface under `transport/`.
- No tests or lint setup yet; add targeted tests around transport semantics when implementing.
