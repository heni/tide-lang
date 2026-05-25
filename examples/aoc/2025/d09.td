// Advent of Code 2025, Day 9 — https://adventofcode.com/2025/day/9
//
// Polygon corners on a grid. For each pair of corners, the smallest
// axis-aligned bounding rectangle.
//   * Simple: the maximum area among all such rectangles.
//   * Hard:   the maximum area among rectangles whose interior is
//             not crossed by any polygon edge.
//
// Forces: tuples nested into tuples (`type Rect = (Corner, Corner)`),
// pairwise iteration, axis-aligned-segment vs open-rectangle
// intersection arithmetic, and the for/else idiom written explicitly
// as a "no-break-seen" flag.

import fmt
import os
import strconv
import strings

type Corner = (int, int)
type Rect   = (Corner, Corner)

func readData(path: string): Result<[]Corner, error> {
  let data = try os.readFile(path)
  let text = strings.fromBytes(data)
  var out: []Corner = []Corner{}
  for ln in strings.split(text, "\n") {
    let s = strings.trimSpace(ln)
    if s.len() == 0 { continue }
    let parts = strings.split(s, ",")
    if parts.len() != 2 {
      return Err(error("bad line, expected x,y: " + ln))
    }
    let x = try strconv.atoi(strings.trimSpace(parts[0]))
    let y = try strconv.atoi(strings.trimSpace(parts[1]))
    out = out.push((x, y))
  }
  return Ok(out)
}

func intMin(a: int, b: int): int { if a < b { return a }; return b }
func intMax(a: int, b: int): int { if a > b { return a }; return b }

func bounding(c0: Corner, c1: Corner): Rect {
  let x0 = intMin(c0.0, c1.0)
  let y0 = intMin(c0.1, c1.1)
  let x1 = intMax(c0.0, c1.0)
  let y1 = intMax(c0.1, c1.1)
  return ((x0, y0), (x1, y1))
}

func rectArea(r: Rect): int {
  return (r.1.0 - r.0.0 + 1) * (r.1.1 - r.0.1 + 1)
}

func area(c0: Corner, c1: Corner): int {
  return rectArea(bounding(c0, c1))
}

// Does an axis-aligned segment cross the *interior* of `rect`? "On the
// boundary" does not count — same convention as the Python reference.
func segmentCrossesRect(rect: Rect, a: Corner, b: Corner): bool {
  let xr0 = rect.0.0
  let yr0 = rect.0.1
  let xr1 = rect.1.0
  let yr1 = rect.1.1
  if a.0 == b.0 {
    // Vertical segment at x = a.0; check y span.
    return xr0 < a.0 && a.0 < xr1
        && intMin(a.1, b.1) < yr1
        && intMax(a.1, b.1) > yr0
  }
  if a.1 == b.1 {
    // Horizontal segment at y = a.1; check x span.
    return yr0 < a.1 && a.1 < yr1
        && intMin(a.0, b.0) < xr1
        && intMax(a.0, b.0) > xr0
  }
  // Diagonal segments don't occur in this puzzle's inputs.
  return false
}

func solveSimple(corners: []Corner): int {
  let n = corners.len()
  var best = 0
  for i in 0..n {
    for j in (i + 1)..n {
      let a = area(corners[i], corners[j])
      if a > best { best = a }
    }
  }
  return best
}

func solveHard(corners: []Corner): int {
  let n = corners.len()
  var best = 0
  for i in 0..n {
    for j in (i + 1)..n {
      let rect = bounding(corners[i], corners[j])
      var crossed = false
      for k in 0..n {
        let next = (k + 1) % n
        if segmentCrossesRect(rect, corners[k], corners[next]) {
          crossed = true
          break
        }
      }
      // Python's `for/else` equivalent: only score if no break fired.
      // Reuse `rect` from the bounding() call above — no redundant
      // bounding-recompute.
      if !crossed {
        let a = rectArea(rect)
        if a > best { best = a }
      }
    }
  }
  return best
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: d09 <input>")
    os.exit(2)
  }
  match readData(os.args[1]) {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(cs) => {
      // Report grid size (max coordinate + 1) like the Python original.
      var maxCoord = 0
      for c in cs {
        if c.0 > maxCoord { maxCoord = c.0 }
        if c.1 > maxCoord { maxCoord = c.1 }
      }
      fmt.println("Corners:", cs.len(), maxCoord + 1, "x", maxCoord + 1)
      fmt.println("Simple answer:", solveSimple(cs))
      fmt.println("Hard answer:  ", solveHard(cs))
    },
  }
}
