# `concurrency/` — the Go-runtime pitch

These programs are about the side of Tide that other "modern TS-shaped"
languages can't easily reach: cheap goroutines, channels, `select`, and
structured-concurrency `scope` blocks mapped onto Go's `errgroup` +
`context`. The 15-example v1 acceptance suite touches concurrency in two
places (`services/parallel_fetcher`, `services/graceful_server`); this
folder makes the runtime case directly, with one program per canonical
pattern.

| Example | Pattern | Forces |
|---|---|---|
| `pipeline.td` | Three stages connected by channels | directional channel types (`SendChan<T>` / `RecvChan<T>`), `for v in ch` ranging to close |
| `worker_pool.td` | N workers consume a job channel, write a result channel | fan-out + fan-in, bounded parallelism, scope-joined workers |
| `pubsub.td` | One publisher, many subscribers, drop-on-overflow | per-subscriber channels under a mutex, `select` with `default` for non-blocking sends |
| `rate_limited.td` | Ticker bounds the emission rate | `time.tick`, `time.after`, `select` with `default` for non-blocking buffer pushes |
| `nested_scopes.td` | Outer scope launches inner scopes per region | nested structured concurrency, cancellation propagation via `context.Context` |
| `select_showcase.td` | Every `select` case form in one demo | receive-into-binding, receive-and-drop, send, timeout via `time.after` |

These programs are part of the broader paper-validation effort:
constructs they use must be in [`../../docs/language-spec.md`](../../docs/language-spec.md),
and stdlib calls they make must be in
[`../../docs/binding-surface.md`](../../docs/binding-surface.md).
