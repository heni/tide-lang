// LeetCode 56. Merge Intervals.
// Given a list of `[start, end]` intervals, merge all overlapping ones.
//
// Forces: structs (records), sorting with a comparator (function value),
// slice rebuilding.

import fmt
import sort
import strconv

type Interval = {
  start: int
  end:   int
}

func merge(intervals: []Interval): []Interval {
  if intervals.len() == 0 {
    return []Interval{}
  }

  // Sort by start. The wrapper takes a "less" comparator and returns a new
  // slice — Tide preserves immutability of the input.
  let sorted = sort.sorted(intervals, (a, b) => a.start < b.start)

  var out: []Interval = []Interval{}
  var cur = sorted[0]
  for i in 1..sorted.len() {
    let nxt = sorted[i]
    if nxt.start <= cur.end {
      cur = Interval{ start: cur.start, end: max(cur.end, nxt.end) }
    } else {
      out = out.push(cur)
      cur = nxt
    }
  }
  out = out.push(cur)
  return out
}

func max(a: int, b: int): int {
  if a > b { return a }
  return b
}

func render(iv: Interval): string {
  return "[" + strconv.itoa(iv.start) + "," + strconv.itoa(iv.end) + "]"
}

func main() {
  let input = [
    Interval{ start: 1,  end: 3 },
    Interval{ start: 2,  end: 6 },
    Interval{ start: 8,  end: 10 },
    Interval{ start: 15, end: 18 },
  ]
  let merged = merge(input)
  for iv in merged {
    fmt.println(render(iv))
  }
}
