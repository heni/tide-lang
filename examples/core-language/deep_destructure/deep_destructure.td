// Raku-inspired stress test #2: deep pattern destructuring.
//
// Raku's
// [`S05-pattern-matching/`](https://github.com/Raku/roast/tree/master/S05-grammar)
// and `S07-iterators-and-laziness` test suites hammer pattern
// destructuring at unusual depth: variants of records of tuples of
// variants. Tide's `match` was designed to handle this
// without growing new syntax — the stress test is whether the spec
// composes the existing rules cleanly.
//
// The shape: a workflow `Step` is a sum type whose payload is a
// record that itself contains a tuple plus another sum-typed field.
// `summarise` extracts the load-bearing data in **one** match arm
// per outer variant, leaning on inner tuple- and sum-patterns.

import fmt
import strconv

type Direction = | North | South | East | West
type Coord     = (int, int)

type StepInfo = {
  at:        Coord
  heading:   Direction
  remaining: int
}

type Step =
  | Idle
  | Move(info: StepInfo)
  | Pause(reason: string)
  | Abort(info: StepInfo, cause: string)

func dirName(d: Direction): string {
  return match d {
    North => "N",
    South => "S",
    East  => "E",
    West  => "W",
  }
}

func summarise(s: Step): string {
  return match s {
    Idle =>
      "idle",

    // Variant payload is one record; destructure that record by
    // field name. Each field is bound via `field: binding` (record
    // punning is intentionally omitted in v1 — `at: (x, y)` is
    // the explicit form, never `at` alone).
    Move(StepInfo{ at: (x, y), heading: dir, remaining: rest }) =>
      ("move " + dirName(dir)
        + " from (" + strconv.itoa(x) + "," + strconv.itoa(y) + ")"
        + ", " + strconv.itoa(rest) + " left"),

    Pause(why) =>
      "paused: " + why,

    // Variant with two payload positions: a record and a string.
    // Destructure both; inside the record destructure the tuple,
    // discarding fields we don't need.
    Abort(StepInfo{ at: (x, y), heading: _, remaining: _ }, cause) =>
      "abort at (" + strconv.itoa(x) + "," + strconv.itoa(y) + "): " + cause,
  }
}

func main() {
  let steps = [
    Idle,
    Move(StepInfo{ at: (3, 4), heading: North, remaining: 5 }),
    Pause("waiting for sensor"),
    Abort(StepInfo{ at: (7, 2), heading: West, remaining: 0 }, "obstacle"),
  ]
  for s in steps {
    fmt.println(summarise(s))
  }
}
