// Showcase: uncolored concurrency (D7) — any function may block, and the
// Go scheduler handles it. A structured-concurrency `scope` joins
// spawned tasks at its end; the first failure cancels its siblings
// (mapping onto errgroup + context under the hood, per
// docs/architecture.md §4).
//
// The program fetches a list of URLs in parallel with a per-request
// timeout and prints either the page sizes or the first error.

import context
import fmt
import http
import io
import os
import time

type Page = {
  url:  string
  size: int
}

// `scope.context` is the cancellable context of the surrounding
// concurrency scope (lexically bound). Each fetch attaches its
// own deadline derived from that context.
func fetch(parent: context.Context, url: string, timeout: time.Duration):
    Result<Page, error> {
  let (ctx, cancel) = context.withTimeout(parent, timeout)
  defer cancel()

  let req  = try http.newRequest("GET", url, None)
  let resp = try http.do(req.withContext(ctx))
  defer resp.body.close()

  let body = try io.readAll(resp.body)
  return Ok(Page{ url: url, size: body.len() })
}

// Under the unified scope model (Result<T, E> with trailing-expr-after-
// join), per-spawn results flow back through a channel that the scope's
// trailing expression drains.
func fetchAll(urls: []string, timeout: time.Duration): Result<[]Page, error> {
  return scope<[]Page, error> {
    // Buffer size = number of spawns so a successful sibling never
    // blocks on `collect.send(p)` even if the scope short-circuits on
    // another spawn's Err and the trailing-expression drain never
    // runs. Don't shrink without revisiting deadlock.
    let collect = makeChannel<Page>(urls.len())
    for u in urls {
      spawn {
        let p = try fetch(scope.context, u, timeout)
        collect.send(p)
        return Ok(())
      }
    }
    // Trailing expression — runs after every spawn has joined.
    collect.close()
    var out: []Page = []Page{}
    for p in collect { out = out.push(p) }
    out
  }
}

func main() {
  let urls = [
    "https://example.com/",
    "https://example.org/",
    "https://example.net/",
  ]

  match fetchAll(urls, time.seconds(2)) {
    Ok(pages) => {
      for p in pages {
        fmt.println(p.url, "->", p.size, "bytes")
      }
    },
    Err(e) => {
      fmt.println("fetch failed:", e)
      os.exit(1)
    },
  }
}
