// Timus 1423 — String Tale (KMP prefix function + circular search)
// http://acm.timus.ru/problem.aspx?space=1&num=1423
//
// Given pattern `a` of length n and a cyclic string `b` of length n,
// find the rotation offset where `a` first appears in `b+b` (treating
// `b` as cyclic). Output 0 if `a == b`, the offset otherwise, or no
// match if `a` never appears.
//
// Forces: a `Stack`-free state machine — the KMP prefix function `pi`
// is a flat `makeSlice<int>(n)` table mutated by index-write; the
// inner `nextState` walks `pi` via `pi[st]` chains. Stresses
// byte-indexed string access (G45) and Go-style integer arithmetic
// on small ints.

import fmt
import os

func nextState(s: []byte, pi: []int, st0: int, b: byte): int {
  var st = st0
  while s[st] != b && st > 0 {
    st = pi[st]
  }
  if s[st] == b {
    st = st + 1
  }
  return st
}

func calcPrefixFn(s: []byte): []int {
  let n  = s.len()
  let pi = makeSlice<int>(n)
  var i  = 2
  while i < n {
    pi[i] = nextState(s, pi, pi[i - 1], s[i - 1])
    i = i + 1
  }
  return pi
}

// Find first occurrence of `a` in `b`, or -1.
func fastFind(a: []byte, b: []byte): int {
  let pi = calcPrefixFn(a)
  var st = 0
  for (i, c) in b {
    st = nextState(a, pi, st, c)
    if st == a.len() {
      return i + 1 - st
    }
  }
  return -1
}

func main() {
  let n = match fmt.scan<int>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(x)  => x,
  }
  let a = match fmt.scan<string>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(s)  => s,
  }
  let b = match fmt.scan<string>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(s)  => s,
  }
  if a.len() != n || b.len() != n {
    fmt.println("input lengths do not match n")
    os.exit(1)
  }
  // Build b + b and search for a in it.
  let doubled = b + b
  fmt.println(fastFind(a.bytes(), doubled.bytes()))
}
