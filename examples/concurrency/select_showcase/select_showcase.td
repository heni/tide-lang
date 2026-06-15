// Every `select` case form in one program: receive-into-binding,
// receive-and-drop, send, default (non-blocking), and timeout via
// time.after. The "right" way to wait on multiple channels with a
// deadline, without leaking a goroutine.
//
// Forces: all `select` shapes, time.after as a timeout idiom,
// mixed send+receive cases.

import fmt
import time

func main() {
  let inbox   = makeChannel<string>(4)
  let outbox  = makeChannel<int>(4)
  let stop    = makeChannel<unit>(1)
  let timeout = time.after(time.milliseconds(300))

  // Produce some inbox traffic.
  match scope<unit, error> {
    spawn {
      for s in ["hello", "world", "tide", "rising"] {
        inbox.send(s)
        time.sleep(time.milliseconds(40))
      }
      return Ok(())
    }

    // Drain outbox so the "send" case can succeed; track count.
    spawn {
      var n = 0
      for _ in outbox {
        n = n + 1
      }
      fmt.println("outbox received:", n)
      return Ok(())
    }

    spawn {
      var ticks = 0
      var done  = false
      while !done {
        select {
          // 1. Receive-into-binding.
          case s = <-inbox => {
            fmt.println("inbox:", s)
          },

          // 2. Receive-and-drop (we only care that stop fired).
          case <-stop => {
            fmt.println("stop signal — leaving select")
            done = true
          },

          // 3. Send (only proceeds if outbox has room).
          case outbox.send(ticks * 10) => {
            ticks = ticks + 1
          },

          // 4. Timeout — one-shot via time.after.
          case <-timeout => {
            fmt.println("timed out after 300ms")
            done = true
          },

          // 5. Default — non-blocking: take the first chance, else
          // fall through. We don't ship a `default` arm here because
          // we *do* want to wait on the four cases above; default
          // would burn CPU.
        }
      }
      outbox.close()
      return Ok(())
    }

    // Demo a manual stop after 150ms.
    spawn {
      time.sleep(time.milliseconds(150))
      stop.send(())
      return Ok(())
    }
  } {
    Ok(_)  => {},
    Err(e) => fmt.println("select error:", e),
  }
}
