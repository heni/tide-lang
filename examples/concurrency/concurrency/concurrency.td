// Port of Borgo's `concurrency.brg` codegen-emit test.
// Source: https://github.com/borgo-lang/borgo/blob/main/compiler/test/snapshot/codegen-emit/concurrency.exp
//
// The Borgo original mirrors Go closely: it spawns goroutines with
// `spawn (|| {...})()` IIFE-syntax, uses an explicit `sync.WaitGroup`,
// and reads from a bare channel. Tide nudges every piece of this onto
// structured concurrency (scope + spawn + join contract) and channels
// without a separate WaitGroup — the scope itself joins.
//
// Side-by-side:
//   * Borgo `Channel.new()` returns `(sender, receiver)` halves.
//     Tide returns a bidirectional `Channel<T>` from
//     `makeChannel<T>(buf)` and widens to `SendChan<T>` / `RecvChan<T>`
//     implicitly at function-parameter sites.
//   * Borgo `spawn (|| {...})()` is an immediately-invoked lambda.
//     Tide writes the block directly: `spawn { ... }`.
//   * Borgo's manual `sync.WaitGroup.Add(N) / Done() / Wait()` is the
//     scope's join contract in Tide — the trailing expression of the
//     scope (and code after it) runs only when every spawn has
//     finished.
//
// The program computes the sum of integers sent by `desired` worker
// goroutines (each contributing 1) plus two literal sends.

import fmt

func main() {
  let ch = makeChannel<int>(8)

  let initial = scope<int, error> {
    spawn { ch.send(5);  return Ok(()) }
    spawn { ch.send(10); return Ok(()) }

    let a = ch.recv()
    let b = ch.recv()
    a + b                                              // trailing expression: 15
  }
  match initial {
    Ok(sum) => fmt.println("initial sum:", sum),
    Err(e)  => fmt.println("err:", e),
  }

  // At this point `ch` has been drained dry by the first scope's
  // trailing expression — every send was matched by a recv before
  // join. We reuse the same channel for the fan-out below.
  //
  // Fan-out: N workers each send 1 into `ch`; the body collects N and
  // sums them. The scope's join contract replaces sync.WaitGroup.
  let desired = 5
  let total = scope<int, error> {
    for _ in 0..desired {
      spawn { ch.send(1); return Ok(()) }
    }

    var count = 0
    for _ in 0..desired {
      count = count + ch.recv()
    }
    count
  }
  match total {
    Ok(n)  => fmt.println("count:", n),               // 5
    Err(e) => fmt.println("err:", e),
  }
}
