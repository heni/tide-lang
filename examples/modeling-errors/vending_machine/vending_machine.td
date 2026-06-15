// Showcase: a state machine modelled as a sum type, driven by exhaustive
// `match`. The compiler refuses to compile a `match` that forgets a state
// or a transition — the "cannot reach Sold from Idle without paying"
// invariant is enforced by the type system, not by tests.

import fmt
import strconv

type Item = | Cola | Chips | Candy

func price(i: Item): int {
  match i {
    Cola  => 150,
    Chips => 100,
    Candy =>  50,
  }
}

// Events the machine can receive.
type Event =
  | InsertCoin(amount: int)
  | Select(item: Item)
  | Refund
  | Restock

// States the machine can be in.
type State =
  | Idle
  | Holding(balance: int)
  | Dispensing(item: Item, change: int)
  | OutOfStock

func step(s: State, e: Event): State {
  match (s, e) {
    // Idle: accepts coins or restock; ignores selection/refund.
    (Idle, InsertCoin(n))       => Holding(n),
    (Idle, Select(_))           => Idle,
    (Idle, Refund)              => Idle,
    (Idle, Restock)             => Idle,

    // Holding: accepts more coins, a selection, or a refund.
    (Holding(b), InsertCoin(n)) => Holding(b + n),
    (Holding(b), Select(i))     => if b >= price(i) {
                                     Dispensing(i, b - price(i))
                                   } else {
                                     Holding(b)
                                   },
    (Holding(_), Refund)        => Idle,
    (Holding(_), Restock)       => Idle,

    // Dispensing: returns to Idle after one tick (modelled by any event).
    (Dispensing(_, _), _)       => Idle,

    // OutOfStock: only Restock leaves this state.
    (OutOfStock, Restock)       => Idle,
    (OutOfStock, _)             => OutOfStock,
  }
}

func render(s: State): string {
  match s {
    Idle                  => "Idle",
    Holding(b)            => "Holding " + intStr(b),
    Dispensing(i, change) => "Dispensing " + itemName(i) + " (change " + intStr(change) + ")",
    OutOfStock            => "OutOfStock",
  }
}

func itemName(i: Item): string {
  match i {
    Cola  => "Cola",
    Chips => "Chips",
    Candy => "Candy",
  }
}

func intStr(n: int): string { return strconv.itoa(n) }

func main() {
  let events = [
    InsertCoin(50),
    InsertCoin(100),
    Select(Cola),       // 150 >= 150 → Dispensing
    Restock,            // back to Idle
    Select(Chips),      // ignored from Idle
    InsertCoin(200),
    Refund,
  ]
  var s: State = Idle
  fmt.println("start:", render(s))
  for e in events {
    s = step(s, e)
    fmt.println("->", render(s))
  }
}
