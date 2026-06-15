// Advent of Code 2025, Day 2 — https://adventofcode.com/2025/day/2
//
// Comma-separated `start-end` ranges per input line. For each integer
// in each range, check a "bad ID" predicate:
//   * simple: the decimal string is a repeated pair (length even AND
//     first half == second half);
//   * hard:   the decimal string can be tiled by some divisor-length
//     chunk repeated identically (so 122122122 is bad, 12122 is not).
// Sum the bad IDs.
//
// Forces: line + range parsing, string slicing `s[a:b]` (G45), tuple
// type alias `type Range = (int, int)`, sorting `[]string`, and a
// `(string) => bool` first-class function value passed as `check`.

import fmt
import os
import sort
import strconv
import strings

type Range = (int, int)

func readData(path: string): Result<[]Range, error> {
  let data = try os.readFile(path)
  let text = strings.fromBytes(data)
  var out: []Range = []Range{}
  for ln in strings.split(text, "\n") {
    let row = strings.trimSpace(ln)
    if row.len() == 0 { continue }
    for region in strings.split(row, ",") {
      let r = strings.trimSpace(region)
      if r.len() == 0 { continue }
      let parts = strings.split(r, "-")
      let st  = try strconv.atoi(parts[0])
      let fin = try strconv.atoi(parts[1])
      out = out.push((st, fin))
    }
  }
  return Ok(out)
}

func checkSimple(s: string): bool {
  let n = s.len()
  if n % 2 != 0 { return false }
  return s[0:n/2] == s[n/2:n]
}

func checkHard(s: string): bool {
  let n = s.len()
  var d = 1
  while d <= n / 2 {
    if n % d == 0 {
      var parts: []string = []string{}
      var i = 0
      while i < n {
        parts = parts.push(s[i:i+d])
        i = i + d
      }
      let sorted = sort.sorted(parts, (a, b) => a < b)
      if sorted[0] == sorted[sorted.len() - 1] {
        return true
      }
    }
    d = d + 1
  }
  return false
}

func sumBadIDs(ranges: []Range, check: (string) => bool): int {
  var total = 0
  for r in ranges {
    let (st, fin) = r
    var n = st
    while n <= fin {
      if check(strconv.itoa(n)) {
        total = total + n
      }
      n = n + 1
    }
  }
  return total
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: d02 <input>")
    os.exit(2)
  }
  match readData(os.args[1]) {
    Err(e)     => { fmt.println("read error:", e); os.exit(1) },
    Ok(ranges) => {
      fmt.println("Simple:", sumBadIDs(ranges, checkSimple))
      fmt.println("Hard:  ", sumBadIDs(ranges, checkHard))
    },
  }
}
