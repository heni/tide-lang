// Advent of Code 2025, Day 8 — https://adventofcode.com/2025/day/8
//
// 3-D points in a star catalogue. For each pair of points, compute the
// Euclidean distance. Sort the pairs by distance ascending, then:
//   * Simple: after joining the first K pairs into a DSU, return the
//     product of the three largest component sizes.
//   * Hard:   walk pairs in order; the first join that fills all N
//     stars into one component is the answer, returned as the product
//     of those two points' X coordinates.
//
// Forces: a `class DSU` with array-backed parent and size tables,
// path-compressing `find` that writes through a `this.field[i] = v`
// chain; `math.sqrt`; primitive numeric conversion `float64(int)`
// (G49); `sort.sorted` over a `(int, int, float64)` tuple by `.2`
// inside `buildPairs` and by `.1` inside `components()` (G24).

import fmt
import math
import os
import sort
import strconv
import strings

type Point = (int, int, int)
type Pair  = (int, int, float64)

func readData(path: string): Result<[]Point, error> {
  let data = try os.readFile(path)
  let text = strings.fromBytes(data)
  var out: []Point = []Point{}
  for ln in strings.split(text, "\n") {
    let s = strings.trimSpace(ln)
    if s.len() == 0 { continue }
    let parts = strings.split(s, ",")
    if parts.len() != 3 {
      return Err(error("bad line, expected x,y,z: " + ln))
    }
    let x = try strconv.atoi(strings.trimSpace(parts[0]))
    let y = try strconv.atoi(strings.trimSpace(parts[1]))
    let z = try strconv.atoi(strings.trimSpace(parts[2]))
    out = out.push((x, y, z))
  }
  return Ok(out)
}

func dist(a: Point, b: Point): float64 {
  let dx = a.0 - b.0
  let dy = a.1 - b.1
  let dz = a.2 - b.2
  return math.sqrt(float64(dx * dx + dy * dy + dz * dz))
}

class DSU {
  var id: []int
  var sz: []int

  static new(n: int): DSU {
    var id: []int = []int{}
    var sz: []int = []int{}
    for i in 0..n {
      id = id.push(i)
      sz = sz.push(1)
    }
    return DSU{ id: id, sz: sz }
  }

  // Find with path compression. The function is recursive; `id` is a
  // `var` field so the path-compressing write is legal.
  find(a: int): int {
    if this.id[a] != a {
      this.id[a] = this.find(this.id[a])
    }
    return this.id[a]
  }

  // Returns the new root on a successful join, -1 if `a` and `b` were
  // already in the same component.
  join(a: int, b: int): int {
    var ra = this.find(a)
    var rb = this.find(b)
    if ra == rb {
      return -1
    }
    if this.sz[ra] < this.sz[rb] {
      let tmp = ra
      ra = rb
      rb = tmp
    }
    this.sz[ra] = this.sz[ra] + this.sz[rb]
    this.id[rb] = ra
    return ra
  }

  // Component sizes (root index, size), sorted descending by size.
  components(): [](int, int) {
    var out: [](int, int) = [](int, int){}
    for (i, sz) in this.sz {
      if this.id[i] == i {
        out = out.push((i, sz))
      }
    }
    return sort.sorted(out, (a, b) => a.1 > b.1)
  }
}

func solveSimple(n: int, pairs: []Pair, k: int): int {
  let g = DSU.new(n)
  var i = 0
  while i < k && i < pairs.len() {
    let _ = g.join(pairs[i].0, pairs[i].1)
    i = i + 1
  }
  let comps = g.components()
  var prod = 1
  let top = if comps.len() < 3 { comps.len() } else { 3 }
  var j = 0
  while j < top {
    prod = prod * comps[j].1
    j = j + 1
  }
  return prod
}

func solveHard(points: []Point, pairs: []Pair): Result<int, error> {
  let n = points.len()
  let g = DSU.new(n)
  for p in pairs {
    let root = g.join(p.0, p.1)
    if root != -1 && g.sz[root] == n {
      return Ok(points[p.0].0 * points[p.1].0)
    }
  }
  return Err(error("unreachable: graph never became connected"))
}

func buildPairs(points: []Point): []Pair {
  var pairs: []Pair = []Pair{}
  let n = points.len()
  for i in 0..n {
    for j in (i + 1)..n {
      pairs = pairs.push((i, j, dist(points[i], points[j])))
    }
  }
  return sort.sorted(pairs, (a, b) => a.2 < b.2)
}

func main() {
  if os.args.len() != 3 {
    fmt.println("usage: d08 <input> <K>")
    os.exit(2)
  }
  match readData(os.args[1]) {
    Err(e)     => { fmt.println("read error:", e); os.exit(1) },
    Ok(points) => {
      match strconv.atoi(os.args[2]) {
        Err(e) => { fmt.println("bad K:", e); os.exit(2) },
        Ok(k)  => {
          let pairs = buildPairs(points)
          fmt.println("Simple:", solveSimple(points.len(), pairs, k))
          match solveHard(points, pairs) {
            Ok(v)  => fmt.println("Hard:  ", v),
            Err(e) => fmt.println("hard error:", e),
          }
        },
      }
    },
  }
}
