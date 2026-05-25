// Advent of Code 2025, Day 3 — https://adventofcode.com/2025/day/3
//
// Each input line is a run of digits — a "battery line". For a given
// length L, build the largest L-digit number you can read off by
// picking digits in increasing index order. Sum over all lines the
// largest L=2 number (Simple) and L=12 number (Hard).
//
// Forces: per-character digit parsing, a monotonic stack over `int`,
// stack slicing — `Stack<T>` doesn't expose slicing directly, so we
// model the stack as a `var []int` and use `[a:b]`.
//
// Algorithm: maintain a non-increasing stack of length ≤ L. For each
// new digit d at position idx (with budget max_depth = min(remaining,
// L)), pop while we still have room AND d > top. Push d if the stack
// is shorter than L. The stack at the end is the answer's digit
// sequence.

import fmt
import os
import strconv
import strings

func readData(path: string): Result<[][]int, error> {
  let data = try os.readFile(path)
  let text = strings.fromBytes(data)
  var out: [][]int = [][]int{}
  for ln in strings.split(text, "\n") {
    let s = strings.trimSpace(ln)
    if s.len() == 0 { continue }
    var line: []int = []int{}
    var i = 0
    while i < s.len() {
      let n = try strconv.atoi(s[i:i+1])
      line = line.push(n)
      i = i + 1
    }
    out = out.push(line)
  }
  return Ok(out)
}

func solveLine(line: []int, length: int): int {
  var stack: []int = []int{}
  for (idx, d) in line {
    var sp = stack.len()
    let remaining = line.len() - idx
    let maxDepth  = if remaining < length { remaining } else { length }
    while sp > length - maxDepth && d > stack[sp - 1] {
      sp = sp - 1
    }
    stack = stack[0:sp]
    if sp < length {
      stack = stack.push(d)
    }
  }
  // The stack holds the digits, most-significant first.
  var value = 0
  for d in stack {
    value = value * 10 + d
  }
  return value
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: d03 <input>")
    os.exit(2)
  }
  match readData(os.args[1]) {
    Err(e)    => { fmt.println("read error:", e); os.exit(1) },
    Ok(lines) => {
      var simple = 0
      var hard   = 0
      for line in lines {
        simple = simple + solveLine(line, 2)
        hard   = hard   + solveLine(line, 12)
      }
      fmt.println("Simple:", simple)
      fmt.println("Hard:  ", hard)
    },
  }
}
