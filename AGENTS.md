# Basic requirements for Golang programs.

You are an expert AI programming assistant specializing in building APIs with Go, using the Gin library to build http api for services.

Always use the latest stable version of Go (1.26 or 1.27 or newer) and be familiar with RESTful API design principles, best practices, and Go idioms.

- You need to use the module name defined in go.mod as the base package name for this project.
- The programs you write should follow the style of the existing code to maintain overall consistency and simplicity in the codebase. You should adhere to Golang best practices when writing your programs.
- Before writing, modifying, fixing, or refactoring any Go code, you must load and use JetBrains' `modern-go-guidelines@goland-codex-marketplace` skill/plugin. Run its `list` command for the target Go file (or the Go version from `go.mod`), read the complete unfiltered output, and treat the returned version-specific guidance as authoritative. Run `explain` for every relevant guideline whose application is unclear; do not guess or silently fall back to outdated idioms if the skill/plugin is unavailable.
- Apply every relevant Modern Go Guidelines recommendation unless it would not compile, would change required behavior, or does not match the edited code. If you skip a relevant recommendation, document the concrete reason. After Go changes, run the configured formatters, the full `golangci-lint` suite, and relevant tests; do not hide new failures with broad exclusions or unexplained `//nolint` directives.
- First think step-by-step - describe your plan for the API structure, endpoints, and data flow in pseudocode, written out in great detail.
- Confirm the plan, then write code!
- Write correct, up-to-date, bug-free, fully functional, secure, and efficient Go code for APIs.
- Utilize the new Gin framework for routing
- Implement proper handling of different HTTP methods (GET, POST, PUT, DELETE, etc.)
- Implement proper error handling, including custom error types when beneficial.
- Do not return errors with `fmt.Errorf`. Define package-level sentinel errors with `errors.New`, return them directly when no underlying cause exists, and use `errors.Join(ErrSpecific, err)` when preserving an underlying error. This keeps errors testable and classifiable with `errors.Is` / `errors.As`.
- Use appropriate status codes and format JSON responses correctly.
- Implement input validation for API endpoints.
- Utilize Go's built-in concurrency features when beneficial for API performance.
- Follow RESTful API design principles and best practices.
- Include necessary imports, package declarations, and any required setup code.
- Implement proper logging using the `github.com/rs/zerolog/log` package.
- Consider implementing middleware for cross-cutting concerns (e.g., logging, authentication).
- Implement rate limiting and authentication/authorization when appropriate, using standard library features or simple custom implementations.
- Leave NO todos, placeholders, or missing pieces in the API implementation.
- Be concise in explanations, but provide brief comments for complex logic or Go-specific idioms.
- If unsure about a best practice or implementation detail, say so instead of guessing.
- Offer suggestions for testing the API endpoints using Go's testing package.
- The programs you write must follow cybersecurity standards and protect user privacy.
- Avoid using hardcoded values in the code; instead, use defined static variables or configuration files for settings whenever possible.
- Avoid duplicated implementation logic. When two or more code paths share the same lifecycle, validation, query preparation, file output, cleanup, cache, decoding, or result-building logic, extract a small helper function or focused type instead of copying the block. Keep the abstraction narrow and type-safe, pass package-level sentinel errors into helpers when each caller needs its own error classification, and verify duplicate-code changes with `golangci-lint run --enable-only=dupl` in addition to the full lint run.
- Use each tool's system default cache directories. Do not set `GOCACHE`, `GOMODCACHE`, `GOLANGCI_LINT_CACHE`, npm cache paths, or similar cache environment variables to paths inside this repository unless the user explicitly asks for an isolated per-project cache. Shared default caches allow Go, golangci-lint, npm, and other tools to reuse artifacts across projects and avoid duplicating large cache directories in every checkout.
- For more requirements, please refer to the file and directory descriptions as well as the architecture description of this project.

Always prioritize security, scalability, and maintainability in your API designs and implementations. Leverage the power and simplicity of Go's standard library to create efficient and idiomatic APIs.

# Golangci-lint code quality requirements

Treat `/.golangci.yaml` as the executable source of truth. These requirements explain the intended engineering behavior behind the enabled linters; they do not replace the configuration. Run `golangci-lint run ./...` against the entire repository after every Go change. Fix the design that caused an issue instead of weakening a threshold, adding a broad exclusion, or hiding it with an unexplained `//nolint` directive. Any unavoidable suppression must name the specific linter and explain the concrete compatibility or correctness constraint.

## Program complexity

The complexity linters are complementary. Passing one does not excuse a failure from another:

- `cyclop` limits cyclomatic complexity to the configured default maximum of 10 per function. Keep independent branches and decision paths bounded; extract cohesive operations or replace repeated branching with data-driven logic.
- `gocognit` reports cognitive complexity at 10 or above. Prefer guard clauses, early returns, focused helpers, and shallow control flow. Do not move the same nested logic into an anonymous closure merely to lower the enclosing function's score.
- `funlen` uses its defaults of 60 lines and 40 statements, ignoring comment-only lines. Split functions by responsibility and preserve one clear owner for transactions, cleanup, error classification, and resource lifetime.
- `nestif` reports nested-if complexity at its default threshold of 5. Flatten nested conditionals with validation guards and explicit terminal cases without changing required ordering or failure behavior.
- `maintidx` reports functions whose maintainability index is below its default threshold of 20. Improve naming, cohesion, duplication, control flow, and function size together; do not optimize only one metric.
- Complexity refactoring must preserve authorization, transaction boundaries, lock ordering, error identity, cleanup order, and side-effect ordering. Small helpers must represent meaningful units of behavior rather than fragmenting code solely to satisfy a score.

## Package names and exported-name stuttering

- Follow the `revive` naming rules and choose short, lowercase, singular package names that communicate one responsibility.
- Do not repeat the package name in an exported identifier when the qualified name would stutter. In package `user`, prefer `user.Service`, `user.Repository`, or `user.New` over `user.UserService`, `user.UserRepository`, or `user.NewUser` when the shorter name remains clear.
- Preserve conventional initialisms such as `ID`, `HTTP`, `URL`, and `API`. Do not use an import alias merely to conceal a stuttering or confusing package API.
- A repeated word is acceptable only when removing it would make the public API ambiguous or would break required compatibility. Document that constraint rather than renaming mechanically.

## Interface width

- `interfacebloat` allows at most 5 methods per interface in this repository, stricter than its upstream default of 10.
- Define interfaces at the consumer boundary and include only the behavior that consumer needs. Prefer multiple cohesive capability interfaces and compose them where a caller genuinely needs the combined contract.
- Do not add a method to a broad shared interface merely for one implementation. Do not create a large provider-side interface in advance of a real consumer, and do not split an atomic contract into artificial interfaces that weaken correctness.
- Prefer accepting the smallest useful interface and returning a concrete type unless an existing public contract requires otherwise. Keep mocks and test doubles aligned with the same narrow production contract.

## Error definition, wrapping, and matching

- `err113`, `errname`, `errorlint`, and `wrapcheck` jointly enforce stable error identity and correct propagation. Sentinel errors must be package-level values named with an `Err` prefix; custom error types must end in `Error`.
- Return a sentinel directly when there is no underlying cause. When a dependency or lower layer supplies the cause, classify it with `errors.Join(ErrOperation, err)` so callers can match both the package-level operation and the original cause. Do not return an external-package error unchanged from a package boundary.
- Do not construct returned errors with `fmt.Errorf`, build dynamic `errors.New` values at call sites, compare errors with `==`, or compare `err.Error()` strings. Use `errors.Is` for sentinel errors and wrapped causes.
- On Go 1.26 and newer, use `errors.AsType[T](err)` when matching a concrete error type. Include pointer-ness in `T`, for example:

```go
if pathErr, ok := errors.AsType[*os.PathError](err); ok {
	handle(pathErr)
}
```

- Do not introduce the legacy temporary-target form `var target *T; errors.As(err, &target)` in new or modified code. Use it only when required by code that must compile against a Go version older than 1.26, which this repository does not currently support.
- Add operation context through a classifiable sentinel rather than replacing the underlying cause with log text. Log an error at the layer that owns the failed operation, and avoid repeatedly logging the same propagated error at every layer.

## Struct field layout

- `fieldalignment` is enabled through `govet.enable-all`. Order fields to minimize padding and, where reported, reduce the pointer-scanned portion of frequently allocated structs. This is especially important for request state, cache entries, queue metadata, and other types with high instance counts or concurrency.
- Follow the analyzer's type-aware recommendation instead of relying only on visual size ordering; alignment, embedded fields, arrays, architecture, and pointer scanning all affect the result. Re-run the full lint suite after changing a struct.
- Before reordering fields, check whether declaration order is externally observable through positional composite literals, reflection, generated code, binary or C layout, unsafe operations, database mapping, or serialized field order. Prefer keyed literals and separate wire/storage DTOs from memory-optimized runtime structs.
- If compatibility makes reordering unsafe, preserve behavior and use only a narrow `//nolint:govet` with a concrete explanation of the layout contract. Do not disable `govet` or `fieldalignment` repository-wide.

## Modern Go requirements

- The `modernize` linter runs with all of its analyzers enabled, and `exptostd`, `intrange`, `copyloopvar`, `usestdlibvars`, `usetesting`, `perfsprint`, and `mirror` provide additional modernization checks. Apply their supported fixes when they preserve behavior.
- Before editing Go, run the Modern Go Guidelines skill as required above. Its Go-version-specific output is authoritative even when nearby code uses an older idiom.
- In addition to `errors.AsType`, prefer current standard-library forms such as integer ranges, iterator-based `maps`/`slices` helpers, typed atomics, `min`/`max`, `clear`, `strings.Cut` variants, `errors.Join`, `testing.T.Context`, and `testing.B.Loop` when the target Go version supports them and the pattern applies.
- Use `sync.WaitGroup.Go` inside a focused low-level concurrency abstraction, but application code in this repository must continue to start managed goroutines through `xutil.Go`, `xutil.WaitGroup`, or `xutil.ErrGroup` so panic recovery, naming, logging, cancellation, and ownership are preserved.
- For Go 1.27 and newer, use `encoding/json/v2` for new JSON code and `omitzero` for zero-valued numeric, boolean, struct, and time fields that should be omitted. Do not migrate established serialization behavior without explicit compatibility review and tests.
- Do not accept an automatic modernization that changes public APIs, serialized output, error identity, goroutine ownership, cancellation, ordering, or other required behavior. Apply the modern form manually or retain the existing form with a documented reason when compatibility requires it.

# Performance Optimization Methodology

Performance work must optimize the complete request and background-processing lifecycle, not merely the execution time of an individual SQL statement or Redis command. Reduce synchronous steps, network round trips, transaction duration, lock hold time, duplicated work, and unbounded background work while preserving business correctness.

## Performance analysis and planning

Before changing an implementation, describe the current and proposed data flow in pseudocode and identify the authoritative data source, consistency boundaries, failure semantics, and required ordering. Use the following workflow:

1. Draw the real synchronous request path and every asynchronous continuation, including MySQL, Redis, queue, filesystem, and third-party calls.
2. Establish a reproducible baseline with realistic data volume, key distribution, concurrency, and hot-key behavior. Record throughput and latency percentiles, especially P50, P95, and P99; averages alone are insufficient.
3. Measure these costs separately:
   - Network round trips to MySQL, Redis, and external services.
   - Server-side work such as SQL statement count, rows examined, Redis command count, Lua execution time, scans, sorts, allocations, and serialization.
   - Waiting and saturation such as row-lock time, transaction duration, connection-pool waits, goroutine growth, queue lag, hot keys, and Redis single-thread blocking.
4. Define invariants that the optimization must preserve, including idempotency, uniqueness, authorization, state transitions, ordering, and read-after-write requirements.
5. Set a measurable target and a rollback criterion before implementation. Compare before and after using the same workload and verify that work was removed rather than merely moved to an unobserved component.

Pipeline, Lua, batch SQL, caching, and concurrency solve different costs. A Redis Pipeline reduces network round trips but does not remove Redis command execution. Lua can provide atomicity but blocks Redis while running. Parallel calls can reduce wall-clock latency but increase downstream load. Always state which measured cost a proposed change addresses.

## Optimization order for request paths

Apply optimizations in this order unless measurements justify a different choice:

1. Remove duplicate reads, writes, serialization, and repeated configuration parsing.
2. Reuse data already loaded in a transaction or earlier stage by returning a focused result context instead of querying it again.
3. Batch independent reads and same-table writes with bounded request and payload sizes.
4. Shorten transactions and lock scopes; move preparation before the transaction and non-authoritative work after commit.
5. Replace read-then-lock-then-insert flows with atomic conditional writes when the operation only validates existing state and does not modify the validated rows.
6. Move retryable cache, index, notification, and projection work out of the authoritative transaction through a durable asynchronous boundary when eventual consistency is acceptable.
7. Move capacity maintenance and historical cleanup out of hot request paths into bounded periodic jobs.
8. Add bounded precomputation only when repeated requests can safely reuse the work; never create authoritative records, exposure counts, reservations, or cooldowns until the result is actually committed or returned.
9. Change consistency guarantees or business algorithms only after the preceding steps are exhausted and the tradeoff is explicitly approved.

Do not allow a best-effort follow-up operation, such as prefetching the next result, to turn an already committed authoritative operation into an apparent client failure. Return the committed outcome and report or retry the follow-up separately.

## MySQL, GORM, and transaction guidance

- Keep authoritative validation and writes in MySQL. Redis may perform bounded candidate selection or coarse filtering, but MySQL must enforce final business constraints when Redis data may be stale or incomplete.
- Prefer one set-based query over N per-record queries. Collect identifiers, deduplicate them, use bounded `IN` queries or joins, and batch inserts or updates when this reduces measured round trips and database work.
- Use conditional `INSERT`, `UPDATE`, or compare-and-swap predicates for atomic state transitions. Treat zero affected rows as a classified conflict or stale-candidate result rather than issuing a separate preflight query when possible.
- Do not remove locks that protect updates derived from existing values. For multi-row locking, determine and document a stable application-level lock order, acquire locks consistently, and do not assume an `IN` query's result ordering guarantees the physical lock acquisition order under every execution plan.
- Minimize work while locks are held. Perform validation and non-locking reads before the lock when safe, combine writes inside the critical section, and defer cache or external-system work until after commit.
- Return the data needed by the post-transaction stage from the transaction function. Do not re-read the same rows solely to reconstruct context already available in memory.
- Inspect generated SQL and query plans for performance-sensitive GORM code. Verify index selectivity, rows examined, result cardinality, and that batch size remains below database parameter and packet limits.
- Keep transactions free of Redis, queue publication without a transactional outbox, and third-party HTTP calls. Never treat independent MySQL and Redis writes as a distributed transaction.
- Give every transaction a context deadline and a single explicit commit-or-rollback owner. Register a rollback immediately after beginning a manual transaction so every error, panic, cancellation, and early return releases the connection and its locks; a rollback after a successful commit may be treated as a harmless no-op. Close query rows promptly and never leak a transaction-scoped GORM handle outside its lifecycle.
- Every long-running command that initializes GORM must participate in graceful shutdown. After stopping new HTTP requests and job claims and completing or cancelling in-flight work within a bounded deadline, obtain the underlying `*sql.DB` and call `Close()` so pooled database sessions and any remaining transactions are actively disconnected. Close the database only after components that use it have stopped.
- Do not rely on graceful cleanup as the only lock-release mechanism: cleanup cannot run after `SIGKILL`, a runtime crash, or host/network failure. Configure bounded query and transaction timeouts, connection lifetime and idle limits, TCP/server-side dead-session detection where operationally available, and monitoring for long-running transactions and lock waits so MySQL eventually detects failed clients and releases their sessions.

## Redis and cache guidance

- Use the `/cache` package for ordinary caching and `/redis` wrappers for specialized Redis operations; feature packages must continue to avoid direct `go-redis` usage.
- Use Pipeline for bounded independent operations when the goal is fewer network round trips. Use Lua only when related operations require Redis-side atomicity, such as idempotency, compare-and-swap, leases, or conditional compensation.
- Keep each Lua script small and predictably bounded. Never put an unbounded scan, a large collection of unrelated objects, or a whole worker batch into one script; split work into fixed-size batches and Pipeline independent per-object scripts when appropriate.
- Every cache or Redis structure must define capacity, TTL or retention, ownership of cleanup, cleanup frequency, and maximum cleanup batch size. "Clean it up later" is not a capacity plan.
- Cache only data whose cardinality and staleness are bounded. Provide TTL, capacity, active invalidation where required, empty-result behavior, and a reliable source-of-truth fallback. Do not cache complete historical relationships or metadata whose memory grows approximately with business history without a measured retention design.
- Distinguish correctness cleanup from capacity cleanup. Removal whose delay can affect authorization, eligibility, reservations, routing, or valid requests must occur synchronously or atomically. Expired indexes, excess low-value entries, and historical redundancy may be removed asynchronously in bounded batches.
- Avoid hot-key concentration and oversized values. Measure key cardinality, value size, command complexity, and contention under realistic skew, not only uniform benchmarks.

## Application-side Redis, cache, and queue usage

Choose the package by responsibility, not merely because all three currently use Redis:

1. Use `/cache` for ordinary derived-value caching where a source-of-truth fallback exists and temporary staleness or cache loss is acceptable.
2. Use `/redis` wrappers for specialized Redis data structures or atomic operations such as counters, bounded lists, sorted sets, leases, idempotency, and explicit expiration changes.
3. Use `/queue` for asynchronous work with at-least-once delivery, bounded retry, dead-letter handling, and independent worker ownership.

Application and feature packages must not import `github.com/redis/go-redis/v9`, `github.com/go-redis/cache/v9`, or Watermill Redis Stream implementations directly. Extend the focused wrapper in `/redis`, `/cache`, or `/queue` instead. Infrastructure code and package-level integration tests may use the underlying client when the wrapper itself is being implemented or verified.

### Redis package usage

- Initialize Redis once during command startup with `redis.Init(cmd.Context())` and fail startup if it returns an error. Reuse the shared client and pool for the complete process lifetime; never create or close a Redis client per request, job, tenant, or feature.
- Pass logical, unprefixed keys to `/redis` wrapper functions. Prefixing belongs inside the Redis package. Feature code must not call `redis.WithPrefix` before calling a wrapper because that can create double-prefixed keys and split one logical dataset across namespaces.
- Add a focused wrapper for every reusable operation rather than exposing command sequences to feature packages. The wrapper must validate keys, durations, batch sizes, score ranges, and output pointers before performing a network call, and must return package-level classifiable errors.
- Use `redis.SetExpire(ctx, key, ttl)` when an existing logical key needs a new relative expiration. Treat `redis.ErrKeyNotFound` as a distinct state with `errors.Is`; do not silently report success when the key no longer exists. Expiration must be positive and should normally include bounded jitter when many keys are created together.
- `redis.Increment` and `redis.Decrement` atomically update the counter and refresh its TTL. Use them only when sliding expiration is intended. Add a different wrapper when expiration must be set only on first creation; do not emulate that behavior with multiple application-side commands.
- `redis.RightPushWithLimitExpired` is the preferred bounded recent-history list operation because push, trim, and expiration are one atomic transaction. Keep batches within the wrapper limit and do not use it for unbounded history or an authoritative event log.
- Prefer a single wrapper Pipeline for bounded independent commands and a small Lua script for dependent atomic commands. Never call `Get`, compute a decision in Go, and then issue an unguarded write when another process can change the key between those commands.
- Give every operation a caller-derived context with a deadline. Handle errors with `errors.Is` and decide explicitly whether Redis failure is fail-open, fail-closed, or retryable for that business operation. Authorization, uniqueness, money, inventory, and other correctness decisions must not fail open merely because Redis is unavailable.
- Define keys using stable namespaces and identifiers, avoid embedding secrets or personal data, and keep values compact. Any new structure must document TTL or retention, maximum cardinality/value size, cleanup owner, hot-key risk, and expected command complexity.
- Only the root shutdown coordinator calls `redis.Close()`, after Queue, Cache users, HTTP handlers, and workers have stopped. Watermill receives the borrowed shared client and must not own or close the Redis pool.

### Cache package usage

- Initialize the cache wrapper after successful Redis initialization with `cache.InitCache()`. Ordinary application code should use `cache.PutString`, `cache.GetString`, and `cache.Delete` rather than the underlying cache client.
- `cache.PutString` requires a TTL of at least one second. Every call site must choose a TTL from the data's acceptable staleness window; do not use zero as an implicit default or treat cache entries as permanent storage.
- Handle `cache.ErrCacheMiss` with `errors.Is` as a normal control-flow state: load from the authoritative source, validate the result, populate the cache with a bounded TTL, and return the authoritative result. Do not log every miss as an application error.
- Distinguish a miss from a Redis/cache failure. A non-critical derived cache may fall back to MySQL on dependency failure, while security-sensitive negative decisions must follow an explicit fail-closed policy. Do not collapse dependency errors into a miss because that can create a database retry storm.
- Prevent cache stampedes for expensive or hot data with bounded request coalescing, staggered TTL jitter, refresh-ahead under durable ownership, or an equivalent measured strategy. Coalescing must respect caller cancellation and must not introduce an unbounded map of in-flight keys.
- Negative caching is allowed only for safe, bounded not-found results. Use a short explicit TTL, include tenant/authorization scope in the key, and never negative-cache transient dependency errors or permission decisions across users.
- Update or delete derived cache entries only after the authoritative MySQL transaction commits. If the cache update must reliably follow the commit, publish it through a transactional outbox. Cache failure after commit must not turn the committed business operation into an apparent client failure.
- Use delete-on-write by default when reconstructing a complete cached value is uncertain. When writing through, protect against stale concurrent writers with a version, compare-and-swap rule, or monotonic projection version.
- Do not cache mutable ORM objects and then share their pointers between requests. Cache serialized immutable values and rebuild request-local objects after decoding.
- `cache.Close()` only releases the cache wrapper; it does not own the Redis connection pool. The root shutdown coordinator closes Cache before Redis.

### Queue package usage

- Initialize Redis successfully before `queue.Init()`. Publish with `queue.Publish(ctx, job)` using the request, command, or durable worker context so cancellation and deadlines reach Redis. Do not launch a goroutine from an HTTP handler merely to call `Publish` after the request returns.
- Implement the complete `queue/job.Job` contract. `GetJobData` and `ParseJob` must return serialization errors; never replace malformed data with an empty or zero-value payload. Register every concrete job in `/queue/subscriber.go`.
- Keep payloads small, stable, and versionable. Prefer identifiers, idempotency keys, tenant scope, event version, and the minimum immutable facts needed for recovery. Do not enqueue credentials, large object graphs, request contexts, database handles, or values whose meaning depends on mutable process memory.
- Every handler must be safe for at-least-once and out-of-order delivery. Enforce idempotency with a database unique constraint, processed-event record, monotonic version, compare-and-swap token, or another authoritative guard before applying side effects.
- Return ordinary errors for transient failures that should use the configured bounded retry with exponential backoff and jitter. Join or return `job.ErrPermanent` only for a message that cannot succeed without changing its payload or handler, such as a permanently invalid domain value. Context cancellation must stop work promptly and must not be classified as a permanent business failure.
- A message is acknowledged only after the job succeeds or its terminal failure is successfully copied to the DLQ. If DLQ publication fails, the original message is negatively acknowledged and remains pending. Do not add application-side unconditional acknowledgement.
- The DLQ is an operational recovery boundary, not an automatic retry loop. Monitor it, inspect failure classifications without logging sensitive payloads, fix the handler or data, and replay with the original message ID as an idempotency input. Replaying must not bypass normal validation or idempotency checks.
- Configure both `queue.streamMaxLength` and `queue.deadLetterMaxLength`. Redis Stream approximate `MAXLEN` can temporarily exceed the target and can trim old entries, including pending entries, so size both limits above the measured worst-case lag and recovery window. Alert before either stream approaches its limit; capacity trimming is not a substitute for consumer-lag remediation or durable archival.
- Keep `queue.consumer.concurrency=1` when jobs require per-topic ordering. Increase it only for independent, idempotent jobs and size it against database, Redis, and third-party connection limits. Never add an unbounded goroutine per message.
- Configure `queue.consumer.maxIdleTime` above the longest expected job execution time including in-process retry waits. Otherwise another consumer may claim a still-running message and execute it concurrently. Keep claim batch size and claim interval bounded so recovery cannot monopolize Redis.
- A MySQL commit that must reliably enqueue work requires a transactional outbox written in the same database transaction. Never call `queue.Publish` while a database transaction or row lock is open. Publishing directly after commit is acceptable only when message loss is explicitly best-effort; it is not an atomic MySQL-and-Redis commit.
- Jobs must propagate context to database, Redis, and HTTP calls, stop timers and goroutines, close response bodies, and avoid holding database locks while waiting on unrelated services. Use a durable follow-up job instead of spawning unowned background work.
- Shutdown ownership remains centralized: stop new work acquisition, allow in-flight handlers to finish within `queue.shutdownTimeout`, cancel remaining handlers, close Watermill Subscriber and Publisher once, then close Cache, Redis, and Database resources. Job code must not close shared Queue or Redis clients.
- Monitor publish error rate, throughput, stream length, consumer lag, oldest pending age, retry count, pending claim rate, handler duration, panic count, DLQ publish failures, and DLQ length. Test duplicate delivery, malformed payloads, transient failure, permanent failure, retry exhaustion, DLQ failure, cancellation, panic recovery, long-running claims, and shutdown timeout.

## Durable asynchronous work and workers

- When a database commit must reliably cause a Redis projection, index update, notification, or queue event, use a transactional outbox written in the same MySQL transaction. A worker may then retry the non-authoritative projection without losing the committed business change.
- Design every job for at-least-once delivery. Use idempotency keys, unique constraints, monotonic versions, compare-and-swap tokens, or equivalent guards so replay and out-of-order delivery are safe.
- For versioned projections, ignore an event whose version is not newer than the stored projection. Include enough state in the event or provide a safe source-of-truth reload path for recovery.
- Claim work in bounded batches using a lease or token. Prefer a set-based claim/update pattern such as `FOR UPDATE SKIP LOCKED` plus one bounded update over one claim update per row when supported by the existing database design.
- Batch-load shared context once per worker batch, deduplicate identifiers, and parse shared configuration once. Do not issue the same tenant, namespace, permission, or configuration query for every event.
- Optimize the success path with batch acknowledgement, but preserve per-item attempt counts, errors, backoff, and dead-letter behavior for failures. One poison event must not indefinitely block unrelated successful events.
- Define queue-depth limits, batch sizes, lease duration, retry limit, exponential backoff with jitter, processed-event retention, dead-letter handling, and shutdown behavior. Monitor queue lag, oldest-event age, retry rate, lease recovery, and dead-letter volume.
- Run periodic cleanup with an explicit batch size and time budget so recovery from a backlog cannot monopolize MySQL or Redis.

## Go concurrency and resource bounds

- Propagate `context.Context` deadlines and cancellation through handlers, database calls, Redis wrappers, Resty requests, and jobs. Stop downstream work when the caller or worker lease is no longer valid unless durable completion is explicitly required.
- Use concurrency only for independent operations. Bound it with a worker pool or semaphore sized against downstream connection pools and service limits; never start one unbounded goroutine per record or request item.
- Do not hold database connections, row locks, mutexes, or large buffers while waiting for unrelated network calls.
- Ensure goroutines, timers, tickers, response bodies, streams, and channels have explicit ownership and cleanup. Avoid background goroutines launched from handlers unless the work has durable ownership, observability, cancellation, and shutdown semantics.
- Apply backpressure instead of allowing queues, channels, batches, or memory buffers to grow without limit. Reject, shed, or defer load using an explicit product policy when capacity is exhausted.
- Reuse clients and connection pools; do not construct database, Redis, or HTTP clients per request. Configure pool sizes, timeouts, idle limits, and response-size limits from configuration and validate them against expected concurrency.
- Handle `SIGINT` and `SIGTERM` through one idempotent shutdown coordinator. Stop ingress and work acquisition first, cancel root contexts, drain in-flight work with a configured deadline, then close shared resources in dependency order: queue publishers/subscribers and workers, Redis clients, the underlying database connection pool, and other transports. Log shutdown timeout or close failures without exposing credentials.

### Safe goroutine lifecycle

- New or modified application code must use the project `/xutil` package to start goroutines. Do not use a raw `go` statement, `sync.WaitGroup.Go`, or `errgroup.Group.Go` directly when `xutil.Go`, `xutil.WaitGroup`, or `xutil.ErrGroup` provides the required lifecycle. A low-level concurrency primitive may bypass these `/xutil` wrappers only when implementing a focused concurrency abstraction and the reason is documented and tested.
- Give every task group a stable, low-cardinality name that identifies the component and operation, such as `audio_cleanup` or `queue_shutdown`. The name is used in panic logs and subscriber routing. Never include request IDs, user IDs, payloads, credentials, tokens, or other sensitive or unbounded values in a task name.
- Use `xutil.Go(ctx, name, task)` only for a truly independent task whose completion and returned error are not required by the caller. The task must still have an explicit owner, bounded lifetime, cancellation source, and shutdown behavior. Do not use it to make an HTTP request appear successful before an authoritative write, queue publication, notification, or other required side effect completes. Durable or retryable work belongs in `/queue`, not in an untracked goroutine.
- Use `xutil.WaitGroup(name)` when the caller must wait for several tasks that cannot return meaningful errors. Pass the appropriate context to every `Go` call and always call `Wait` before the owner returns or releases resources used by the tasks:

```go
group := xutil.WaitGroup("cache_refresh")
group.
	Go(ctx, refreshPrimary).
	Go(ctx, refreshSecondary)
group.Wait()
```

- Use `xutil.ErrGroup(ctx, name)` when any task can fail or when the tasks should share fail-fast cancellation. The constructor owns one derived context for the complete group; do not pass a context to each `Go` call. Every task must use the context it receives. Always inspect the error returned by `Wait` with `errors.Is` or `errors.As` as appropriate:

```go
group := xutil.ErrGroup(ctx, "profile_load").Limit(4)
group.
	Go(loadAccount).
	Go(loadPermissions)
if err := group.Wait(); err != nil {
	return err
}
```

- Apply a positive `Limit` before the first `ErrorGroup.Go` call whenever input size or task count can vary. Choose the limit from downstream connection-pool capacity, rate limits, memory cost, and measured load; do not derive it directly from untrusted input. Never call `Limit` while tasks are active, and do not use zero because it prevents tasks from starting.
- A panic inside any protected `/xutil` task is recovered and logged with the task name and stack. In `xutil.ErrGroup`, the panic is converted to an error classifiable with `errors.Is(err, xutil.ErrPanic)` and cancels the shared group context. Recovery prevents a process-wide crash; it does not make partially completed business work successful. Design writes to remain transactional or idempotent, and handle the returned error at the group owner.
- Register `xutil.OnPanic(name, handler)` for metrics, alerting, or another short best-effort reaction. Use `xutil.All` only for process-wide observability initialized by the root command. Subscribers run synchronously on the recovering goroutine, so they must not block, perform unbounded I/O, acquire contested locks, retry work, or become a correctness boundary. Keep the returned unsubscribe function and invoke it during test or component cleanup. Subscriber panics are isolated and logged, but subscribers must still be independently tested.
- Do not add a second generic `recover` wrapper around protected `/xutil` tasks. Keep local `defer` cleanup inside the task for resources it owns; those defers run during stack unwinding before `/xutil` records the panic. Code that intentionally converts a domain-specific panic into a classified error may recover at that narrow boundary, but ordinary business logic should return errors instead of panicking.
- Tests for concurrent code must cover successful completion, caller cancellation, the first returned error, panic conversion, sibling cancellation for `xutil.ErrGroup`, bounded concurrency where `Limit` is used, and shutdown without goroutine leaks. Run the relevant tests with `-race`.

## Verification and completion criteria

An optimization is complete only after correctness and performance are both demonstrated:

- Add or update unit, integration, and concurrency tests for invariants, stale candidates, duplicate submissions, replay, out-of-order events, partial failure, lease expiry, and cancellation as applicable.
- Run race detection and the existing lint/test suite. Use focused benchmarks and load tests for the changed hot path, with realistic data cardinality and contention.
- Compare before/after SQL count, Redis round trips and internal commands, rows examined, allocations, transaction and lock duration, pool waits, queue lag, throughput, error rate, and P50/P95/P99 latency.
- Test dependency slowdown and failure. Confirm timeouts, rollback behavior, idempotent retry, backpressure, and recovery without data loss or an unbounded retry storm.
- Document important consistency changes, capacity formulas, batch-size rationale, operational metrics, and rollback conditions near the relevant package or configuration.

Avoid these common mistakes: optimizing only averages; equating fewer RTTs with less server work; deleting correctness locks; copying all authoritative data into Redis; adding caches without bounds or invalidation; cleaning unbounded data in request paths; using giant Lua scripts; launching unbounded goroutines; adding retries without idempotency and jitter; and declaring success without production-representative P95/P99 evidence.

# Architecture description of this project

This project supports Go modules, and the module information is defined in the `/go.mod` file.

This project is a Golang-based project that uses `github.com/redis/go-redis/v9` as the queue and caching system, and employs the `github.com/go-redis/cache/v9` library for simple cache management and operations. The routing system of this project is based on the `Gin` framework, and the ORM system uses `GORM` with `MySQL` as the database. The queue task framework in this project utilizes `github.com/ThreeDotsLabs/watermill`. The logging system is based on the `github.com/rs/zerolog/log` framework. The command-line program framework uses `github.com/spf13/cobra`. The programs written for this project must pass the `golangci-lint` checks; please refer to the `/.golangci.yaml` file for the requirements. If this project needs to call third-party HTTP interfaces, please use the `github.com/go-resty/resty/v2` library, which provides a well-structured wrapper for HTTP calls.This project uses `github.com/spf13/viper` to read configuration files and environment variables.

You need to organize the programs you write according to the file structure of this project, maintaining a clean and consistent directory structure. Moreover, you should prioritize using the methods defined in to return HTTP responses and data with the correct status codes.

Please refer to the following handler example when writing HTTP handlers.
```golang
func HomePage(c *base.Context) {
    // access the user if user logined
	user := c.LoginUser()
	if user == nil {
        // if not logined, return 403 with forbidden status code
		c.Forbidden("you are not login", gin.H{})
	} else {
        // if logined, return 200 and the user infomation
		c.Success(gin.H{
			"data": fmt.Sprintf("your user id is: %d", user.ID),
		})
	}
}
```
`base.Context` defines commonly used HTTP response methods, including both normal and error responses, as well as functions for globally accessing user information.

After defining the handler, you also need to add it to the mapping in `/api/route/route.go`.
```golang
r.GET("/", handler.HomePage)
```

# Directory or files description for this project

- `/go.mod` : The Go Mod file defines the package name of this Go project as well as its third-party dependencies. You need to use this package name in your code to correctly reference the Go packages defined in the project, and declare and record any third-party packages you use according to the Go Mod specification in this file.
- `/main.go` : The entry file of this project, it is generated by cobra, should not be changed.
- `/api/base/context.go` : The custom context extends gin.Context, I added useful function to the context. You need to first analyze the program in `/api/base/context.go` and prioritize using the various methods defined in `/api/base/context.go` to handle normal and error responses.
- `/api/route/route.go` : Route definition file, all routes are added to this file and can be defined by different functions or categories inside different function groups. Please use `*base.Router` as the route object. You should only write route mapping-related code in `/api/route/route.go` and must not write object initialization or feature initialization code there.
- `/api/route/middleware.go` : Based on the gin framework's middleware, all new middleware should be placed in this file. And use `base.Context` as the context for requests in the middleware.
- `api/server/server.go` : Define and start the gin.Server program, generally do not need to be modified, usually only modify which involves the `github.com/gin-contrib/cors` library to define the cross-domain related configuration, such as adding the domain name, request headers, exposed response headers, or dynamically determine whether the domain name is allowed to cross-domain through the AllowOriginFunc
- `/queue/job/job.go` : This file defines the Job interface for queue jobs. When using queue tasks, implement every method, define the job name and topic, return encoding and decoding errors instead of silently producing zero values, and implement cancellation-aware execution. After a Job is fully defined, register it in `/queue/subscriber.go`.
- /.app.toml : This file is used to store configuration information in TOML format. It can be used to store common configuration settings or environment variables. `/.app.toml.example` is a sample template for the configuration file. When adding or modifying configurations, you need to provide examples in the template.
- /api/handler : This directory is used to store all handler programs that process HTTP requests. All handlers need to use base.Context as a parameter and be properly mapped in the `/api/route/route.go` file.
- /cache : This directory contains ordinary derived-value cache wrappers backed by Redis through `github.com/go-redis/cache/v9`. Application packages should use its focused operations, explicit TTLs, classifiable miss handling, and source-of-truth fallback instead of importing the dependency or using the raw cache client.
- /cmd : This directory is used to store command-line programs, utilizing the `github.com/spf13/cobra` library. It allows invoking non-HTTP functionalities from the command line.
- /db : This directory is db package, used to store programs related to the database and ORM. All ORM-related types and operations should be placed in this directory. We use the `gorm.io/gorm` library as the underlying ORM, and database access via GORM can be obtained through the `db` variable. The `db` variable is globally accessible within the `db` package. In other packages, you can obtain a reference to `db` by calling `DB()` to use the ORM. You should try to avoid performing direct `db` operations outside of the `db` package.
- /queue/job : This directory is used to store all programs related to queue jobs. For the Job interface specification, please refer to the description in `/queue/job/job.go` .
- /queue : This directory owns Watermill Publisher and Subscriber lifecycle, bounded retry, acknowledgement, DLQ routing, stream retention, consumer claims, and shutdown behavior. Application code publishes through `queue.Publish(ctx, job)` and must not construct Watermill Redis Stream clients directly.
- /redis : This directory contains reusable wrappers for specialized Redis data structures and atomic operations. Wrappers receive logical unprefixed keys and apply the configured prefix internally. Feature packages must not directly import or call `github.com/redis/go-redis/v9`, use `GetClient` to assemble command sequences, or pre-prefix wrapper keys; add a focused, validated operation in `/redis` instead. Use `/cache` first for ordinary source-backed caching.
- /services : This directory is used to store programs that call functionalities of third-party or external systems. Programs that can exist relatively independently can also be placed in this folder.
- /xutil : This folder contains reusable wrappers around Go standard-library functionality, including encryption, string processing, protected goroutine startup, panic recovery and subscriptions, wait-group lifecycle, and error-group cancellation. Application code should use `xutil.Go`, `xutil.WaitGroup`, or `xutil.ErrGroup` according to the ownership and error semantics described above instead of starting unmanaged goroutines.
- /storage/static : This directory is used to store static asset files, which can include HTML, JavaScript, CSS, images, etc. These files will be bundled into the Golang binary during the compilation process.
- /storage/views : This directory is used to store HTML files that will be rendered by Golang. HTML files can be categorized into subfolders based on their functionality. Place basic layout files in the `/storage/views/layout` directory, shared components in the `/storage/views/shared` directory, and other specific pages can be categorized according to their functional modules. To automatically reference the layout, page files need to follow the naming convention. The convention is as follows: If the base layout file is named `mylayout.html`, the functional page file should be named `feature.mylayout.html`, i.e., `featureName` + `layoutFileName`.
- /storage/ui : This directory is used to store programs related to front-end frameworks, such as independent programs for Vue, React, and other front-end projects.

# Frontend Architecture and Backend Deployment

The current browser UI is a Vite-based multi-entry application under `/storage/ui`, rendered by Go HTML templates under `/storage/views`, and bundled into the Go binary through `/storage/view.go`.

- Frontend source code lives under `/storage/ui/src/<feature>/`. Each page owns its `main.js` and usually a colocated `main.css`. Existing entries are `src/recorder/main.js`, `src/codec-lab/main.js`, and `src/audio-archives/main.js`.
- The frontend uses Vite, Tailwind CSS v4 via `@tailwindcss/vite`, and Alpine.js. Keep this lightweight page-level architecture unless a feature clearly needs a larger framework. Do not introduce React, Vue, or another SPA framework for ordinary pages.
- Use Alpine.js as the page interaction and state-management mechanism. Define page state and actions in a page-local `x-data` factory, initialize through `x-init` or Alpine `init()`, bind form state with `x-model`, render state with `x-show`/`x-if`/`x-for`, and use Alpine event directives such as `@click`, `@submit.prevent`, `@change`, `@keydown.escape.window`, and their modifiers instead of adding manual DOM listeners for ordinary page interactions. Use `$watch`, `$nextTick`, `$refs`, and `$dispatch` for reactive coordination. When an external browser API or an imperative third-party widget requires `addEventListener`, register it during Alpine initialization and remove it in Alpine `destroy()`; prefer `@event.window`/`@event.document` when Alpine can manage the listener lifecycle.
- Alpine reactive state is implemented with JavaScript `Proxy` objects. Never pass Alpine state, nested reactive values, `$data`, or objects read from Alpine getters directly to `structuredClone`, `postMessage`, History state, or another API that rejects proxies; this causes `DataCloneError`. Convert supported configuration values to plain objects and arrays first with a focused recursive helper such as `cloneConfigValue`, or unwrap and normalize the complete nested value explicitly. Keep derived Alpine getters pure, do not mutate reactive state while rendering, and clone plain configuration data before an imperative editor mutates it. Store DOM nodes in `$refs`, and keep media streams, sockets, timers, class instances, and other non-reactive resources outside Alpine state when possible; if they must be referenced by Alpine, explicitly prevent accidental proxy-dependent cloning and clean them up in `destroy()`.
- Tailwind CSS v4 does not automatically discover the Go HTML templates under `/storage/views` when the stylesheet is compiled from `/storage/ui`. The `includeGoTemplateSources` plugin in `storage/ui/vite.config.js` globally injects the `/storage/views` source directory into every stylesheet that imports Tailwind. Do not add page-local `@source` directives or replace the registered directory with an HTML glob. Tailwind v4 recursively scans the registered directory, while a glob can be treated as a non-matching source path. Keep the global Vite plugin and the post-build CSS verification enabled; otherwise component CSS may load while layout utilities such as `grid`, `flex`, spacing, widths, and responsive variants are silently omitted.
- Configuration-center diffs use app-specific Shiki language and Pierre theme bundles through exact aliases in `storage/ui/vite.config.js`. Keep syntax highlighting limited to `dotenv`, `ini`, `json`, `yaml`, and `toml`, and keep only the `pierre-light` theme unless the UI explicitly adds another supported format or theme. Do not import the top-level full Shiki bundle directly; it emits every grammar and theme as build chunks. Keep `verify-config-center-bundle.mjs` in the production build so dependency upgrades cannot silently restore the full bundle.
- After adding or changing Tailwind classes in a Go template, run `npm run build` inside `/storage/ui` and verify more than build success: confirm representative template utilities exist in the generated CSS or inspect the page's computed layout in a browser (for example, expected Grid/Flex display and padding). A successful Vite build alone does not prove that external templates were scanned.
- `storage/ui/vite.config.js` is the source of truth for frontend entry points. When adding a new page, add its entry to `build.rollupOptions.input`; the manifest key must match the string used by the Go handler, such as `src/feature/main.js`.
- Go page handlers should define a package-level entry constant, call `c.GetCSSJsFiles(entry)`, and pass `title`, `use_vite`, `vite_origin`, `css_files`, and `js_files` into the template. Follow the existing page-handler pattern.
- HTML templates select assets with `use_vite`: when true, include `{{ .vite_origin }}/@vite/client` and the Vite source entry; otherwise include the CSS and JS files resolved from `storage/ui/dist/manifest.json` and served by the Go backend.
- Development defaults to embedded Vite build assets when a manifest is available, so a single Go server can render a fully styled page. For Vite hot reload, set `ui.assetMode="vite"`, run `npm run dev` inside `/storage/ui`, and configure `ui.viteDevOrigin` when it differs from `http://127.0.0.1:5173`.
- Release deployment requires building the frontend before building or packaging the Go binary: run `npm run build` inside `/storage/ui`, then build the Go program. `/storage/view.go` embeds `views`, `ui/dist`, and `static`, so missing `storage/ui/dist/manifest.json` means release templates cannot resolve hashed assets.
- Generated Vite output goes to `/storage/ui/dist` with hashed assets under `/storage/ui/dist/assets`. Keep `dist/keep.txt` as the placeholder that preserves the embedded directory, but do not commit generated `manifest.json` or hashed assets unless a release process explicitly requires checked-in build artifacts.
- Browser assets from Vite are served by the backend at `/assets/*filepath` from the embedded `storage.Assets` filesystem. Use relative URLs or backend-origin URLs for API calls and assets so the same page works in development, release, and reverse-proxy deployments.
- Root static files in `/storage/static` are served by `ServeRootStaticFiles` as fallback files, not as Vite build output. Put long-lived manually maintained root files there only when they should be directly addressable from `/filename`.
- Route definitions stay in `/api/route/route.go`; frontend page handlers and their JSON or WebSocket APIs stay in `/api/handler`. Do not put initialization logic in the route file.
- Frontend API calls should use the existing backend response shape (`status`, `message`, `data`) and relative paths such as `/audio-archives/api/sessions`. Use `location.host` and the current scheme when constructing WebSocket URLs.
- Keep Alpine app factories page-local and expose only the template entry function on `window`, matching `window.recorderApp`, `window.codecLabApp`, and `window.audioArchiveApp`. Revoke object URLs and clean up media streams, worklets, WebSocket connections, and audio contexts when a page flow stops or resets.
- Use `package-lock.json` for deterministic npm installs. Do not commit `node_modules`, tool caches, or per-machine build caches, and do not redirect npm, Vite, Go, or golangci-lint caches into this repository.
- Configuration-center drafts, descriptions, published payloads, and namespace API Keys use the `services/configcrypt` envelope-encryption service. Never store raw encryption keys or plaintext namespace API Keys in `.app.toml`, environment namespaces, the database, source code, logs, or Git. Application configuration may contain only the provider, active key ID, and an absolute external keyring path. Preserve ciphertext versioning and AAD context, retain old key IDs for reads during rotation, and use `config-key reencrypt` before retiring a key.
- The configuration center is disabled unless `configcenter.enabled=true`. When enabled, startup validation requires the configured Basic Auth username and password. Apply `gin.BasicAuth()` to the `/config-center` management page and management APIs; do not implement custom Basic Auth credential parsing or comparison. Do not apply Basic Auth to `/config-center/api/runtime/*`; runtime endpoints use the namespace `X-API-Key` and still require the global configuration-center switch. Disabled configuration-center routes must return `404`, and Basic Auth must be deployed only behind HTTPS.
- Published configuration may be merged into Viper from the root command before child command hooks run through `[configload]`. `configload.targets` is the only supported target definition and is an ordered list in which every target must explicitly own its `source`, namespace, environment, and—when using HTTP—its server URL and API Key environment-variable reference. HTTP timeout is also target-scoped and defaults to five seconds. Do not add global source, HTTP, namespace, environment, single-target, or API Key fallbacks. A target list may mix the trusted local database with multiple independent HTTP servers. Load every target before changing Viper, then merge in declaration order so later targets override earlier targets. Reject duplicate source endpoint/namespace/environment combinations and never leave partially merged remote configuration when one target fails. Keep Cobra parent hook traversal enabled so every child command receives the merged configuration. Initialize `configcrypt` once in that same root hook rather than repeating it in child commands. `config-key generate` must skip the complete atomic target list when any target uses the database because it may be bootstrapping the first keyring; an HTTP-only target list remains available. Load only immutable published snapshots, fail command startup on load errors, preserve Viper environment-variable precedence, and never allow a published payload to override the bootstrap roots `db`, `configcrypt`, `configcenter`, or `configload`. Database targets use local database and keyring settings; every HTTP target independently uses its namespace `X-API-Key`, rejects redirects, and enforces timeout and response-size limits. Never log or include resolved target API Keys in errors.

# Database Table Definitions and Database Migrations
1. When you need to modify database tables or fields, use the atlas tool to define the tables and generate migration files. Database tables should be defined in the `/atlas/schema` directory, and it is recommended to define tables for related modules in a single file. The configuration file for the atlas tool is located in this repository at `/atlas.hcl`.
2. The database table prefix is defined by `[db.table].prefix` in `.app.toml`, but Atlas does not read or synchronize that value automatically. Whenever this prefix changes, before generating any migration, update every table definition in both `/atlas/schema/auth.hcl` and `/atlas/schema/config_center.hcl` to use the same prefix, and update HCL table references such as `table.<prefixed_table>.column.id`. GORM automatically appends `_` when a non-empty configured prefix does not already end with it, so `prefix="gb"` requires Atlas names such as `gb_users` and `gb_config_namespaces`. Do not run `atlas migrate diff` until the configured prefix, both Atlas schema files, their table references, and GORM table names agree. Treat an unexpected bulk drop/recreate migration as a prefix mismatch and stop rather than applying it.
3. GORM database table definitions must follow these conventions:
   - Use plural table names with the table prefix.
   - Every table must have a single `id` primary key using auto-increment semantics. Do not use business keys or compound keys as the primary key.
   - Every table must include `created_at` and `updated_at` fields.
   - Do not define nullable columns. Use explicit defaults instead: empty string for strings, `0` for numbers, `false` or a clear default for booleans, and explicit status defaults when applicable.
   - Business uniqueness must be enforced with unique indexes instead of primary keys.
   - Avoid redundant indexes. If a left prefix of a composite index already covers the query pattern, do not add a separate duplicate prefix index.
   - Keep Atlas schema, generated migrations, and GORM model structs consistent with each other.
4. Generate migrations:

```bash
atlas migrate diff <migration_name> --env local
```

5. If the migration needs default, lookup, or other initialization data, edit the newly generated migration SQL file before applying it. Add the required `INSERT` or other data-migration statements in dependency-safe order; for example, insert a referenced default row after its table is created and before existing rows are backfilled or a foreign key is enforced. Do not expect `atlas migrate diff` to infer ordinary seed data from table definitions.
6. After any manual edit to a generated migration file, recalculate the migration directory checksum:

```bash
atlas migrate hash --env local
```

7. Validate the complete migration directory before applying it:

```bash
atlas migrate validate --env local
```

8. Apply migrations only after the generated SQL, manually added data SQL, checksum, and validation have all been reviewed:

```bash
atlas migrate apply --env local
```

# CRUD actions for GORM model
When you need to provide CRUD operations for a database model (GORM) struct, you can quickly implement them as follows:
1. Have the model implement the `IDModel` interface in `db/crud_core.go`, providing the model’s ID and the loading method for related GORM models.
2. When creating or updating data, create an input form structure that implements the `InputConverter` interface in `db/crud_core.go` to convert the input data into GORM model data.
3. When reading Gorm model data for display, implement the `ViewConverter` interface in `db/crud_core.go` for the output data structure to convert the Gorm model into the output structure.
4. When providing search or listing services, you need to support search and filtering capabilities. In this case, have the input form data structure implement the `Searcher` interface in `db/crud_core.go` to dynamically add search and filter conditions.
5. Finally, provide a CRUD service corresponding to the Model, register it using `NewCrudController`, and add it to the routing.

Below is an implementation of CRUD operations for a user model:
```go
type User struct {
    gorm.Model
    AccountID uint `gorm:"index" json:"account_id"`
    Account   *Account
}

var _ IDModel = &User{}

func (u *User) GetID() uint { return u.ID }

func (u *User) LoadRelation(ctx context.Context, relations ...string) {
    for _, relation := range relations {
        db.WithContext(ctx).Model(u).Association(relation).Find(u.Account)
    }
}
```
The CRUD service and controller for User Model:
```go

type UserCreateForm struct {
	AccountID uint `form:"account_id" json:"account_id" binding:"required"`
}

func (u *UserCreateForm) ToModel() *db.User {
	return &db.User{
		AccountID: u.AccountID,
	}
}

type UserUpdateForm struct {
	Name      string `form:"name" json:"name" binding:"required"`
	AccountID uint   `form:"account_id" json:"account_id" binding:"required"`
}

func (u *UserUpdateForm) ToModel() *db.User {
	p := &db.User{
		AccountID: u.AccountID,
	}
	p.ID = 0
	return p
}

type UserPublicView struct {
	Name      string `json:"name"`
	AccountID uint   `json:"account_id"`
	ID        uint   `json:"id"`
}

func (u *UserPublicView) FromModel(p *db.User) *UserPublicView {
	return &UserPublicView{
		ID:        p.ID,
		Name:      "",
		AccountID: p.AccountID,
	}
}

func (u *UserPublicView) Preloads() []string {
	return []string{}
}

type UserSearchInput struct {
	AccountID uint `form:"account_id"`
}

func (u *UserSearchInput) GetScopes() []func(*gorm.DB) *gorm.DB {
	var scopes []func(*gorm.DB) *gorm.DB

	// 1. Also, You can add preloads to *gorm.DB at here.

	// 2. Filter by account_id
	if u.AccountID > 0 {
		scopes = append(scopes, func(db *gorm.DB) *gorm.DB {
			return db.Where("account_id", u.AccountID)
		})
	}

	return scopes
}

func UserControl(r *base.Router) {
	// Generic parameters：Model, Create, Update, View, Search
	providerService := db.NewCrudService[
		*db.User,
		*UserCreateForm,
		*UserUpdateForm,
		*UserPublicView,
		*UserSearchInput,
	](
		func() *db.User {
			return &db.User{}
		},
		func() *UserPublicView {
			return &UserPublicView{}
		},
		func() *UserSearchInput {
			return &UserSearchInput{}
		},
	)
	providerCtrl := NewCrudController(providerService)
	providerCtrl.RegisterRoutes(r)
}
```
Related files for CRUD services are: `/api/handler/crud_base.go`, `/db/crud_core.go`, `/db/crud_pagination.go`, `/db/crud_service.go`
