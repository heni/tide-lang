// Timus 1820 — Ural Steaks (max + ceiling-divide on int64)
// http://acm.timus.ru/problem.aspx?space=1&num=1820
//
// Given N, K (people, frying-pan size), the answer is
// max(2, ceil(2N / K)).
//
// Forces: `fmt.scan2<int64, int64>()` for two whitespace-separated
// values, int64 arithmetic, ceiling-divide as `(n*2 + k - 1) / k`.

import fmt
import os

func max(a: int64, b: int64): int64 {
  if a > b { return a }
  return b
}

func main() {
  match fmt.scan2<int64, int64>() {
    Err(e)      => { fmt.println("read error:", e); os.exit(1) },
    Ok((n, k))  => {
      let answer = max(int64(2), (n * int64(2) + k - int64(1)) / k)
      fmt.println(answer)
    },
  }
}
