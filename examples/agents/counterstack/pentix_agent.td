// CounterStack Pentix agent — a Tide port sketch of the architecture
// from the `counterstack-champion` Python project. Pentix is Tetris
// with 12 pentominoes, run by a 500 Hz server over JSON Lines on TCP.
//
// This file ports the three load-bearing pieces of that project in a
// single demonstration:
//
//   1. **Wire protocol** — a sum-typed `Inbound`/`Outbound` plus
//      encode/decode against `encoding/json`. The Python original
//      dispatches at runtime on a string `t` field; the Tide port
//      lifts that to an exhaustive `match`.
//   2. **TCP + JSON Lines transport** — `net.dialTCP` connects;
//      `bufio.Scanner` reads newline-delimited envelopes; a buffered
//      writer pushes outbound envelopes. Concurrent reader / writer
//      spawns feed an inbox / outbox channel pair.
//   3. **Game loop** — a `Strategy implements`-style interface
//      decides one `Action` per inbound `Tick`; the loop terminates
//      cleanly on `MatchEnd` or a transport error.
//
// Forces: bound `net` + `bufio` (new binding-surface rows), exhaustive
// `match` over the inbound sum type, a `class implements Strategy`
// pattern, structured concurrency with two long-running spawns that
// share inbox / outbox channels, `defer ch.close()` for clean shutdown
// on the reader side, `defer conn.close()` for the transport.

import bufio
import fmt
import json
import net
import os
import strconv
import strings

// ---------------------------------------------------------------------------
// Wire protocol — see http://counter-stack.rutsh.com/spec.html v0.5
// ---------------------------------------------------------------------------

let protocolVersion = 5

type Piece = | F | I | L | N | P | T | U | V | W | X | Y | Z

// Inbound envelopes the agent reacts to. The wire format has many more
// event kinds; we model the subset the loop actually consumes.
type Inbound =
  | Hello(serverName: string, version: int)
  | Tick(tickNo: int, current: Piece, next: []Piece, board: [][]bool)
  | Ping(nonce: int)
  | MatchEnd(score: int, rank: int)

// Outbound envelopes. Operations are the server's accepted opcodes.
type Outbound =
  | HelloOut(agentName: string, token: string)
  | Ready(configName: string)
  | Action(op: string)                              // left / right / rot_cw / rot_ccw / drop / noop
  | Pong(nonce: int)

// DTOs we hand to `json.serialize`. Records, not anonymous shapes —
// json infers the field-key map from the declared field names.
type HelloPayload = {
  agent_name:       string
  token:            string
  protocol_version: int
}
type ReadyPayload  = { config_name: string }
type ActionPayload = { op: string }
type PongPayload   = { nonce: int }

type Envelope<P> = {
  t: string
  q: Option<string>
  p: P
}

func encodeOutbound(o: Outbound): []byte {
  let raw = match o {
    HelloOut(name, token) =>
      json.serialize(Envelope<HelloPayload>{
        t: "hello", q: None,
        p: HelloPayload{ agent_name: name, token: token, protocol_version: protocolVersion },
      }),
    Ready(cfg) =>
      json.serialize(Envelope<ReadyPayload>{
        t: "ready", q: None, p: ReadyPayload{ config_name: cfg },
      }),
    Action(op) =>
      json.serialize(Envelope<ActionPayload>{
        t: "action", q: Some("own"), p: ActionPayload{ op: op },
      }),
    Pong(nonce) =>
      json.serialize(Envelope<PongPayload>{
        t: "pong", q: Some("keepalive"), p: PongPayload{ nonce: nonce },
      }),
  }
  match raw {
    Ok(bytes) => return bytes.push(byte('\n')),
    Err(e)    => {
      fmt.println("encode error:", e)
      return []byte{}
    },
  }
}

// First-pass shape — `p` stays as raw JSON bytes (Go's
// `json.RawMessage` idiom) so a second-pass parse can narrow per
// envelope kind without resorting to G23's `Any` escape in
// user-authored code.
type RawInbound = {
  t: string
  q: Option<string>
  p: []byte
}

// Decode one JSON-Lines envelope into the typed Inbound. Errors are
// reported as `Err(error)` so a single malformed line doesn't kill
// the loop. The per-arm payload narrowing is sketched — production
// code would call `json.parse<TickPayload>(raw.p)` etc.
func decodeInbound(line: []byte): Result<Inbound, error> {
  let raw = try json.parse<RawInbound>(line)
  match raw.t {
    "hello"     => Ok(Hello("?server", protocolVersion)),
    "tick"      => Ok(Tick(0, I, []Piece{}, [][]bool{})),
    "ping"      => Ok(Ping(0)),
    "match_end" => Ok(MatchEnd(0, 0)),
    _           => Err(error("unknown envelope kind: " + raw.t)),
  }
}

// ---------------------------------------------------------------------------
// Strategy
// ---------------------------------------------------------------------------

interface Strategy {
  decide(current: Piece, next: []Piece, board: [][]bool): string
}

// A trivial strategy: always drop. Real strategies live in their own
// files (see the Python project's `strategies/`).
class AlwaysDrop implements Strategy {
  decide(current: Piece, next: []Piece, board: [][]bool): string {
    return "drop"
  }
}

// ---------------------------------------------------------------------------
// Connection — TCP + JSONL with concurrent reader / writer
// ---------------------------------------------------------------------------

class Connection {
  let conn:   net.Conn
  let scan:   bufio.Scanner
  let writer: bufio.Writer

  static dial(host: string, port: int): Result<Connection, error> {
    let addr = host + ":" + strconv.itoa(port)
    let c    = try net.dialTCP(addr)
    return Ok(Connection{
      conn:   c,
      scan:   bufio.newScanner(c),
      writer: bufio.newWriter(c),
    })
  }

  send(o: Outbound): Result<unit, error> {
    let bytes = encodeOutbound(o)
    try writer.write(bytes)
    try writer.flush()
    return Ok(())
  }

  // Returns None at EOF / clean close, Some(env) otherwise.
  recv(): Option<Result<Inbound, error>> {
    if !scan.scan() {
      match scan.err() {
        Some(e) => return Some(Err(e)),
        None    => return None,                       // clean EOF
      }
    }
    return Some(decodeInbound(scan.bytes()))
  }
}

// ---------------------------------------------------------------------------
// Main loop
// ---------------------------------------------------------------------------

func runAgent(host: string, port: int, agentName: string, strat: Strategy):
    Result<int, error> {
  let conn = try Connection.dial(host, port)
  defer conn.conn.close()

  let inbox  = makeChannel<Inbound>(64)
  let outbox = makeChannel<Outbound>(64)

  return scope<int, error> {
    // Reader: pull lines until EOF or transport error. Closing `inbox`
    // on the way out (clean or otherwise) lets the body's `for env in
    // inbox` exit deterministically — without this the scope hangs at
    // match end.
    spawn {
      defer inbox.close()
      var done = false
      while !done {
        match conn.recv() {
          None           => { done = true },          // clean EOF
          Some(Ok(env))  => inbox.send(env),
          Some(Err(e))   => return Err(e),
        }
      }
      return Ok(())
    }

    // Writer: drain outbox until closed.
    spawn {
      for env in outbox {
        try conn.send(env)
      }
      return Ok(())
    }

    // Decision loop runs inline in the scope body. The body owns
    // every send to `outbox` and so closes it at the very end —
    // closing earlier (e.g. inside the reader's `defer`) would race
    // with these sends.
    outbox.send(HelloOut(agentName, ""))
    outbox.send(Ready("pentris-7"))
    var finalScore = 0
    for env in inbox {
      match env {
        Hello(_, _)             => {},
        Ping(nonce)             => outbox.send(Pong(nonce)),
        Tick(_, cur, nxt, brd)  => outbox.send(Action(strat.decide(cur, nxt, brd))),
        MatchEnd(score, rank)   => {
          fmt.println("match end: score=", score, "rank=", rank)
          finalScore = score
          break
        },
      }
    }
    outbox.close()
    finalScore                                        // trailing expression
  }
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: pentix_agent <host:port>")
    os.exit(2)
  }
  let parts = strings.split(os.args[1], ":")
  if parts.len() != 2 {
    fmt.println("bad address, expected host:port")
    os.exit(2)
  }
  let host = parts[0]
  match strconv.atoi(parts[1]) {
    Err(_) => { fmt.println("bad port"); os.exit(2) },
    Ok(port) => {
      let strat = AlwaysDrop{}
      match runAgent(host, port, "tide-pentix-demo", strat) {
        Ok(score) => {
          fmt.println("final score:", score)
          os.exit(0)
        },
        Err(e) => {
          fmt.println("agent error:", e)
          os.exit(1)
        },
      }
    },
  }
}
