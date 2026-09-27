# AGENTS.md

Guidance for AI coding agents working in this repository. TelePortal is a high-performance, bi-directional audio bridge designed to connect VoIP systems (via SIP/RTP) and WebRTC with real-time AI agents (via WebSockets). It enables low-latency, full-duplex communication between telephony networks, browser clients, and modern AI audio models.

This is a Go **1.27.1+** project focused on concurrent, high-performance, low-latency audio processing. The rules below distill *Concurrency in Go* (Cox-Buday), *Learn Concurrent Programming with Go* (Cutajar), *100 Go Mistakes* (Harsanyi), *Efficient Go* (Plotka), *The Go Programming Language* (Donovan & Kernighan), *Go in Practice* (Butcher et al.), and *Distributed Services with Go* (Jeffery). Follow them unless a task explicitly says otherwise.

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

Always look up APIs with `go doc`, rather than guessing from memory or browsing the web. The module cache is the source of truth for the exact pinned versions in `go.mod`, and `go doc` reads it directly:

```sh
go doc github.com/labstack/echo/v4              # Package overview and exported identifiers
go doc github.com/labstack/echo/v4.Context       # One type, with all its methods
go doc github.com/labstack/echo/v4.Context.JSON  # One method signature and doc comment
go doc -all github.com/labstack/echo/v4/middleware | less # Everything, including unexported examples
go doc -src net/http.Server.Shutdown             # Show the actual source when the doc comment is not enough
go doc github.com/gorilla/websocket.Conn        # WebSocket connection methods
go doc github.com/emiago/sipgo.Client           # SIP client types
go doc github.com/pion/webrtc/v4.PeerConnection # WebRTC peer connection API
go doc golang.org/x/sync/errgroup                # Works for stdlib, x/ packages, and dependencies in go.mod
go doc -ex golang.org/x/sync/errgroup            # List the package runnable examples (Go 1.27+)
go doc golang.org/x/sync/errgroup.ExampleGroup   # Print one example source: the fastest way to see correct usage
go doc go.uber.org/zap.Logger                   # Zap structured logger documentation
```

- Before calling any function from an imported package (third-party or stdlib) that you have not already used in this file, run `go doc` on it and read the signature and doc comment. API surfaces change between versions; the version in `go.sum` is what compiles, not what a training set remembers.
- Before adding a new dependency, `go doc` the package after `go get` to confirm it actually exposes what you need; if it does not, remove it (`go mod tidy`) rather than working around it.
- When doc comments mention concurrency safety ("safe for concurrent use", "must not be called after Close", "the caller must Close the returned Body"), treat that sentence as a strict invariant and mirror it in your own doc comment if you wrap it.
- If `go doc` says "no required module provides package", the dependency is not in `go.mod`; add it properly with `go get`, rather than papering over it with a guessed import path.
- Prefer `go doc` over opening files in `$(go env GOMODCACHE)` directly; it is shorter, version-correct, and shows exactly the exported surface you are allowed to depend on.

---

## 1. Concurrency: Design Rules (Cox-Buday, Cutajar, Donovan & Kernighan)

Concurrency is a property of code structure; parallelism is an execution detail of the runtime. Design for correctness first, measure before assuming speed.

- **Share memory by communicating:** Default to channels to transfer ownership of data between goroutines. Use a mutex only to guard a small internal critical section (a struct's own fields, a cache, a counter). Decision rule: transferring ownership or coordinating stages -> channel; protecting internal state -> `sync.Mutex` or `sync.RWMutex`.
- **Every goroutine must have a known exit:** Before writing `go f()`, answer: *when does this goroutine stop, and who stops it?* Start it only once you can answer. Goroutines that block forever on a channel or `select` are memory leaks that pin their stacks and any captured data. Prevent goroutine leaks at all costs.
- **The Parent Owns the Lifecycle:** Whoever starts a goroutine is responsible for stopping it and waiting for it (`sync.WaitGroup`, `errgroup`, or a `done` channel). Fire-and-forget goroutines belong only in top-level server runners with graceful shutdown.
- **Channel Ownership:** The goroutine that writes to a channel owns it: it creates it, writes to it, and is the only one that closes it. Consumers only read and range. A channel is closed exactly once, always from the sending side, and written to only while open (writing to a closed channel panics). Never close a channel from the receiver side.
- **Return Directional Channels:** Return receive-only (`<-chan T`) or send-only (`chan<- T`) types at API boundaries so the compiler enforces ownership.
- **Prefer Unbuffered Channels:** Use unbuffered channels unless you have a measured reason for a buffer. Buffered channels hide backpressure and turn deadlocks into delayed deadlocks. Buffered channels are justified when: you know the number of writes up front (such as fan-in of N results), or you are intentionally decoupling a bursty producer with a bounded queue.
- **Context Propagation:** Use `context.Context` for exactly three things: cancellation, deadlines, and request-scoped values. It is always the first parameter (`ctx context.Context`). Every blocking operation (`select`, network call, DB call, channel send) must also listen on `ctx.Done()`. Keep a `Context` flowing through parameters, out of struct fields. Always pass a real context; use `context.TODO()` if genuinely unknown. Call every `cancel` returned by `WithCancel`, `WithTimeout`, or `WithDeadline` (`defer cancel()`), otherwise the context and its timer leak.
- **No Unbounded Concurrency:** Always use semaphores or worker pools to limit concurrency. Never allow external input (such as incoming network packets or HTTP requests) to spawn unlimited goroutines.
- **Canonical Patterns:** Reach for established patterns by name instead of inventing ad hoc ones:
  - *Pipeline*: stages connected by channels; each stage owns its output channel and closes it when its input is drained.
  - *Fan-out / fan-in*: multiple workers reading from one channel, results merged into one channel by a goroutine that closes it after `wg.Wait()`.
  - *Worker pool*: fixed number of goroutines (bounded by CPU or downstream capacity, not one per item) pulling from a jobs channel.
  - *or-done channel*: wrap reads so a stage exits on `ctx.Done()` without checking in every `select`.
  - *Bounded semaphore*: `make(chan struct{}, n)` or `golang.org/x/sync/semaphore` to cap concurrent access to a resource.
  - *ErrGroup*: `golang.org/x/sync/errgroup` (`g, ctx := errgroup.WithContext(ctx)`) for coordinating concurrent operations and stopping on first error. Prefer it over a hand-rolled WaitGroup plus error channel. Use `g.SetLimit(n)` to bound concurrency.
  - *Heartbeats* for long-running workers that must prove liveness; *rate limiting* with `golang.org/x/time/rate`.
- **Select Safety:** Multiple ready cases in a `select` statement are chosen pseudo-randomly; write cases so any execution order is correct. A `default` branch makes it non-blocking; in a loop, always pair a `default` branch with a wait to prevent busy-spinning. Reading from a `nil` channel blocks forever. This is a feature: set a channel variable to `nil` to disable a case after it is drained.
- **`time.After` no longer leaks:** Since Go 1.23 an unreferenced timer is garbage-collected before it fires, and Go 1.27 removed the `asynctimerchan` opt-out, so this behavior is unconditional. Using `time.After` in a `select` is fine. Reach for `time.NewTimer` plus `Reset` only to avoid a per-iteration allocation in a measured hot loop, and `context.WithTimeout` when the deadline should propagate.
- **`sync.WaitGroup`:** Call `Add` before starting the goroutine (in the parent, not inside it), and `defer wg.Done()` as the first line of the goroutine. Always pass `*sync.WaitGroup` as a pointer. Prefer `wg.Go(func())` (Go 1.25+) where it fits.
- **Mutex Discipline:** Keep critical sections minimal. Release locks before performing I/O, channel operations, or calls into unknown code. Use `defer mu.Unlock()` immediately after `mu.Lock()` unless the function is hot and the lock scope is tiny. Always pass a struct containing a mutex by pointer, keeping it uncopied (`go vet` copylocks catches this). Use `RWMutex` only when reads vastly outnumber writes and contention is measured; `RWMutex` is slower than `Mutex` under low contention. Always acquire multiple locks in a consistent global order to prevent deadlocks.
- **`sync.Once` and `sync.Pool`:** Use `sync.Once` for lazy initialization. Use `sync.Pool` only for amortizing short-lived allocations (the pool may be emptied on every GC; store only state you can afford to lose).
- **Atomics (`sync/atomic`):** Use atomics only for individual counters or flags, preferring typed primitives (`atomic.Int64`, `atomic.Bool`, `atomic.Pointer[T]`) over raw operations on plain fields. Multi-field invariants require a mutex; atomics cover only a single value.
- **Data Races:** The Go memory model is absolute. Without synchronization establishing happens-before (channel operation, mutex, `Once`, `WaitGroup`, or atomic), concurrent reads and writes are data races, full stop. Test under `-race` on every run.
- **Loop Variable Capture:** Go 1.22+ gives each loop iteration its own variable, so `go func() { use(v) }()` is safe, but still pass values explicitly as arguments when it reads more clearly and for anything shared across iterations.
- **Clean Up Every Goroutine a Test Starts:** Use `t.Context()`; every test that starts goroutines waits for them to finish before returning.
- **Deadlock Awareness:** The runtime only reports that all goroutines are asleep if every goroutine in the process is blocked. A partial deadlock (two goroutines waiting on each other while the rest of the server runs) is silent. Use timeouts in tests and `ctx.Done()` cases in production selects so deadlocks fail loudly.

## 2. Concurrency: API and Error Handling (Harsanyi, Cox-Buday)

- Functions that launch background goroutines must state so in their doc comments and specify how the goroutines are terminated.
- A goroutine errors must reach an owner: return them through an error channel via a result struct (`type result struct { val T; err error }`), collect them with `errgroup`, or log them at the top-level owner. Swallowing errors inside a goroutine is a bug.
- Every long-running worker goroutine handling untrusted network input should include a deferred `recover` handler that logs panics and safely tears down resources. An unrecovered panic in any goroutine kills the whole process.
- Propagate cancellation: when your function context is done, clean up and return `ctx.Err()` (or an error wrapping it) promptly.
- Pass required parameters explicitly; reserve `context.Value` for cross-cutting request metadata (trace IDs, auth principal) only, with unexported key types.
- Wrap errors with context using `fmt.Errorf("doing action: %w", err)` to create informative error chains.
- Distinguish between typed errors using `errors.Is` and `errors.As`.

## 3. Performance: Zero-Allocation Pipeline & Efficiency (Plotka, Harsanyi)

TelePortal processes real-time audio packets (20ms frames, 50 packets per second per call). At 1,000+ concurrent calls, memory allocations in the packet loop cause severe GC pauses that introduce audio jitter and packet loss.

- **Optimization Methodology:** Correct, then clear, then fast, and only fast where measured. Optimize only what a measurement has shown to matter. The workflow is: write the straightforward version -> write a benchmark -> profile -> optimize the hot spot -> re-benchmark and verify with numbers. State efficiency goals explicitly (latency, throughput, RSS) before optimizing, and stop when met.
- **Benchmarking Standards:** Benchmark properly using `b.ReportAllocs()` and `b.ResetTimer()` after setup. Use `b.Loop()` (Go 1.24+) to avoid the compiler optimizing the benchmark loop body away. Benchmarks assigning results to a package-level sink (`var sink T`) are the fallback. Run benchmarks on a quiet machine.
- **Profiling Tools:** Profile instead of guessing. Capture goroutine, block, mutex, cpu, and heap profiles under load.
  - The `goroutineleak` profile (Go 1.27+, `/debug/pprof/goroutineleak`) lists goroutines blocked on a primitive the GC proved unreachable, which means they can never wake. In a clean system, this count must be zero. It complements the design rules.
  - Inspect `alloc_space` and `alloc_objects` for GC pressure, not just `inuse`.
  - Use `go tool trace` to understand scheduler behavior, goroutine blocking, and GC pauses.
  - Use `runtime/metrics` for production observability.
- **Zero-Allocation Pipeline (TelePortal Audio Rules):**
  - RTP network packets must be read directly into shared buffer pools. Use `pkg/audio/pool.go` for byte and int slices.
  - Zero-Allocation Pools (`sync.Pool`): When pooling slices (such as `[]byte` or `[]int`), store the slice value directly (`sync.Pool.Put(b)`) rather than pointers (`*[]byte`). While passing a slice value triggers an interface boxing allocation (`SA6002`), empirical load test profiling confirms that this brief, lightweight allocation is significantly faster than the GC overhead of dereferencing pointers inside the pool under heavy, concurrent network I/O. Suppress `SA6002` where appropriate.
  - Slice Reuse: Always restore slices to capacity (`b = b[:cap(b)]`) before returning them to the pool. Check for nil or zero-capacity slices before slicing.
- **Allocation Reduction:** Check escape analysis with `go build -gcflags=-m`. Preallocate slices and maps with known capacity (`make([]T, 0, capacity)`, `make(map[K]V, capacity)`). Reuse buffers (`bytes.Buffer.Reset`, `sync.Pool`). Avoid `fmt.Sprintf` in hot paths (`strconv.Append*`, `strings.Builder`). Pass small structs by value and large ones by pointer. Avoid interface boxing and closures in hot loops. Use `[]byte` over `string` conversions where data flows through I/O.
- **Slice Gotchas:** `append` on a sub-slice can overwrite the parent backing array; use the full slice expression `s[low:high:max]` or `slices.Clone` when handing slices out. Slicing a huge array or slice for a tiny piece keeps the whole backing array alive in memory; copy it instead. Note that `nil` slices and empty slices behave identically for `append` and `range`, but serialize differently in JSON (`null` versus `[]`).
- **Map Gotchas:** Maps never shrink after deletes. If a map grows huge and then empties, recreate it. Map iteration order is randomized; write code that is correct for any order, sorting keys when output must be deterministic. Reading a map concurrently from multiple goroutines is safe; any concurrent write requires a mutex, or use `sync.Map` only for append-mostly or disjoint-key cases.
- **Strings:** Use `strings.Builder` for concatenation in loops; string `+=` is quadratic. Ranging over a string yields runes, whereas indexing yields raw bytes. Substrings share memory with the parent string; use `strings.Clone` to prevent small substrings of large inputs from keeping the parent alive in memory.
- **Data & Struct Layout:** Arrange struct fields by size (largest to smallest, pointers first) to minimize memory padding and reduce struct footprint. Verify with `make fieldalignment`. Group hot fields together. Avoid false sharing in per-CPU counters (pad to 64-byte cache lines or use per-goroutine accumulation and merge). Prefer slices of structs over slices of pointers for cache locality when elements are small.
- **Pointers vs Values:** Use pointers for large structs or shared mutable state. Use values for small structs to keep data on the stack and avoid GC overhead. Avoid pointers to slices or maps, as they are already reference headers.
- **GC Management:** Tune only with evidence using `GOGC` and `GOMEMLIMIT` (soft limit; set it in containers to prevent OOM kills). Reducing allocation rate beats tuning knobs.
- **Buffered I/O & Network Deadlines:** Always buffer network reads and writes (`bufio.Reader/Writer`). Always set explicit deadlines on network sockets (`SetReadDeadline`, `SetWriteDeadline`). Reuse HTTP client transports across requests. Always `defer resp.Body.Close()`; in Go 1.27 `Close` automatically drains a small unread remainder so the connection is reused, so an explicit `io.Copy(io.Discard, resp.Body)` is only needed when deliberately abandoning a large response body. Prefer `io.Copy` over manual read loops.
- **Compiler Optimizations:** Function inlining is limited (small bodies, simple logic). Bounds checks can be hoisted with `_ = s[n-1]`. Defer is low overhead now but not free in innermost packet loops. Interface method calls block inlining and devirtualization.

## 4. Common Mistakes to Avoid (Harsanyi, "100 Go Mistakes")

- **Shadowing Errors:** Beware of accidental variable shadowing with `:=` inside an `if` or `for` block so the outer `err` is never checked. `make qa` (vet and staticcheck) catches most of these.
- **Error Comparison & Wrapping:** Never compare errors with `==` instead of `errors.Is`. Never use direct type assertion instead of `errors.As`. Wrap errors with `%w` so callers can inspect underlying causes; use `%v` only when deliberately concealing internal error details from public APIs.
- **Typed Nil Error Return:** Never return a typed nil pointer as an `error` interface value (a typed nil pointer inside an interface is not nil!). Return a literal `nil`.
- **Ignoring Deferred Errors:** Do not ignore the return value of deferred calls that can fail (`f.Close()`, `tx.Rollback()`). Capture and combine them with `errors.Join` when relevant, or wrap in a defensive defer function that logs errors.
- **Defer in Loops:** Avoid using `defer` inside a loop; deferred calls execute at function return, not at loop iteration end. Extract the loop body into a separate function.
- **Range Value Modification:** The `range` loop evaluates the expression once and copies each element. Modifying the loop iteration variable does not modify the underlying slice element. Use index indexing or a slice of pointers when in-place modification is required.
- **Time Measurements:** Do not use `time.Now()` for measuring elapsed time across clock adjustments; use `time.Since` which leverages the monotonic clock. Do not compare `time.Time` instances with `==`; use `t.Equal(other)`.
- **Database and Stream Resource Leaks:** Always ensure `rows.Close()` and check `rows.Err()` when querying databases. Use prepared statements in hot query loops and set connection pool limits (`SetMaxOpenConns`, `SetConnMaxLifetime`).
- **Misusing `init()`:** Avoid `init()` functions due to hidden side effects, initialization order dependencies, and untestability. Prefer explicit constructors.
- **Interface Pollution:** Do not define interfaces in producer packages. Define interfaces in consumer packages where they are used. Keep interfaces focused (1 to 3 methods), and introduce them only when multiple concrete implementations exist or a test seam is required. Accept interfaces, return concrete structs.
- **Anti-Patterns:** Avoid getters and setters, generic catch-all "utils" packages, and deeply nested package hierarchies.
- **Misusing Generics:** Use generics for data structures and algorithms that operate across multiple types. When only one type is involved, write a simple concrete function.
- **Floating Point Comparisons:** Compare floating point numbers with an epsilon tolerance rather than direct `==`. Minor units or integer representations should be used for exact values such as currency or fixed precision audio samples.
- **Breaking Out of Loops:** Calling `break` inside a `select` block that sits inside a `for` loop only breaks out of the `select`. Use a labeled `break` or a `return` to exit the outer loop.
- **JSON Serialization Pitfalls:** Unexported struct fields are silently ignored by `encoding/json`. `time.Time` round-trips lose their monotonic clock reading. Untyped numbers decode into `any` as `float64`. Embedded structs implementing `MarshalJSON` take over the entire serialization of the outer struct.
- **HTTP & WebSocket Handler Lifecycles:** Handlers must finish all writes and complete all use of the request context (including within any goroutines spawned by the request) before returning. Always check `r.Context()` for cancellation during long operations.
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

- **Nil Slices vs Empty Slices:** Prefer nil slices (`var s []byte`) for unallocated initial states to reduce heap allocations. Note that `nil` slices and empty slices serialize differently in JSON (`null` vs `[]`).

## 5. Production Services & Cloud Native Architecture (Jeffery, Butcher et al.)

- **Graceful Shutdown:** Implement signal handling (`SIGINT`, `SIGTERM`) using `signal.NotifyContext`. Stop accepting new calls and requests, finish in-flight work before exiting, close media sockets, and notify Redis or connected WebSockets. Every long-running component must expose an idempotent `Close` or `Shutdown` method.
- **Configuration:** Strictly separate configuration from code. Use environment variables with sensible defaults or startup flags. Validate all configuration keys on startup and fail fast if invalid. Pass configuration down explicitly through constructors, never through package-level globals.
- **Structured Logging (`go.uber.org/zap` exclusively):**
  - Create one `*zap.Logger` in `main` (`zap.NewProduction()` by default, `zap.NewDevelopment()` behind a `-dev` flag) and pass it down explicitly. Package-level globals, `zap.L()`, `zap.S()`, and `zap.ReplaceGlobals` are forbidden.
  - Use the typed logger API (`log.Info("msg", zap.String("key", val), zap.Error(err))`) on concurrent and hot paths; it is allocation-free. Sugared logger methods (`Infow`, `Infof`) are allowed only in cold startup or shutdown code. Handlers, workers, and loops always stay on the typed API.
  - Keep log messages short, static, and lowercase phrases. Never use `fmt.Sprintf` inside log message strings. Put all dynamic data into typed fields using stable, `snake_case` keys (`call_id`, `request_id`, `duration_ms`). Field keys are a contract with the log pipeline.
  - Use `zap.Error(err)` for errors (key `error`), and typed field constructors (`zap.Duration`, `zap.Int64`, `zap.String`) rather than `zap.Any` (which uses reflection and allocates on hot paths).
  - Derive child loggers with `logger.With(...)` for request or session IDs and component names (`logger.Named("sip")`), attaching fields once rather than on every invocation.
  - Log at the edge (handlers, workers, `main`), and return errors from the middle. Every error must be handled exactly once: either logged or returned. Log levels: `Debug` for diagnostics, `Info` for state transitions, `Warn` for recoverable degradation, `Error` when operator intervention is required, and `Fatal`/`Panic` only during startup before the service is listening.
  - Place `defer logger.Sync()` in `main`; ignoring its error is acceptable there and only there (standard output and error descriptors can return `EINVAL` on sync).
  - For HTTP request logging with Echo, configure `middleware.RequestLoggerWithConfig` feeding the zap logger via `LogValuesFunc` rather than using the default `middleware.Logger()`.
  - In unit tests, use `zap.NewNop()` by default, `zaptest.NewLogger(t)` when log output on failure is helpful, or `zap/zaptest/observer` to assert on emitted entries.
- **Observability & Metrics:** Integrate Prometheus metrics (`internal/platform/metrics`) into all signaling and audio processing paths:
  - Track active calls, SIP requests, WebRTC sessions, packet counts, jitter buffer drops, and transcoding durations.
  - Expose health check and metrics endpoints (`/metrics`, `/debug/pprof`) on internal administration ports, kept strictly separate from public media or signaling ports.
  - HTTP metrics follow OpenTelemetry semantic conventions, labelled by route pattern (never by raw URI containing IDs). Enable Go runtime metrics from the Prometheus Go collector with all `runtime/metrics` enabled.
- **Resiliency:** Network timeouts on all calls. Retries with jitter and exponential backoff for distributed cache and signaling operations (idempotent operations only). Circuit breakers to prevent cascading failures when external AI agents disconnect.

## General Style & AI Agent Operations

- **Early Return:** Handle error branches and edge cases first with an immediate return (or `continue` in loops). Keep the happy path at the lowest indentation level without redundant `else` blocks.
- **Levels of Abstraction:** Low-level network mechanics (raw RTP parsing, SDP munging, UDP socket reads) stay in dedicated packages. High-level orchestrators (`app`, `call`, `api`) operate on clean domain concepts, leaving raw bytes and wire formats to dedicated types.
- **Standard Library First:** Reach for the standard library first; use third-party dependencies only with a stated technical reason.
- **Errors are Values:** Return errors, wrap them with informative context (`fmt.Errorf("reading packet: %w", err)`), and handle them once.
- **File Deletion Rule:** NEVER use `rm` or `rm -rf` to delete files. You must ALWAYS use `trash` (from `trash-cli`) to safely move files to the trash bin instead.
- **Communication Style:** When generating comments, documentation, and git commit messages, always sound like a human engineer. Never use latin abbreviations like eg or emdash punctuation.

## Testing

- **Native Go Only:** Use ONLY the standard library `testing` package and packages from this module (`testing`, `net/http/httptest`, `testing/synctest`, `reflect.DeepEqual`/`slices.Equal`/`maps.Equal`, `errors.Is`/`errors.As`, `bytes`, `strings`, `io`, `os`). Never introduce third-party test or assertion frameworks such as `testify`, `gomock`, `gomega`, `go-cmp`, or `check.v1`. Two exceptions: constructing the component under test with its own dependencies (such as `echo.New()` or `zap.NewNop()`), and writing minimal mock structs satisfying required interfaces directly inside `_test.go` files.
- **Table-Driven Tests:** Prefer table-driven test cases with `t.Run` for multi-case logic. Call `t.Helper()` in test helper functions.
- **Test Context and Lifecycle:** Use `t.Context()` (Go 1.24+) for cancellation and timeout contexts tied to test lifecycle. Use `t.TempDir()` for temporary file operations. Use `t.Cleanup(func() { ... })` for managing test resources instead of `defer` to ensure cleanup runs even if a test panics or fails early.
- **Parallel Execution:** Call `t.Parallel()` at the beginning of independent unit tests and sub-tests that do not mutate shared package state.
- **Assertions:** Use standard `if got != want { t.Fatalf("got %v, want %v", got, want) }` or `t.Errorf(...)` assertions. Use `reflect.DeepEqual`, `slices.Equal`, or `maps.Equal` for compound types.
- **Event-Driven Synchronization (No Arbitrary Sleeps):** Never use `time.Sleep` to wait for goroutines in tests. Tests that pass because the machine was fast enough are flaky in CI. Replace timing-based assertions with event-driven synchronization:
  - Synchronize on events, not wall clock time: unbuffered channels, `sync.WaitGroup`, `errgroup.Wait`, `<-done`, or `t.Context()` cancellation. When a goroutine reaches a specific milestone, have it signal on a channel.
  - Test time-dependent logic (timeouts, tickers, retries with backoff, expiry) using `testing/synctest` (`synctest.Test(t, func(t *testing.T) { ... })`, followed by `synctest.Wait()`), where fake time advances deterministically and instantly.
  - Inside a `synctest` bubble, use `synctest.Sleep(d)` (Go 1.27+: combines `time.Sleep` and `synctest.Wait` in one call) to advance the synthetic clock and settle goroutines.
  - For HTTP testing under `synctest`, use `httptest.NewTestServer` (Go 1.27+), which serves over an in-memory network without real network ports or wall clock involvement. Use `httptest.NewServer` for tests outside synctest bubbles.
  - If `synctest` does not fit a specific scenario, inject the clock: pass a `now func() time.Time` or a minimal `Clock` interface so tests advance time explicitly.
  - When waiting on external resources (such as server startup or file creation), wait on explicit event channels (listener ready channel, `net.Listen` before `Serve`, or `httptest.NewServer` which is ready upon return) rather than sleeping.
  - Use `t.Context()` combined with `context.WithTimeout` (measured in seconds) strictly as a hang guard against deadlocks, failing with an informative timeout error.
- **Clean Goroutine Teardown in Tests:** Every goroutine a test starts must be terminated and waited on before the test returns.
- **Self-Contained and Deterministic:** Tests must be independent of execution order, map iteration order, random seeds (use `rand.New(rand.NewSource(1))` if seeded pseudo-randomness is required), environment variables, current working directory, external network access, and local timezone or locale.
- **Mocking:** Manually implement minimal mock structs satisfying required interfaces directly inside `_test.go` files. Avoid mock generation tools.
- **Golden Files:** For large expected audio payloads, SDP blocks, or JSON responses, store golden fixtures in a `testdata/` subdirectory.

## Standing Rules

- Run `make qa && make test` after every modification.
- Start a goroutine only when you can state its exit condition.
- Add mutexes, channels, buffer pools, or optimizations only with a clear concurrency invariant or performance measurement.
- Always explain why an error is ignored when using `_`.
- Let `go mod tidy` manage `go.sum`; keep binaries, vendor directories, and IDE files out of commits.

## Commits & Pull Requests

- Keep commits atomic and focused with clear imperative subjects (`feat(sip): add support for re-INVITE handling`).
- Concurrency changes must describe the invariants protected and the goroutine termination path. Performance changes must include before and after benchmark measurements.
- Mention any new module dependency and the architectural rationale for adding it.
