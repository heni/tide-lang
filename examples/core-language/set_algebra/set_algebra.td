// Raku-inspired stress test #1: set algebra.
//
// Raku's [`S03-operators/set_*`](https://github.com/Raku/roast/tree/master/S03-operators)
// tests cover a small algebra of set operators (`(|)`, `(&)`, `(-)`,
// `(^)`, `(<=)`, `(<)`). Tide's `Set<T>` ships only the four
// core ops — `add`/`has`/`delete`/`len` — so the algebra has to be
// reconstructed in user code. This file does that and reports what
// the v1 surface is missing.
//
// Forces: `Set<T>` literals, iteration over `Set`, building a new
// set from two inputs, the absence of `==` on classes (sets
// are classes, so structural equality goes through a helper).

import fmt
import strconv

func union(a: Set<int>, b: Set<int>): Set<int> {
  let out = Set<int>{}
  for x in a { out.add(x) }
  for x in b { out.add(x) }
  return out
}

func intersect(a: Set<int>, b: Set<int>): Set<int> {
  let out = Set<int>{}
  for x in a {
    if b.has(x) { out.add(x) }
  }
  return out
}

func difference(a: Set<int>, b: Set<int>): Set<int> {
  let out = Set<int>{}
  for x in a {
    if !b.has(x) { out.add(x) }
  }
  return out
}

func symmetric(a: Set<int>, b: Set<int>): Set<int> {
  return union(difference(a, b), difference(b, a))
}

// Structural set equality. `==` on `Set<int>` would compare references
// (classes use refEq). For value equality we walk both sides.
func setEq(a: Set<int>, b: Set<int>): bool {
  if a.len() != b.len() { return false }
  for x in a {
    if !b.has(x) { return false }
  }
  return true
}

func isSubset(a: Set<int>, b: Set<int>): bool {
  for x in a {
    if !b.has(x) { return false }
  }
  return true
}

func render(s: Set<int>): string {
  // Order is unspecified for Set iteration; for the demo we
  // emit elements in whatever order Set hands us.
  var out = "{"
  var first = true
  for x in s {
    if !first { out = out + ", " }
    out = out + strconv.itoa(x)
    first = false
  }
  return out + "}"
}

func main() {
  let a = Set<int>{ 1, 2, 3, 4 }
  let b = Set<int>{ 3, 4, 5, 6 }

  fmt.println("a          :", render(a))
  fmt.println("b          :", render(b))
  fmt.println("a ∪ b      :", render(union(a, b)))
  fmt.println("a ∩ b      :", render(intersect(a, b)))
  fmt.println("a - b      :", render(difference(a, b)))
  fmt.println("a △ b      :", render(symmetric(a, b)))
  fmt.println("{1,2} ⊆ a  :", isSubset(Set<int>{ 1, 2 }, a))
  fmt.println("a == a     :", setEq(a, a))
  fmt.println("a == b     :", setEq(a, b))
}
