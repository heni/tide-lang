// Showcase: graceful shutdown. The server listens on :8080; an interrupt
// (SIGINT/SIGTERM) triggers a shutdown that lets in-flight requests
// complete up to a deadline, then stops the listener.
//
// Forces: net/http, os/signal, context, channels, `select`, and a
// structured scope joining the listener and the signal handler.

import context
import fmt
import http
import log
import os
import signal
import time

class Hello implements http.Handler {
  serveHTTP(w: http.ResponseWriter, r: http.Request) {
    // Simulate a slow handler so shutdown has something to wait on.
    time.sleep(time.seconds(1))
    w.writeString("hello\n")
  }
}

func main() {
  let srv = http.Server{
    addr:    ":8080",
    handler: Hello{},
  }

  let sigs = makeChannel<os.Signal>(1)
  signal.notify(sigs, signal.interrupt, signal.terminate)

  let result = scope<unit, error> {
    // The listener.
    spawn {
      log.println("listening on :8080")
      match srv.listenAndServe() {
        Err(e) => if !http.isServerClosed(e) { return Err(e) },
        Ok(_)  => {},
      }
      return Ok(())
    }

    // The signal watcher: blocks on either a signal or scope cancellation,
    // then asks the server to shut down with a 10s grace period.
    spawn {
      select {
        case s = <-sigs => {
          log.println("received", s, "— shutting down")
        },
        case <-scope.context.done() => {
          return Ok(())
        },
      }
      let (ctx, cancel) = context.withTimeout(context.background(), time.seconds(10))
      defer cancel()
      return srv.shutdown(ctx)
    }
  }

  match result {
    Ok(_)  => log.println("clean exit"),
    Err(e) => {
      log.println("error:", e)
      os.exit(1)
    },
  }
}
