// A small JSON REST API: GET /todos, POST /todos, GET /todos/{id},
// DELETE /todos/{id}. State lives in memory.
//
// Forces: DTO records, `encoding/json` round-trip, `net/http` routing,
// `Result<T, ApiError>` mapped to HTTP status codes (200, 201, 400, 404,
// 500), `defer` for lock release, and a class that satisfies
// `http.Handler` via the binding (D14).

import fmt
import http
import io
import json
import log
import strconv
import strings
import sync
// url.URL is reached transitively through http.Request.url; not imported by name.

type Todo = {
  id:    int
  title: string
  done:  bool
}

type CreateTodo = {
  title: string
}

// Custom error type: a class that implements the built-in `error`
// interface. Carries both a public message and the HTTP status to use.
class ApiError implements error {
  let status:  int
  let message: string

  error(): string { return message }
}

func badRequest(msg: string):    ApiError { return ApiError{ status: 400, message: msg } }
func notFound(msg: string):      ApiError { return ApiError{ status: 404, message: msg } }
func internalError(msg: string): ApiError { return ApiError{ status: 500, message: msg } }

class Store {
  var nextID: int
  var items:  Map<int, Todo>
  let lock:   sync.Mutex

  static new(): Store {
    return Store{
      nextID: 1,
      items:  Map<int, Todo>{},
      lock:   sync.Mutex{},
    }
  }

  list(): []Todo {
    lock.lock()
    defer lock.unlock()

    var out: []Todo = []Todo{}
    for (_, t) in items {
      out = out.push(t)
    }
    return out
  }

  get(id: int): Result<Todo, ApiError> {
    lock.lock()
    defer lock.unlock()

    match items.get(id) {
      Some(t) => Ok(t),
      None    => Err(notFound("todo not found")),
    }
  }

  create(title: string): Todo {
    lock.lock()
    defer lock.unlock()

    let t = Todo{ id: nextID, title: title, done: false }
    items.set(nextID, t)
    nextID = nextID + 1
    return t
  }

  delete(id: int): Result<unit, ApiError> {
    lock.lock()
    defer lock.unlock()

    if !items.has(id) {
      return Err(notFound("todo not found"))
    }
    items.delete(id)
    return Ok(())
  }
}

class TodosHandler implements http.Handler {
  let store: Store

  serveHTTP(w: http.ResponseWriter, r: http.Request) {
    match route(r) {
      Ok(payload) => writeJSON(w, 200, payload),
      Err(err)    => writeJSON(w, err.status, err),
    }
  }

  // Path is "/todos" or "/todos/{id}".
  route(r: http.Request): Result<Any, ApiError> {
    let parts = strings.split(r.url.path, "/")
    if parts.len() < 2 || parts[1] != "todos" {
      return Err(notFound("not found"))
    }

    if parts.len() == 2 {
      // /todos collection
      match r.method {
        "GET" => {
          return Ok(store.list())
        },
        "POST" => {
          let body = try readBody(r)
          let req  = try parseJSON<CreateTodo>(body)
          if strings.trimSpace(req.title) == "" {
            return Err(badRequest("title must be non-empty"))
          }
          return Ok(store.create(req.title))
        },
        _ => return Err(badRequest("method not allowed")),
      }
    }

    // /todos/{id}
    let id = try parsePathID(parts[2])
    match r.method {
      "GET" => {
        let t = try store.get(id)
        return Ok(t)
      },
      "DELETE" => {
        try store.delete(id)
        return Ok(())
      },
      _ => return Err(badRequest("method not allowed")),
    }
  }
}

func readBody(r: http.Request): Result<[]byte, ApiError> {
  match io.readAll(r.body) {
    Ok(b)  => Ok(b),
    Err(e) => Err(internalError("read body: " + e.error())),
  }
}

func parseJSON<T>(data: []byte): Result<T, ApiError> {
  match json.parse<T>(data) {
    Ok(v)  => Ok(v),
    Err(e) => Err(badRequest("invalid json: " + e.error())),
  }
}

func parsePathID(s: string): Result<int, ApiError> {
  match strconv.atoi(s) {
    Ok(n)  => Ok(n),
    Err(_) => Err(badRequest("bad id: " + s)),
  }
}

func writeJSON(w: http.ResponseWriter, status: int, payload: Any) {
  w.header().set("Content-Type", "application/json")
  w.writeHeader(status)
  match json.serialize(payload) {
    Ok(data) => w.write(data),
    Err(e)   => log.println("serialize error:", e),
  }
}

func main() {
  let store = Store.new()
  let h     = TodosHandler{ store: store }
  log.println("listening on :8080")
  match http.listenAndServe(":8080", h) {
    Ok(_)  => {},
    Err(e) => log.fatal(e),
  }
}
