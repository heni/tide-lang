// Pub/sub broadcaster. One publisher, many subscribers. Subscribers
// can join and leave dynamically; on broker shutdown every subscriber
// channel is closed cleanly.
//
// Forces: a class managing per-subscriber channels under a mutex,
// `select` with default for non-blocking delivery (drop-on-overflow
// instead of blocking the publisher), structured cleanup on cancel.

import fmt
import sync
import time

class Broker<T> {
  var subs: []SendChan<T>
  let lock: sync.Mutex

  static new(): Broker<T> {
    return Broker<T>{ subs: []SendChan<T>{}, lock: sync.Mutex{} }
  }

  subscribe(buf: int): RecvChan<T> {
    lock.lock()
    defer lock.unlock()

    let ch = makeChannel<T>(buf)
    subs = subs.push(ch)
    return ch
  }

  // Non-blocking publish. A slow subscriber drops the message rather
  // than back-pressuring the publisher — the trade-off most pub/sub
  // designs make.
  publish(v: T) {
    lock.lock()
    defer lock.unlock()

    for ch in subs {
      select {
        case ch.send(v) => {},                        // delivered
        default         => {},                        // dropped — subscriber slow
      }
    }
  }

  // Close every subscriber channel; further publish() calls are no-ops
  // (sending on a closed channel would panic — but subs is cleared).
  shutdown() {
    lock.lock()
    defer lock.unlock()

    for ch in subs {
      ch.close()
    }
    subs = []SendChan<T>{}
  }
}

func main() {
  let broker = Broker<int>.new()

  let alice = broker.subscribe(8)
  let bob   = broker.subscribe(8)

  match scope<unit, error> {
    spawn {
      var total = 0
      for v in alice { total = total + v }
      fmt.println("alice received total:", total)
      return Ok(())
    }
    spawn {
      var count = 0
      for v in bob { count = count + 1 }
      fmt.println("bob received count:", count)
      return Ok(())
    }
    spawn {
      // Publish 10 events; slight delay so subscribers actually run.
      for i in 1..=10 {
        broker.publish(i)
        time.sleep(time.milliseconds(5))
      }
      broker.shutdown()
      return Ok(())
    }
  } {
    Ok(_)  => {},
    Err(e) => fmt.println("broker error:", e),
  }
}
