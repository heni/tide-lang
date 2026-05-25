// Nested structured-concurrency scopes with real cancellation
// propagation. An outer scope launches one inner scope per region; each
// inner scope fans out per item. A failure inside an inner scope
// cancels its siblings *and* the outer scope's other regions, because
// the inner `scope<T, E>(parent)` form derives its context from the
// outer scope's context.
//
// Forces: scope nesting, the explicit-parent scope form
// `scope<T, E>(parentCtx) { ... }` (G44), and callees that take an
// explicit context.Context parameter — `scope.context` is lexically
// bound (G40) and does not flow into callees.

import context
import fmt
import time

type Region = {
  name:  string
  items: []string
}

// "Work". Returns Err for the magic name "bad" so we can see
// cancellation propagate.
func fetch(ctx: context.Context, region: string, item: string):
    Result<string, error> {
  // Honour cancellation early.
  select {
    case <-ctx.done() => return Err(error("cancelled: " + region + "/" + item)),
    default           => {},
  }

  time.sleep(time.milliseconds(20))

  if item == "bad" {
    return Err(error("intentional failure: " + region + "/" + item))
  }
  return Ok(region + "/" + item)
}

// Inner scope: fan out one fetch per item. The scope's context derives
// from `ctx` (the outer scope's context) — when the outer cancels,
// every in-flight `fetch` sees its ctx.done() close.
func fetchRegion(ctx: context.Context, region: Region):
    Result<[]string, error> {
  return scope<string, error>(ctx) {
    for item in region.items {
      spawn { return fetch(scope.context, region.name, item) }
    }
  }
}

func main() {
  let regions = [
    Region{ name: "eu",   items: ["a", "b", "c"] },
    Region{ name: "us",   items: ["x", "bad", "z"] },        // will fail
    Region{ name: "asia", items: ["p", "q"] },
  ]

  let result = scope<[]string, error> {
    for r in regions {
      spawn { return fetchRegion(scope.context, r) }
    }
  }

  match result {
    Ok(perRegion) => {
      for items in perRegion {
        for x in items {
          fmt.println("got", x)
        }
      }
    },
    Err(e) => fmt.println("failed:", e),
  }
}
