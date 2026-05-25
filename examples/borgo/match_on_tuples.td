// Port of Borgo's `match-on-tuples.brg` codegen-emit test.
// Source: https://github.com/borgo-lang/borgo/blob/main/compiler/test/snapshot/codegen-emit/match-on-tuples.exp
//
// Borgo and Tide both spell tuple-match the same way. Tide differs in
// two small places:
//   * Tide requires arity ≥ 2 for tuples — `let res = match () { () => 2 }`
//     in Borgo becomes a match on `unit`, which can't pattern-match
//     positionally (there's only one value). Replaced with a single-arm
//     `match` on the `()` literal.
//   * The `@unreachable()` builtin maps to `panic("unreachable")` in
//     Tide; the panic carries a `.td` source location via D8.

import fmt

func main() {
  let res = match (1, "foo") {
    (3, _)        => 5,
    (1, "bar")    => 6,
    (x, "foo")    => x,
    _             => panic("unreachable"),
  }
  fmt.println(res)                                    // 1

  // The Borgo `match ()` case — unit-typed match with one arm.
  let res2 = match () {
    () => 2,
  }
  fmt.println(res2)                                   // 2
}
