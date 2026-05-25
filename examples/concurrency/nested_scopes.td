// Nested structured-concurrency scopes. An outer scope launches one
// inner scope per region; each inner scope fans out per item. A failure
// inside an inner scope cancels its siblings; the outer scope cancels
// its remaining inner scopes.
//
// Forces: scope nesting, propagation of cancellation through
// context.Context, callees that take an explicit context.Context
// parameter (scope.context is lexically bound — see language-spec.md
// §Concurrency · Structured concurrency).

import context
import fmt
import time

type Region = {
  name:  string
  items: []string
}

// Pretend "work". Returns Err for the magic name "bad" so we can see
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

// Inner scope: one region's items, parallel. First Err here cancels
// siblings; the result bubbles up to the caller.
func fetchRegion(ctx: context.Context, region: Region):
    Result<[]string, error> {
  return scope<string, error> {
    for item in region.items {
      spawn {
        // `ctx` is the parent's context (e.g. the outer scope's
        // scope.context); we'd combine it with scope.context if we
        // wanted the inner scope to honour both — for the demo, scope
        // is enough.
        return fetch(scope.context, region.name, item)
      }
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
