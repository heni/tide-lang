// Advent of Code 2025, Day 5 — https://adventofcode.com/2025/day/5
//
// Two sections in one file separated by a blank line:
//   * "fresh DB" — half-open ranges `start-end` of fresh products;
//   * "store"    — individual product IDs.
// Sort the DB, merge overlapping intervals, then count how many store
// items fall in *no* fresh range. Print the merged-DB size, the count
// of fresh-in-stock items, and the total count of fresh IDs covered.
//
// Forces: a two-section parser, sorting tuples by first component
// (G24), merging sorted intervals (the same shape as merge_intervals
// from leetcode/), iteration with index over a sorted slice.

import fmt
import os
import sort
import strconv
import strings

type Range = (int, int)

func readData(path: string): Result<([]Range, []int), error> {
  let data = try os.readFile(path)
  let text = strings.fromBytes(data)
  var freshDB: []Range = []Range{}
  var store:   []int   = []int{}
  var inFresh = true
  for ln in strings.split(text, "\n") {
    let s = strings.trimSpace(ln)
    if s.len() == 0 {
      inFresh = false
      continue
    }
    if inFresh {
      let parts = strings.split(s, "-")
      let st  = try strconv.atoi(parts[0])
      let fin = try strconv.atoi(parts[1])
      freshDB = freshDB.push((st, fin))
    } else {
      let n = try strconv.atoi(s)
      store = store.push(n)
    }
  }
  return Ok((freshDB, store))
}

func collapse(ranges: []Range): []Range {
  if ranges.len() == 0 { return ranges }
  let sorted = sort.sorted(ranges, (a, b) => a.0 < b.0)
  var out: []Range = []Range{}
  var cur = sorted[0]
  for i in 1..sorted.len() {
    let r = sorted[i]
    if cur.1 >= r.0 {
      let merged = if cur.1 > r.1 { cur.1 } else { r.1 }
      cur = (cur.0, merged)
    } else {
      out = out.push(cur)
      cur = r
    }
  }
  out = out.push(cur)
  return out
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: d05 <input>")
    os.exit(2)
  }
  match readData(os.args[1]) {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(pair) => {
      let (raw, storeUnsorted) = pair
      let freshDB = collapse(raw)
      let store   = sort.sorted(storeUnsorted, (a, b) => a < b)

      fmt.println("Fresh DB size:", raw.len(), "=>", freshDB.len())

      var i = 0
      var spoiled = 0
      for product in store {
        while i < freshDB.len() && product > freshDB[i].1 {
          i = i + 1
        }
        let isFresh = i < freshDB.len() && product >= freshDB[i].0
        if !isFresh { spoiled = spoiled + 1 }
      }
      fmt.println("Available Fresh:", store.len() - spoiled)

      var total = 0
      for r in freshDB {
        total = total + r.1 - r.0 + 1
      }
      fmt.println("Total Fresh:", total)
    },
  }
}
