# AGENTS.md

## Project Overview
- Balefire is a Go + Kratos v2 P2P task/message transport focused on reliability over unstable networks.
- Public surface lives in [api/transport/transport.go](api/transport/transport.go): `Kind`, `Message`, `Handler`, `Transport`.
- Libp2p-backed transport is scoped to [transport/transport.go](transport/transport.go); keep libp2p types private to this package.
- App entrypoint is [main.go](main.go); design expectations are detailed in [api/design/design.md](api/design/design.md).

## Setup
- Requirements: Go 1.25.x, git.
- Install dependencies: `go mod tidy`
- Build: `go build ./...`
- Run (leaf mode default): `go run . -mode=leaf`
- Logging: use Kratos `log` package (already wired in `main.go`); avoid `fmt.Println`.

## Development Workflow
- All transport operations are context-first and must honor cancellation (`Start`, `Stop`, `Send`, handlers).
- Do not leak libp2p or other network types outside `transport/`; only expose the `Transport` interface.
- When acting as a server, register the transport with `kratos.Server(...)` in the app wiring.
- Stable message IDs for idempotency; deduplicate on receive before processing.
- Persist outbound messages (outbox/WAL) with TTL and capacity limits; replay on reconnect.
- Support chunking for large payloads, cumulative ACKs, priority queues, and DLQ after max retries.
- Retry policy uses exponential backoff with jitter: `t = min(cap, base*2^n) + jitter`.
- Prefer structured logs (`log.Infof`, `log.Errorf`) with contextual fields.

## Testing Instructions
- Current suite is empty; run `go test ./...` for sanity.
- Add targeted tests around transport semantics: replay after reconnect, deduplication, backoff timing, chunk reassembly, DLQ thresholds.
- Use fakes/mocks for network; avoid flaky integration tests until a stable harness exists.

## Code Style Guidelines
- Run `gofmt` on Go files; keep files ASCII unless required otherwise.
- Message contract implements `Kind()`, `ID()`, `Payload()`; use enums `KindPing`, `KindPong`, `KindData`, `KindACK`.
- Keep configuration in structs; avoid global mutable state beyond bootstrap wiring.
- Maintain package boundaries: `api/` is dependency-free, `transport/` hides libp2p details.

## Build and Deployment
- Build binary: `go build ./...`
- Run locally: `go run . -mode=leaf`
- Lifecycle: ensure `Start`/`Stop` are safe to call per Kratos expectations and are cancellation-aware.
- Future packaging: prefer static builds; CGO not expected.

## Security Considerations
- Plan for end-to-end encryption; relays/POP nodes must not decrypt payloads.
- Sign messages and validate signatures to prevent tampering.
- Rate-limit forwarding and validate inputs to reduce amplification or abuse.

## Debugging and Troubleshooting
- Enable verbose logging via Kratos logger; include IDs and peer info on errors.
- If replay stalls, check WAL/outbox storage bounds and retry counters.
- Verify dedup caches survive restarts and are bounded to avoid unbounded memory use.

## Pull Request Guidelines
- Before submitting: `gofmt`, `go test ./...`.
- Keep changes scoped; document transport behavior changes in commit messages.
- Update this file or [api/design/design.md](api/design/design.md) when altering reliability semantics.

## File Map
- Entry point: [main.go](main.go)
- Transport API: [api/transport/transport.go](api/transport/transport.go)
- Transport implementation skeleton: [transport/transport.go](transport/transport.go)
- Design details: [api/design/design.md](api/design/design.md)
