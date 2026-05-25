// Timus 1033 — Labyrinth (classic BFS flood-fill counting outer walls)
// http://acm.timus.ru/problem.aspx?space=1&num=1033
//
// Two starting cells: (0,0) and (N-1,N-1). BFS over `.` cells; every
// move that hits a wall (out-of-bounds or non-`.`) counts. Output the
// wall-count × 9 (problem-specific weighting).
//
// Forces: a slice-as-queue idiom (`queue = queue.push(p)` to enqueue;
// `queue[0]` + `queue = queue[1:]` to dequeue), a `Map<Coord, bool>`
// keyed by a record (record structural equality (G24/§Expressions)
// gives the comparable property the map needs), `Option<Coord>` for
// move results, and sum-typed direction enum with exhaustive match.

import fmt
import os

type Coord     = (int, int)                 // (x, y)
type Direction = | Up | Down | Left | Right

let allDirections: []Direction = [Up, Down, Left, Right]

func advance(grid: []string, p: Coord, d: Direction): Option<Coord> {
  let n = grid.len()
  // Reflect boundary moves at the two starting corners (problem rule).
  if p.0 == 0 && p.1 == 0 {
    match d {
      Up | Left => return Some(p),
      _         => {},
    }
  }
  if p.0 == n - 1 && p.1 == n - 1 {
    match d {
      Down | Right => return Some(p),
      _            => {},
    }
  }
  let next = match d {
    Up    => (p.0, p.1 - 1),
    Down  => (p.0, p.1 + 1),
    Left  => (p.0 - 1, p.1),
    Right => (p.0 + 1, p.1),
  }
  if next.0 < 0 || next.0 >= n || next.1 < 0 || next.1 >= n {
    return None
  }
  if grid[next.0][next.1] != byte('.') {
    return None
  }
  return Some(next)
}

func readGrid(): Result<[]string, error> {
  let n = try fmt.scan<int>()
  var rows: []string = []string{}
  for _ in 0..n {
    let row = try fmt.scan<string>()
    if row.len() != n {
      return Err(error("malformatted input row: " + row))
    }
    rows = rows.push(row)
  }
  return Ok(rows)
}

func solve(grid: []string): int {
  let n = grid.len()
  var queue: []Coord = []Coord{}
  var visited: Map<Coord, bool> = Map<Coord, bool>{}

  queue = queue.push((0, 0))
  visited.set((0, 0), true)
  if n > 1 {
    queue = queue.push((n - 1, n - 1))
    visited.set((n - 1, n - 1), true)
  }

  var walls = 0
  while queue.len() > 0 {
    let p = queue[0]
    queue = queue[1:]

    for d in allDirections {
      match advance(grid, p, d) {
        None => walls = walls + 1,                // hit a wall or boundary
        Some(next) => {
          match visited.get(next) {
            Some(_) => {},
            None    => {
              queue = queue.push(next)
              visited.set(next, true)
            },
          }
        },
      }
    }
  }
  return walls * 9
}

func main() {
  match readGrid() {
    Err(e)   => { fmt.println("read error:", e); os.exit(1) },
    Ok(grid) => fmt.println(solve(grid)),
  }
}
