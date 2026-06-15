// Timus 1786 — Sandro's Book (2-D DP, weighted edit distance)
// http://acm.timus.ru/problem.aspx?space=1&num=1786
//
// Slide a fixed pattern "Sandro" along a longer text; at every text
// position compute the cost of aligning the pattern's k-th character
// against text[i] (exact = 0, case-fold = 5, letter-class mismatch =
// 5, default = 10), accumulated diagonally. The cheapest full-pattern
// alignment is the answer; if none aligns, print -1.
//
// Forces: a `[][]int` 2-D DP table allocated via two-step
// `makeSlice<[]int>(n)` + per-row `makeSlice<int>(m)`, chained
// index-write `dp[i][j] = ...` (G45 corollary), `Option<int>` modeled
// as `int` with a -1 sentinel matching the Go reference (Tide would
// idiomatically use `Option<int>` — left as Option-style sentinel for
// faithfulness to the reference), and rune-level case/class checks.

import fmt
import os

// Note: the problem input is bounded to ASCII, so the helpers below
// narrow Go's `unicode.IsLower` / `unicode.ToLower` to the ASCII
// range. Adding a `unicode` binding row would let this file mirror
// Go faithfully; deferred — see spec-gaps backlog.
func isLower(c: rune): bool {
  return c >= 'a' && c <= 'z'
}

func isUpper(c: rune): bool {
  return c >= 'A' && c <= 'Z'
}

func toLower(c: rune): rune {
  if isUpper(c) { return rune(int(c) + (int('a') - int('A'))) }
  return c
}

func cost(a: rune, b: rune): int {
  if a == b                              { return 0  }
  if toLower(a) == toLower(b)            { return 5  }
  if isLower(a) == isLower(b)            { return 5  }
  return 10
}

func solve(text: []rune, pattern: []rune): int {
  let n = text.len()
  let m = pattern.len()

  let dp = makeSlice<[]int>(n)
  for i in 0..n {
    dp[i] = makeSlice<int>(m)
  }

  var best = -1
  for i in 0..n {
    for j in 0..m {
      let c = cost(text[i], pattern[j])
      if j == 0 {
        dp[i][j] = c
      } else if i == 0 {
        dp[i][j] = -1
      } else if dp[i - 1][j - 1] != -1 {
        dp[i][j] = dp[i - 1][j - 1] + c
      } else {
        dp[i][j] = -1
      }
    }
    let v = dp[i][m - 1]
    if v != -1 && (best == -1 || best > v) {
      best = v
    }
  }
  return best
}

func main() {
  let s = match fmt.scan<string>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(x)  => x,
  }
  let pattern = "Sandro".runes()
  let text    = s.runes()
  fmt.println(solve(text, pattern))
}
