// Worker pool — N goroutines read from a shared job channel and write
// to a shared result channel. Bounded parallelism: at most N tasks run
// concurrently regardless of the queue length.
//
// Forces: fan-out (one job channel, many readers), fan-in (one result
// channel, many writers), explicit close coordination, scope joining all
// workers, `RecvChan<T>` / `SendChan<T>` directionality at the worker
// signature.

import fmt
import time

type Job = {
  id:    int
  workN: int       // pretend CPU work — square this many times
}

type Result = {
  jobID:  int
  output: int
}

func worker(id: int, jobs: RecvChan<Job>, results: SendChan<Result>) {
  for j in jobs {
    // Simulate work.
    time.sleep(time.milliseconds(50))
    var v = j.workN
    var k = 0
    while k < j.workN {
      v = v * 2
      k = k + 1
    }
    results.send(Result{ jobID: j.id, output: v })
  }
}

func runPool(workerCount: int, jobs: []Job): Result<[]Result, error> {
  let jobCh    = makeChannel<Job>(workerCount)
  let resultCh = makeChannel<Result>(jobs.len())

  // Fan out N workers. Fire-and-forget; the scope joins when every
  // worker exits (which happens after jobCh is closed and drained).
  try scope<unit, error> {
    for w in 1..=workerCount {
      spawn { worker(w, jobCh, resultCh); return Ok(()) }
    }

    // Feed jobs, then close to let workers drain and exit.
    spawn {
      for j in jobs {
        jobCh.send(j)
      }
      jobCh.close()
      return Ok(())
    }
  }
  resultCh.close()

  var out: []Result = []Result{}
  for r in resultCh {
    out = out.push(r)
  }
  return Ok(out)
}

func main() {
  var jobs: []Job = []Job{}
  for i in 1..=8 {
    jobs = jobs.push(Job{ id: i, workN: i })
  }
  match runPool(3, jobs) {
    Ok(results) => {
      fmt.println("completed", results.len(), "jobs")
      for r in results {
        fmt.println("  job", r.jobID, "->", r.output)
      }
    },
    Err(e) => fmt.println("pool error:", e),
  }
}
