// Timus 1242 — The Werewolves (DFS over directed family graph)
// http://acm.timus.ru/problem.aspx?space=1&num=1242
//
// `nPersons` people; some have parent/child links. For each victim,
// every ancestor *and* every descendant is exonerated (cannot be a
// werewolf). Report the remaining suspects.
//
// Forces: `Map<int, []int>` adjacency (the canonical "graph"
// representation), recursive DFS over an explicit `Map<int, []int>`
// argument, parallel `visited: []bool` flags, the BLOOD-terminator
// parsing pattern (read pairs until a sentinel line).
//
// Input format:
//   N
//   u v          (children edges, until line == "BLOOD")
//   ...
//   BLOOD
//   victim_1     (1-indexed; eof terminates)
//   victim_2
//   ...

import fmt
import os
import strconv
import strings

type Graph = {
  children: Map<int, []int>
  parents:  Map<int, []int>
}

func addLink(adj: Map<int, []int>, u: int, v: int) {
  match adj.get(u) {
    Some(lst) => adj.set(u, lst.push(v)),
    None      => adj.set(u, [v]),
  }
}

func readEdges(): Result<(int, []int, []int, []int), error> {
  let n = try fmt.scan<int>()
  var us: []int = []int{}
  var vs: []int = []int{}
  while true {
    let token = try fmt.scan<string>()
    if token == "BLOOD" { break }
    // First token of an edge line; second token is another scan.
    let u = try strconv.atoi(token)
    let v = try fmt.scan<int>()
    us = us.push(u - 1)
    vs = vs.push(v - 1)
  }
  var victims: []int = []int{}
  while true {
    match fmt.scan<int>() {
      Err(_) => break,                         // EOF
      Ok(u)  => victims = victims.push(u - 1),
    }
  }
  return Ok((n, us, vs, victims))
}

func buildGraph(us: []int, vs: []int): Graph {
  let g = Graph{ children: Map<int, []int>{}, parents: Map<int, []int>{} }
  for (i, u) in us {
    let v = vs[i]
    addLink(g.children, u, v)
    addLink(g.parents, v, u)
  }
  return g
}

// Recursive DFS marking reachable nodes in `visited`.
func dfs(adj: Map<int, []int>, u: int, visited: []bool) {
  visited[u] = true
  match adj.get(u) {
    Some(nbrs) => {
      for c in nbrs {
        if !visited[c] {
          dfs(adj, c, visited)
        }
      }
    },
    None => {},
  }
}

func solve(n: int, us: []int, vs: []int, victims: []int): []int {
  let g = buildGraph(us, vs)
  let visitedChild  = makeSlice<bool>(n)
  let visitedParent = makeSlice<bool>(n)
  for u in victims {
    dfs(g.children, u, visitedChild)
    dfs(g.parents,  u, visitedParent)
  }
  var suspects: []int = []int{}
  for i in 0..n {
    if !visitedChild[i] && !visitedParent[i] {
      suspects = suspects.push(i)
    }
  }
  return suspects
}

func main() {
  match readEdges() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok((n, us, vs, victims)) => {
      let suspects = solve(n, us, vs, victims)
      if suspects.len() == 0 {
        fmt.println(0)
      } else {
        var out = ""
        for u in suspects { out = out + strconv.itoa(u + 1) + " " }
        fmt.println(strings.trimSpace(out))
      }
    },
  }
}
