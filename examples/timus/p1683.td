// Timus 1683 — Cutting a Polyglot (greedy, slice accumulator)
// http://acm.timus.ru/problem.aspx?space=1&num=1683
//
// Repeatedly cut off floor(n/2). Print the number of cuts, then each
// cut size separated by spaces.
//
// Forces: a slice accumulator, sequential cut algorithm, multi-line
// output with `fmt.print` for the trailing-space format.

import fmt
import os

func solve(n: int): []int {
  var cuts: []int = []int{}
  var m = n
  while m > 1 {
    let half = m / 2
    cuts = cuts.push(half)
    m = m - half
  }
  return cuts
}

func main() {
  match fmt.scan<int>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(n)  => {
      let cuts = solve(n)
      fmt.println(cuts.len())
      for offset in cuts {
        fmt.print(offset, " ")
      }
      fmt.println()
    },
  }
}
