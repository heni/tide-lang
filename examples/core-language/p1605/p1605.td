// Timus 1605 — Mirror Power-of-Two (math.log10, integer cast)
// http://acm.timus.ru/problem.aspx?space=1&num=1605
//
// Given n, return floor(log10(2) * (n - n%2)) — the number of decimal
// digits in 2^(n - n%2) minus 1, with a small adjustment.
//
// Forces: `math.log10` from the new `math` binding row, `int(float64)`
// truncation, `float64(int)` widening, mixed-type arithmetic.

import fmt
import math
import os

func solve(n: int): int {
  let d  = math.log10(float64(2))
  let np = n - n % 2
  return int(math.floor(d * float64(np)))
}

func main() {
  match fmt.scan<int>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(n)  => fmt.println(solve(n)),
  }
}
