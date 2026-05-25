// Timus 1335 — Sense of Beauty (math, stdin smoke test)
// http://acm.timus.ru/problem.aspx?space=1&num=1335
//
// Given N, print N*(N+1), N*(N+2), N*N on one line.
//
// Forces: `fmt.scan<uint64>()` for stdin (the bound form of Go's
// `fmt.Scan(&N)`), uint64 arithmetic, variadic `fmt.println` for
// space-separated output.

import fmt
import os

func main() {
  match fmt.scan<uint64>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(n)  => {
      fmt.println(n * (n + 1), n * (n + 2), n * n)
    },
  }
}
