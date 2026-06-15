// Advent of Code 2025, Day 1 — https://adventofcode.com/2025/day/1
//
// Each input line is a rotation direction ('L' or 'R') followed by a count
// (e.g. "L20", "R5"). Starting at position 50 on a 100-position dial,
// apply each rotation and count how many times the dial passes through 0.
//
// Forces: file read, line splitting, string slicing `s[a:b]`,
// integer parsing, modular arithmetic.

import fmt
import os
import strconv
import strings

type Step = {
  dir:   string       // "L" or "R" — single-char substring
  count: int
}

func readData(path: string): Result<[]Step, error> {
  let data = try os.readFile(path)
  let text = strings.fromBytes(data)
  var out: []Step = []Step{}
  for ln in strings.split(text, "\n") {
    let s = strings.trimSpace(ln)
    if s.len() == 0 { continue }
    let dir   = s[0:1]
    let count = try strconv.atoi(s[1:])
    out = out.push(Step{ dir: dir, count: count })
  }
  return Ok(out)
}

func solveSimple(steps: []Step): int {
  var pos  = 50
  var code = 0
  for s in steps {
    if s.dir == "L" {
      pos = (pos + 100 - s.count) % 100
    } else {
      pos = (pos + s.count) % 100
    }
    if pos == 0 { code = code + 1 }
  }
  return code
}

func solveHard(steps: []Step): int {
  var pos  = 50
  var code = 0
  for s in steps {
    if s.dir == "L" {
      code = code + (100 - pos + s.count) / 100 - (100 - pos) / 100
      pos  = (pos + 100 - s.count) % 100
    } else {
      code = code + (pos + s.count) / 100
      pos  = (pos + s.count) % 100
    }
  }
  return code
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: d01 <input>")
    os.exit(2)
  }
  match readData(os.args[1]) {
    Err(e)    => { fmt.println("read error:", e); os.exit(1) },
    Ok(steps) => {
      fmt.println("Simple:", solveSimple(steps))
      fmt.println("Hard:  ", solveHard(steps))
    },
  }
}
