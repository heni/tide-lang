// Port of Borgo's `interfaces.brg` codegen-emit test.
// Source: https://github.com/borgo-lang/borgo/blob/main/compiler/test/snapshot/codegen-emit/interfaces.exp
//
// Both languages use an explicit `implements`/`impl`-style relation
// (D14 — no implicit / accidental satisfaction). Tide pairs the
// interface and the implementing class on the class declaration
// (`class Bar implements Foo`); Borgo separates the data definition
// (`struct Bar`) from the impl block (`impl (b: Bar) { fn foo() }`).
// Functionally equivalent; Tide's grouping reads tighter for the
// common one-class-one-interface case.

import fmt

interface Foo {
  foo(): int
}

class Bar implements Foo {
  let x: int
  foo(): int { return x }
}

// Composite interface — Tide spells inheritance with `extends` instead
// of Borgo's `impl Foo` row. The empty body is a deliberate mirror of
// Borgo's snapshot, which uses a single-parent empty composite to test
// the form; in real Tide code the empty `extends Foo { }` is just a
// renamed alias and would normally carry additional methods.
interface Composite extends Foo { }

func baz(f: Foo): int {
  return f.foo() + 4
}

func checkComposite(c: Composite) {
  let _ = c.foo()
}

func main() {
  let b = Bar{ x: 6 }
  fmt.println(baz(b))                                 // 10
}
