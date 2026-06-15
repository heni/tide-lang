// Rate-limited dispatcher. A producer emits as fast as it can; a ticker
// bounds the consumer at one event per `interval`. Excess events are
// buffered; if the buffer is full the producer drops them (the simple
// alternative to back-pressuring the producer).
//
// Forces: time.tick (returns a RecvChan<time.Time>), select with
// `default` for non-blocking buffer pushes, time.after as a one-shot
// deadline.

import fmt
import time

class Counters {
  var produced: int
  var dropped:  int
  var emitted:  int
}

func main() {
  let deadline = time.after(time.milliseconds(500))
  let ticks    = time.tick(time.milliseconds(50))
  let buffer   = makeChannel<int>(4)
  let cnt      = Counters{ produced: 0, dropped: 0, emitted: 0 }

  match scope<unit, error> {
    // Producer: every iteration tries to enqueue a sequential number;
    // drops if the buffer is full. Exits when deadline closes.
    spawn {
      var i = 0
      var done = false
      while !done {
        i = i + 1
        select {
          case buffer.send(i)  => { cnt.produced = cnt.produced + 1 },
          case <-deadline      => { done = true },
          default              => { cnt.dropped  = cnt.dropped + 1 },
        }
      }
      return Ok(())
    }

    // Consumer: one read per tick, until deadline.
    spawn {
      var done = false
      while !done {
        select {
          case <-ticks => {
            match buffer.tryRecv() {
              Some(v) => {
                cnt.emitted = cnt.emitted + 1
                fmt.println("tick emit:", v)
              },
              None    => fmt.println("tick (buffer empty)"),
            }
          },
          case <-deadline => { done = true },
        }
      }
      return Ok(())
    }
  } {
    Ok(_)  => {},
    Err(e) => fmt.println("dispatcher error:", e),
  }

  fmt.println("---")
  fmt.println("produced:", cnt.produced, "dropped:", cnt.dropped, "emitted:", cnt.emitted)
}
