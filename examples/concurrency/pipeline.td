// A three-stage pipeline: produce → transform → consume, each stage in
// its own goroutine, connected by channels. The classic Go idiom that
// `for v in ch { ... }` ranges-until-closed makes natural.
//
// Forces: directional channel types (SendChan<T>, RecvChan<T> — see
// language-spec.md §Concurrency · Channels), spawn + fire-and-forget
// scope, range over a receive-only channel, ch.close() to terminate
// downstream stages.

import fmt

// Stage 1: emit the integers 1..n on `out`, then close.
func produce(n: int, out: SendChan<int>) {
  for i in 1..=n {
    out.send(i)
  }
  out.close()
}

// Stage 2: read from `inCh`, square each value, push to `out`, then close.
func square(inCh: RecvChan<int>, out: SendChan<int>) {
  for v in inCh {
    out.send(v * v)
  }
  out.close()
}

// Stage 3: read from `inCh`, accumulate a sum, push it to `out`, close.
func sum(inCh: RecvChan<int>, out: SendChan<int>) {
  var total = 0
  for v in inCh {
    total = total + v
  }
  out.send(total)
  out.close()
}

func main() {
  let nums    = makeChannel<int>(4)
  let squares = makeChannel<int>(4)
  let result  = makeChannel<int>(1)

  // All three stages are fire-and-forget; the pipeline's output flows
  // out through `result`. The scope joins when every stage's spawn
  // returns (which happens once each stage's input is closed and
  // drained).
  match scope<unit, error> {
    spawn { produce(10, nums); return Ok(()) }
    spawn { square(nums, squares); return Ok(()) }
    spawn { sum(squares, result); return Ok(()) }
  } {
    Ok(_)  => {},
    Err(e) => fmt.println("pipeline error:", e),
  }

  // sum of squares 1..=10 = 1 + 4 + 9 + ... + 100 = 385
  fmt.println("sum of squares 1..=10 =", result.recv())
}
