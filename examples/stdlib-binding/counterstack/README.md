# `agents/counterstack/` — Pentix-arena agent

A single-file Tide sketch of the Pentix-arena agent architecture from
the *counterstack-champion* Python project. Pentix is Tetris with 12
pentominoes, run by a 500 Hz server over JSON Lines on TCP. The Python
project is 513 files; this directory keeps the load-bearing patterns
in one place:

| Aspect | Where it shows up in the file |
|---|---|
| **Wire protocol** as a sum type | `type Inbound = | Hello(...) | Tick(...) | ...` with payload-bearing variants; encode/decode round-trips against `encoding/json` |
| **TCP + JSON Lines transport** | `class Connection` wraps `net.dialTCP` and `bufio.Scanner` / `bufio.Writer` (new binding-surface rows) |
| **Concurrent reader + writer + inline decision loop** | `scope<int, error>` with **two** long-running spawns (reader, writer) sharing inbox / outbox channels; the decision loop runs inline in the scope body. The reader's `defer inbox.close()` lets the inline loop terminate deterministically on EOF or transport error |
| **Strategy as an interface** | `interface Strategy { decide(...): string }` with a trivial `class AlwaysDrop implements Strategy` |
| **Per-variant DTOs for the wire** | `type HelloPayload = {...}` etc. — JSON serialization keys come from declared field names, not from an anonymous shape |
| **Generic record** | `type Envelope<P>` — newly speced in `docs/language-spec.md` §Records |
| **`json.RawMessage` idiom** | `RawInbound.p: []byte` keeps the raw inner JSON for a second-pass typed parse, so the user-side code never has to touch `Any` (`Any` is kept to the binding boundary only) |

The decode side is sketched — production code would call
`json.parse<TickPayload>(raw.p)` etc. on the captured raw-bytes `.p`
to narrow per envelope kind. The point of the file is to anchor the
architectural shape, not to be wire-correct against the live server.
