// Advent of Code 2025, Day 4 — https://adventofcode.com/2025/day/4
//
// 2-D maze of `@` (rolling) and `.` (empty). Each step cleans every `@`
// whose count of adjacent `@`s is ≤ 3. Repeat until no `@` remains;
// report the total cells cleaned and a periodic progress line.
//
// Forces: a `[][]byte` 2-D grid with mutation `grid[r][c] = byte('.')`,
// nested `r,c` iteration, neighbour scan over 8 deltas, byte-literal
// comparison via `byte('@')` (G49).

import fmt
import os
import strings

func readData(path: string): Result<[][]byte, error> {
  let data = try os.readFile(path)
  let text = strings.fromBytes(data)
  var rows: [][]byte = [][]byte{}
  for ln in strings.split(text, "\n") {
    let s = strings.trimSpace(ln)
    if s.len() == 0 { continue }
    rows = rows.push(s.bytes())
  }
  return Ok(rows)
}

func countAdjacent(grid: [][]byte, r: int, c: int): int {
  let roll = byte('@')
  let n = grid.len()
  let m = grid[0].len()
  var count = 0
  for dr in -1..=1 {
    for dc in -1..=1 {
      if dr == 0 && dc == 0 { continue }
      let nr = r + dr
      let nc = c + dc
      if nr < 0 || nr >= n || nc < 0 || nc >= m { continue }
      if grid[nr][nc] == roll { count = count + 1 }
    }
  }
  return count
}

// Returns a list of (r, c) for every `@` cell whose adjacent-@ count
// is ≤ maxAdjacent. Reads from the grid; does not mutate.
func enumerateAccessible(grid: [][]byte, maxAdjacent: int): [](int, int) {
  let roll = byte('@')
  let n = grid.len()
  let m = grid[0].len()
  var out: [](int, int) = [](int, int){}
  for r in 0..n {
    for c in 0..m {
      if grid[r][c] == roll && countAdjacent(grid, r, c) <= maxAdjacent {
        out = out.push((r, c))
      }
    }
  }
  return out
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: d04 <input>")
    os.exit(2)
  }
  match readData(os.args[1]) {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(g)  => run(g),
  }
}

func run(grid: [][]byte) {
  let empty = byte('.')
  var totalClean = 0
  var step       = 0
  var window     = 0     // bucketed sum for steps ≥ 5
  var windowFrom = 0
  while true {
    step = step + 1
    let accessible = enumerateAccessible(grid, 3)
    if accessible.len() == 0 {
      if window != 0 {
        fmt.println("Steps", windowFrom, "..", step - 1, "=>", window, "cells cleaned up")
      }
      fmt.println("Total cleaned up:", totalClean)
      return
    }
    for cell in accessible {
      grid[cell.0][cell.1] = empty
    }
    let n = accessible.len()
    totalClean = totalClean + n
    if step < 5 {
      fmt.println("Step", step, "=>", n, "cells cleaned up")
    } else {
      if window == 0 { windowFrom = step }
      window = window + n
      if step % 20 == 0 {
        fmt.println("Steps", windowFrom, "..", step, "=>", window, "cells cleaned up")
        window = 0
      }
    }
  }
}
