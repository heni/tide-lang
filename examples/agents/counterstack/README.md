# `agents/counterstack/` — Pentix-arena agent

A single-file Tide sketch of the Pentix-arena agent architecture from
the [counterstack-champion](https://github.com/) Python project. Pentix
is Tetris with 12 pentominoes, run by a 500 Hz server over JSON Lines
on TCP. The Python project is 513 files; this directory keeps the load-
bearing patterns in one place:

| Aspect | Where it shows up in the file |
|---|---|
| **Wire protocol** as a sum type | `type Inbound = | Hello(...) | Tick(...) | ...` with payload-bearing variants; encode/decode round-trips against `encoding/json` |
| **TCP + JSON Lines transport** | `class Connection` wraps `net.dialTCP` and `bufio.Scanner` / `bufio.Writer` (new binding-surface rows) |
| **Concurrent reader / writer / decision loop** | `scope<int, error>` with three spawns sharing inbox / outbox channels and one cancellable context |
| **Strategy as an interface** | `interface Strategy { decide(...): string }` with a trivial `class AlwaysDrop implements Strategy` |
| **Per-variant DTOs for the wire** | `type HelloPayload = {...}` etc. — JSON serialization keys come from declared field names, not from an anonymous shape |
| **Generic record** | `type Envelope<P>` — newly speced in `docs/language-spec.md` §Records |

The decode side is sketched — production code would call
`json.parse<TickPayload>` on the `.p` substring rather than hand-build
each `Inbound` case. The point of the file is to anchor the
architectural shape, not to be wire-correct.
