# AGENTS.md

Guidance for AI coding agents working in this repository. TelePortal is a high-performance, bi-directional audio bridge designed to connect VoIP systems (via SIP/RTP) and WebRTC with real-time AI agents (via WebSockets). It enables low-latency, full-duplex communication between telephony networks, browser clients, and modern AI audio models.

The rules below distill *Concurrency in Go* (Cox-Buday), *Learn Concurrent Programming with Go* (Cutajar), *100 Go Mistakes* (Harsanyi), *Efficient Go* (Plotka), *The Go Programming Language* (Donovan & Kernighan), *Go in Practice* (Butcher et al.), and *Distributed Services with Go* (Jeffery). Follow them unless a task explicitly says otherwise.

## Project

- **Language & Runtime:** Go 1.27.1+ with tool dependencies pinned directly in `go.mod`.
- **Primary Stack:**
  - **SIP Protocol:** `github.com/emiago/sipgo`
  - **SDP Parsing:** `github.com/pion/sdp/v3`
  - **WebRTC Stack:** `github.com/pion/webrtc/v4`
  - **HTTP & WebSocket:** `github.com/labstack/echo/v4` and `github.com/gorilla/websocket`
  - **Structured Logging:** `go.uber.org/zap` (typed fields only)
  - **Distributed State & Cache:** `github.com/redis/go-redis/v9` (orchestrator coordination)
  - **TUI & Load Testing:** `github.com/charmbracelet/bubbletea` and `github.com/charmbracelet/lipgloss`
  - **Metrics:** `github.com/prometheus/client_golang`

### Directory Layout & Architecture

- `cmd/`: Binary entrypoints only (no business logic, no cross-imports between commands).
  - `cmd/teleportal/`: Main core audio bridge service.
  - `cmd/loadtest/`: Standalone TUI-based load testing tool simulating 1,000+ concurrent calls across SIP and WebRTC.
- `internal/`: Private domain and infrastructure packages. Never imported by `pkg/` or external consumers.
  - `internal/app/`: Application orchestrator coordinating initialization and lifecycle for SIP, WebRTC, RTP, and API subsystems.
  - `internal/sip/`: SIP signaling state machine (`sipgo`), handling INVITE, ACK, BYE, CANCEL, and SDP negotiation.
  - `internal/webrtc/`: WebRTC signaling, peer connection lifecycle, and media track management.
  - `internal/rtp/`: UDP port allocation and RTP packet streaming (`internal/rtp/rtpdefs`).
  - `internal/call/`: Call manager tracking active calls via a high-performance `DualIndexedArray`.
  - `internal/api/`: HTTP endpoints (Echo) and WebSocket `AudioBridge` handling client connection distribution.
  - `internal/audio/`: Media processing engine:
    - Adaptive jitter buffering via `pion/jitterbuffer`.
    - RFC 4733 DTMF detection and injection (`internal/rtp/dtmf.go`).
    - Synchronized stereo WAV recording via `recorder.go` and zero-allocation `wav_fast.go`.
    - G.711 PCMU/PCMA to L16 PCM conversion and Opus/Ogg stream encoding.
  - `internal/platform/`: Low-level platform modules (`cache`, `config`, `logging`, `metrics`). Platform packages never depend on higher-level internal orchestration.
  - `internal/netutil/`: IP and network helper utilities.
- `pkg/`: Public, exportable packages. Must never import `internal/` or `cmd/`.
  - `pkg/api/`: Public API data contracts and WebSocket message envelopes.
  - `pkg/audio/`: Shared zero-allocation audio buffer pool (`sync.Pool` for `[]byte` and `[]int`).
  - `pkg/client/`: Production-grade client for AI agents to stream audio to and from TelePortal.
- `docs/`: SIP documentation, architecture diagrams, and load test reports.
- `grafana/`: Prometheus and Grafana provisioning configs and dashboards.
- `recordings/`: Local directory for call recordings when enabled.

### Make as the Only Interface

Build, test, QA, and tool workflows are orchestrated through `make`. Always use defined `make` targets instead of ad hoc shell commands. Tool dependencies (`golangci-lint`, `gofumpt`, `nilaway`, `arch-go`, `govulncheck`, `fieldalignment`) are pinned in `go.mod` via Go `tool` directives and run directly via `go tool`.

## Commands

```sh
make help           # List available targets
make install-tools  # Download dependencies and build pinned tools into module cache
make build-all      # Build both the teleportal server and loadtest binary
make build          # Build the core teleportal binary -> build/teleportal
make build-loadtest # Build the standalone loadtest tool -> build/loadtest
make test           # Run all native Go tests
make run            # Build and run the service, loading variables from .env
make qa             # Full static-analysis gate: fix -> fmt -> vet -> lint -> nilaway -> arch-go -> loc -> fieldalignment
make fix            # go fix modernizers: rewrites locally; CI=1 fails on any pending fix
make fmt            # gofumpt: rewrites locally; CI=1 fails on any unformatted file
make vet            # go vet
make lint           # golangci-lint run
make nilaway        # NilAway nil-panic analysis (test files excluded)
make arch-go        # Architecture boundary and function complexity validation (arch-go.yml)
make loc            # scripts/loc.sh: fail on Go files exceeding 1500 lines of code
make fieldalignment # Check struct memory layout and padding
make vuln           # govulncheck against Go vulnerability DB (needs network; not part of qa)
make clean          # Remove build/ directory
```

- **After every code change run `make qa && make test`.** Run them after each edit, not just before declaring work done. If either fails, inspect and fix the output; claim success only when both pass. Also verify the relevant binary builds (`make build` or `make build-loadtest`).
- `make qa` execution order matters:
  1. `fix`: modernizers bounded by `go.mod` version rewrite code first.
  2. `fmt`: `gofumpt` formats the rewritten code so line numbers remain consistent.
  3. `vet`: checks suspicious constructs before linting.
  4. `lint`: runs `golangci-lint` with errcheck, gocritic, gosec, and staticcheck.
  5. `nilaway`: validates nil safety and potential nil panic flows across production packages.
  6. `arch-go`: enforces architectural layering and package boundary rules.
  7. `loc`: ensures no file exceeds the 1500 line threshold.
  8. `fieldalignment`: verifies optimal struct memory alignment.
- `make vuln` is kept separate from `qa` because it requires network access to the Go vulnerability database. Run it when modifying dependencies.
- After modifying imports: run `go mod tidy`, and commit `go.mod` and `go.sum` together.

## Using Imported Packages: `go doc` First

Look up APIs with `go doc` rather than guessing from memory or browsing the web. The module cache is the source of truth for the exact pinned versions in `go.mod`:

```sh
go doc github.com/labstack/echo/v4              # Package overview and exported identifiers
go doc github.com/labstack/echo/v4.Context       # Context interface and methods
go doc github.com/gorilla/websocket.Conn        # WebSocket connection methods
go doc github.com/emiago/sipgo.Client           # SIP client types
go doc github.com/pion/webrtc/v4.PeerConnection # WebRTC peer connection API
go doc golang.org/x/sync/errgroup                # Works for stdlib, x/ packages, and dependencies
go doc go.uber.org/zap.Logger                   # Zap structured logger documentation
```

- Before calling functions from an imported package that you have not already used in this file, run `go doc` to inspect parameter types and return signatures.
- When doc comments mention concurrency safety ("safe for concurrent use", "must not be called after Close"), treat that as a strict invariant.

---

## 1. Concurrency: Design Rules

Concurrency is a property of code structure; parallelism is an execution detail of the runtime. Design for correctness first, measure before assuming speed.

- **Never Start Without Stopping:** You must never spawn a goroutine without a defined mechanism for it to stop (context cancellation or channel signal). Prevent goroutine leaks at all costs.
- **The Parent Owns the Lifecycle:** Whoever starts a goroutine is responsible for stopping it and waiting for it (`sync.WaitGroup`, `errgroup`, or a `done` channel). Fire-and-forget goroutines belong only in top-level server runners with graceful shutdown.
- **Context Propagation:** Every long-running function or I/O operation must accept `context.Context` as its first argument. Respect cancellation immediately.
- **Channel Ownership:** Strictly enforce the rule: the goroutine that writes to a channel is responsible for closing it. Never close a channel from the receiver side.
- **No Unbounded Concurrency:** Always use semaphores or worker pools to limit concurrency. Never allow external input (such as incoming network packets or HTTP requests) to spawn unlimited goroutines.
- **Mutex vs Channels:** Use channels for orchestration and passing ownership of data. Use mutexes for state consistency, local struct protection, and caching. If critical sections are small, prefer `sync.Mutex` or `sync.RWMutex`.
- **Return Directional Channels:** Return receive-only (`<-chan T`) or send-only (`chan<- T`) types at API boundaries so the compiler enforces ownership.
- **Prefer Unbuffered Channels:** Use unbuffered channels unless you have a measured reason for a buffer. Buffered channels hide backpressure and turn deadlocks into delayed deadlocks.
- **Canonical Patterns:** Reach for established patterns by name:
  - *Pipeline*: stages connected by channels; each stage owns and closes its output channel.
  - *Fan-out / fan-in*: multiple workers reading from one channel, results merged into one channel.
  - *Worker pool*: fixed set of workers bounded by CPU or downstream capacity.
  - *ErrGroup*: `golang.org/x/sync/errgroup` for coordinating concurrent operations and stopping on first error.
  - *Bounded semaphore*: `make(chan struct{}, n)` or `golang.org/x/sync/semaphore` to cap concurrent access.
- **Select Safety:** Multiple ready cases in a `select` statement are chosen pseudo-randomly; write cases so any execution order is correct. In a loop, always pair a `default` branch with a wait to prevent busy-spinning. Reading from a nil channel blocks indefinitely; set channel variables to nil to disable cases once drained.
- **Mutex Discipline:** Keep critical sections minimal. Release locks before performing I/O, channel operations, or calls into unknown external code. Acquire multiple locks in a consistent global order to prevent deadlocks.
- **Atomics (`sync/atomic`):** Use atomics only for individual counters or flags, preferring typed primitives (`atomic.Int64`, `atomic.Bool`, `atomic.Pointer[T]`). Multi-field invariants require a mutex.
- **Data Races:** The Go memory model is absolute. Without synchronization establishing happens-before, concurrent reads and writes are data races. Test under `-race` on every run.

## 2. Concurrency: API and Error Handling

- Functions that launch background goroutines must document this in their comments and specify how the goroutines are terminated.
- Every goroutine's errors must be handled: return them through an error channel, collect them with `errgroup`, or log them at the top-level owner. Swallowing errors inside a goroutine is a bug.
- Every long-running worker goroutine handling untrusted network input should include a deferred `recover` handler that logs panics and safely tears down resources.
- Wrap errors with context using `fmt.Errorf("doing action: %w", err)` to create informative error chains.
- Distinguish between typed errors using `errors.Is` and `errors.As`.

## 3. Performance: Zero-Allocation Pipeline & Efficiency

TelePortal processes real-time audio packets (20ms frames, 50 packets per second per call). At 1,000+ concurrent calls, memory allocations in the packet loop cause severe GC pauses that introduce audio jitter and packet loss.

- **Zero-Allocation Pipeline:** RTP network packets must be read directly into shared buffer pools. Use `pkg/audio/pool.go` for byte and int slices.
- **Zero-Allocation Pools (`sync.Pool`):** When pooling slices (such as `[]byte` or `[]int`), store the slice value directly (`sync.Pool.Put(b)`) rather than pointers (`*[]byte`). While passing a slice value triggers an interface boxing allocation (`SA6002`), empirical load test profiling confirms that this brief, lightweight allocation is significantly faster than the GC overhead of dereferencing pointers inside the pool under heavy, concurrent network I/O. Suppress `SA6002` where appropriate.
- **Slice Reuse:** Always restore slices to capacity (`b = b[:cap(b)]`) before returning them to the pool. Check for nil or zero-capacity slices before slicing.
- **Pre-allocation:** Always specify capacity for slices and maps when the size is known or can be estimated (`make([]T, 0, capacity)`, `make(map[K]V, capacity)`).
- **Struct Layout:** Arrange struct fields by size (largest to smallest, pointers first) to minimize memory padding and reduce struct footprint. Verify with `make fieldalignment`.
- **Pointers vs Values:**
  - Use pointers for large structs or shared mutable state.
  - Use values for small structs to keep data on the stack and avoid GC overhead.
  - Avoid pointers to slices or maps, as they are already reference headers.
- **Avoid Allocations in Hot Loops:**
  - Avoid `fmt.Sprintf` in per-packet audio processing loops. Use `strconv.Append*` or pre-formatted byte slices.
  - Use `strings.Builder` or `bytes.Buffer` for string assembly.
  - Avoid interface boxing and closure allocations in hot paths.
- **Buffered I/O & Timeouts:** Always buffer network reads and writes (`bufio.Reader/Writer`). Always set explicit deadlines on network sockets (`SetReadDeadline`, `SetWriteDeadline`).

## 4. Common Mistakes to Avoid

- **Global State:** Avoid package-level variables for mutable state (`var db *sql.DB`). Pass dependencies explicitly through constructors.
- **Panic:** Never use `panic` in production code unless during startup bootstrapping or for unrecoverable configuration errors.
- **Chained Contexts in Structs:** Do not store `context.Context` inside struct fields. Pass context explicitly as the first parameter to methods.
- **Busy Waiting:** Never use `for { }` loops without a select statement or strict termination condition.
- **Naked Returns:** Avoid naked returns in non-trivial functions; name return values only for documentation or when modifying them in deferred handlers.
- **Defensive Defer:** When using `defer` to close resources, wrap the call to handle and log potential errors:

  ```go
  defer func() {
      if err := f.Close(); err != nil {
          log.Warn("Failed to close resource", zap.Error(err))
      }
  }()
  ```

- **Interface Pollution:** Do not define interfaces in producer packages. Define interfaces in consumer packages where they are used. Keep interfaces focused (1 to 3 methods). Accept interfaces, return concrete structs.
- **Shadowing Errors:** Beware of accidental variable shadowing with `:=` inside `if` or `for` blocks.
- **Nil Slices vs Empty Slices:** Prefer nil slices (`var s []byte`) for unallocated initial states to reduce heap allocations. Note that `nil` slices and empty slices serialize differently in JSON (`null` vs `[]`).

## 5. Production Services & Cloud Native Architecture

- **Graceful Shutdown:** Implement signal handling (`SIGINT`, `SIGTERM`) using `signal.NotifyContext`. Stop accepting new calls, finish in-flight requests before exiting, close media sockets, and notify Redis or connected WebSockets.
- **Configuration:** Strictly separate configuration from code. Use environment variables with sensible defaults. Validate all configuration keys on startup and fail fast if invalid.
- **Structured Logging:** Use `go.uber.org/zap` exclusively:
  - Create one `*zap.Logger` in `main` and pass it down explicitly.
  - Use the typed logger API (`log.Info("msg", zap.String("key", val), zap.Error(err))`) on concurrent and hot paths.
  - Keep log messages short, static, and lowercase phrases. Put all dynamic data into typed fields using `snake_case` keys.
  - Avoid package-level global loggers (`zap.L()` or `zap.S()`).
- **Observability & Metrics:** Integrate Prometheus metrics (`internal/platform/metrics`) into all signaling and audio processing paths:
  - Track active calls, SIP requests, WebRTC sessions, packet counts, jitter buffer drops, and transcoding durations.
  - Expose health check and metrics endpoints on internal administration ports.
- **Resiliency:**
  - Network timeouts on all calls.
  - Retries with jitter and exponential backoff for distributed cache and signaling operations.
  - Circuit breakers to prevent cascading failures when external AI agents disconnect.

## General Style & AI Agent Operations

- **Early Return:** Handle error branches and edge cases first with an immediate return. Keep the happy path at the lowest indentation level without redundant `else` blocks.
- **Levels of Abstraction:** Low-level network mechanics (raw RTP parsing, SDP munging, UDP socket reads) stay in dedicated packages. High-level orchestrators (`app`, `call`, `api`) operate on clean domain concepts.
- **File Deletion Rule:** NEVER use `rm` or `rm -rf` to delete files. You must ALWAYS use `trash` (from `trash-cli`) to safely move files to the trash bin instead.
- **Communication Style:** When generating comments, documentation, and git commit messages, always sound like a human engineer. Never use latin abbreviations like eg or emdash punctuation.

## Testing

- **Native Go Only:** Use ONLY the standard library `testing` package. Never introduce third-party test or assertion frameworks such as `testify`, `gomega`, `ginkgo`, or `check.v1`.
- **Table-Driven Tests:** Prefer table-driven test cases with `t.Run` for multi-case logic.
- **Parallel Execution:** Call `t.Parallel()` at the beginning of independent unit tests and sub-tests.
- **Assertions:** Use standard `if got != want { t.Errorf(...) }` or `t.Fatalf(...)` assertions. Use `reflect.DeepEqual`, `slices.Equal`, or `maps.Equal` for compound types.
- **Cleanup:** Use `t.Cleanup(func() { ... })` for managing test resources instead of `defer` to ensure cleanup runs even if a test panics or fails early.
- **Mocking:** Manually implement minimal mock structs satisfying required interfaces directly inside `_test.go` files. Avoid mock generation tools.
- **Golden Files:** For large expected audio payloads, SDP blocks, or JSON responses, store golden fixtures in a `testdata/` subdirectory.
- **Event-Driven Synchronization:** Never use arbitrary `time.Sleep` to wait for goroutines in tests. Synchronize using channels, `sync.WaitGroup`, or errgroups so tests execute deterministically and without flakiness.

## Standing Rules

- Run `make qa && make test` after every modification.
- Start a goroutine only when you can state its exit condition.
- Add mutexes, channels, or buffer pools only with a clear concurrency invariant or performance measurement.
- Always explain why an error is ignored when using `_`.

## Commits & Pull Requests

- Keep commits atomic and focused with clear imperative subjects (`feat(sip): add support for re-INVITE handling`).
- Concurrency changes must describe the invariants protected and the goroutine termination path.
- Mention any new module dependency and the architectural rationale for adding it.
