// Advent of Code 2025, Day 11 — https://adventofcode.com/2025/day/11
//
// Input is an adjacency list — each line `node: neighbour neighbour …`.
// Count paths from a named source to a named destination by recursive
// DFS with memoisation. Hard part: combine path counts among four
// named landmarks (`you/out`, `fft/dac`, `svr`, … per puzzle) under
// the assumption the graph has no cycles in the relevant direction.
//
// Forces: `Map<string, []string>` (adjacency), `Map<string, int>`
// (memoisation cache), `Set<string>` for cycle detection,
// recursion, string splitting.

import fmt
import os
import strings

type Graph = Map<string, []string>

func readData(path: string): Result<Graph, error> {
  let data = try os.readFile(path)
  let text = strings.fromBytes(data)
  var adj: Graph = Map<string, []string>{}
  for ln in strings.split(text, "\n") {
    let s = strings.trimSpace(ln)
    if s.len() == 0 { continue }
    let items = strings.fields(s)
    let first = items[0]
    if !strings.hasSuffix(first, ":") {
      return Err(error("bad line, expected `node:` prefix: " + ln))
    }
    let node = first[0:(first.len() - 1)]
    var neighbours: []string = []string{}
    for i in 1..items.len() {
      neighbours = neighbours.push(items[i])
    }
    adj.set(node, neighbours)
  }
  return Ok(adj)
}

// DFS with memoisation. The cycle-check via `visited` lets us flag
// infinite loops as `Err` instead of stack-overflowing.
func countPaths(adj: Graph, src: string, dest: string): Result<int, error> {
  var cache: Map<string, int> = Map<string, int>{}
  cache.set(dest, 1)
  var visited: Set<string> = Set<string>{}

  return dfs(adj, src, cache, visited)
}

func dfs(adj: Graph, v: string, cache: Map<string, int>, visited: Set<string>):
    Result<int, error> {
  match cache.get(v) {
    Some(n) => return Ok(n),
    None    => {},
  }
  if visited.has(v) {
    return Err(error("cycle detected at node: " + v))
  }
  visited.add(v)

  var total = 0
  let neighbours = match adj.get(v) {
    Some(ns) => ns,
    None     => []string{},
  }
  for u in neighbours {
    total = total + try dfs(adj, u, cache, visited)
  }
  cache.set(v, total)
  // Roll back `visited` so a node reached again through a different
  // path isn't mis-flagged as a cycle. Once cached, the `cache.get`
  // short-circuit above answers without re-entering the `visited`
  // check at all — so the roll-back is observationally equivalent to
  // Python's persistent `visited` for any DAG input.
  visited.delete(v)
  return Ok(total)
}

func solveSimple(adj: Graph): Result<int, error> {
  return countPaths(adj, "you", "out")
}

func solveHard(adj: Graph): Result<int, error> {
  let n12 = try countPaths(adj, "fft", "dac")
  let n21 = try countPaths(adj, "dac", "fft")
  if n12 != 0 && n21 != 0 {
    return Err(error("infinite loop between fft and dac"))
  }
  if n12 == 0 {
    let a = try countPaths(adj, "svr", "dac")
    let b = try countPaths(adj, "fft", "out")
    return Ok(a * n21 * b)
  }
  let a = try countPaths(adj, "svr", "fft")
  let b = try countPaths(adj, "dac", "out")
  return Ok(a * n12 * b)
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: d11 <input>")
    os.exit(2)
  }
  match readData(os.args[1]) {
    Err(e)  => { fmt.println("read error:", e); os.exit(1) },
    Ok(adj) => {
      match solveSimple(adj) {
        Ok(n)  => fmt.println("Simple:", n),
        Err(e) => fmt.println("simple error:", e),
      }
      match solveHard(adj) {
        Ok(n)  => fmt.println("Hard:  ", n),
        Err(e) => fmt.println("hard error:", e),
      }
    },
  }
}
