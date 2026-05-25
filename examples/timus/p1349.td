// Timus 1349 — Pythagoreans' Trousers (case dispatch)
// http://acm.timus.ru/problem.aspx?space=1&num=1349
//
// For N = 1 print "1 2 3", for N = 2 print "3 4 5", otherwise print -1.
//
// Forces: `if`/`else if`/`else` chain at statement position, three-arg
// variadic `fmt.println`.

import fmt
import os

func main() {
  match fmt.scan<int>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(n)  => {
      if n == 1 {
        fmt.println(1, 2, 3)
      } else if n == 2 {
        fmt.println(3, 4, 5)
      } else {
        fmt.println(-1)
      }
    },
  }
}
