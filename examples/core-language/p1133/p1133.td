// Timus 1133 — Fibonacci Sequence (big-int linear equation, Fn over Z)
// http://acm.timus.ru/problem.aspx?space=1&num=1133
//
// Given F(i), F(j), and n (with i, j, n possibly far apart), find F(n)
// in a sequence satisfying the Fibonacci recurrence. Since F(k) =
// a*F(k-1) + b*F(0) for some integer a,b depending only on k, and
// using the Fibonacci coefficients (a_k, b_k) we can solve the 2x2
// linear system for (F(0), F(1)) and then compose forward to F(n).
//
// Forces: `big.BigInt` from the new `math/big` binding row — the
// fibonacci coefficients overflow int64 well before the puzzle's
// upper bound. Arithmetic uses the functional surface
// (`a.add(b)` returning a fresh BigInt) instead of Go's pointer-
// mutation `a.Add(x, y)`. Also exercises `fmt.scan5<...>()`-like
// multi-token reads by chaining single scans.

import big
import fmt
import os

func minInt(a: int, b: int): int {
  if a < b { return a }
  return b
}

// Returns the pair (F(k), F(k+1)) of the classical Fibonacci
// sequence (F(0)=0, F(1)=1). The two BigInts also serve as the
// `(a_k, b_k)` coefficients in the linear combination of any
// generalised Fibonacci-recurrent sequence:
//   G(k) = a_k * G(0) + b_k * G(1)
// where (a_k, b_k) = (F(k-1), F(k)). We use that fact in `solve`
// below to set up the 2x2 Cramer's system.
func fibCoeff(k: int): (big.BigInt, big.BigInt) {
  var a = big.fromInt(0)            // F(0)
  var b = big.fromInt(1)            // F(1)
  for _ in 0..k {
    let nextB = a.add(b)
    a = b
    b = nextB
  }
  return (a, b)
}

// Solve {a1*x + b1*y = c1, a2*x + b2*y = c2} for integer (x, y) via
// Cramer's rule. Assumes a non-singular system.
func solveLinear(
  a1: big.BigInt, b1: big.BigInt, c1: big.BigInt,
  a2: big.BigInt, b2: big.BigInt, c2: big.BigInt,
): (int64, int64) {
  let det  = a1.mul(b2).sub(a2.mul(b1))
  let detY = a1.mul(c2).sub(a2.mul(c1))
  let detX = c1.mul(b2).sub(c2.mul(b1))
  return (detX.div(det).toInt64(), detY.div(det).toInt64())
}

func solve(i: int, fi: int64, j: int, fj: int64, n: int): int64 {
  let base = minInt(minInt(i, j), n)
  let (aI, bI) = fibCoeff(i - base)
  let (aJ, bJ) = fibCoeff(j - base)
  let (x, y)   = solveLinear(aI, bI, big.fromInt64(fi),
                              aJ, bJ, big.fromInt64(fj))
  let (aN, bN) = fibCoeff(n - base)
  return aN.toInt64() * x + bN.toInt64() * y
}

// One place that demonstrates the diverging-built-in rule
// (`os.exit` unifies with any expected type), so the call sites
// below stay clean.
func readInt(): int {
  return match fmt.scan<int>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(v)  => v,
  }
}

func readInt64(): int64 {
  return match fmt.scan<int64>() {
    Err(e) => { fmt.println("read error:", e); os.exit(1) },
    Ok(v)  => v,
  }
}

func main() {
  let i  = readInt()
  let fi = readInt64()
  let j  = readInt()
  let fj = readInt64()
  let n  = readInt()
  fmt.println(solve(i, fi, j, fj, n))
}
