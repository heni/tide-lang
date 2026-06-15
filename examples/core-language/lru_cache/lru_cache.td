// Showcase: a generic class with methods, combining a map (for O(1) lookup)
// with a doubly-linked list (for O(1) eviction-order maintenance).
// Demonstrates static factory methods, instance methods under the
// implicit-receiver rule (bare field/method names; `this.field` reserved
// for the shadowed case, which this class avoids by construction), and
// that classes are reference types — the list nodes referenced
// from the map are the same objects mutated by `touch`.

import fmt

class LRUNode<K, V> {
  let key:   K
  var value: V
  var prev:  Option<LRUNode<K, V>>
  var next:  Option<LRUNode<K, V>>
}

class LRU<K, V> {
  let capacity: int

  var size:  int
  var head:  Option<LRUNode<K, V>>      // most recently used
  var tail:  Option<LRUNode<K, V>>      // least recently used (eviction target)
  var index: Map<K, LRUNode<K, V>>

  static new(capacity: int): LRU<K, V> {
    return LRU<K, V>{
      capacity: capacity,
      size:     0,
      head:     None,
      tail:     None,
      index:    Map<K, LRUNode<K, V>>{},
    }
  }

  // Returns the cached value for `k`, marking it most-recently-used.
  get(k: K): Option<V> {
    match index.get(k) {
      None    => None,
      Some(n) => {
        touch(n)
        Some(n.value)
      },
    }
  }

  // Inserts or overwrites. Evicts the least-recently-used entry if full.
  put(k: K, v: V) {
    match index.get(k) {
      Some(n) => {
        n.value = v
        touch(n)
        return
      },
      None => {},
    }
    let n = LRUNode<K, V>{
      key:   k,
      value: v,
      prev:  None,
      next:  head,
    }
    match head {
      Some(h) => h.prev = Some(n),
      None    => tail = Some(n),
    }
    head = Some(n)
    index.set(k, n)
    size = size + 1
    if size > capacity {
      evictTail()
    }
  }

  len(): int { return size }

  // ----- internals -----

  touch(n: LRUNode<K, V>) {
    match head {
      Some(h) => if refEq(h, n) { return },
      None    => return,
    }
    detach(n)
    n.next = head
    n.prev = None
    match head {
      Some(h) => h.prev = Some(n),
      None    => {},
    }
    head = Some(n)
  }

  detach(n: LRUNode<K, V>) {
    match n.prev {
      Some(p) => p.next = n.next,
      None    => head = n.next,
    }
    match n.next {
      Some(nx) => nx.prev = n.prev,
      None     => tail = n.prev,
    }
    n.prev = None
    n.next = None
  }

  evictTail() {
    match tail {
      None    => return,
      Some(t) => {
        detach(t)
        index.delete(t.key)
        size = size - 1
      },
    }
  }
}

func main() {
  let cache = LRU<string, int>.new(2)
  cache.put("a", 1)
  cache.put("b", 2)
  fmt.println("get a:", cache.get("a"))       // -> Some(1); a becomes MRU
  cache.put("c", 3)                            // evicts b
  fmt.println("get b:", cache.get("b"))       // -> None
  fmt.println("get c:", cache.get("c"))       // -> Some(3)
  fmt.println("len:",   cache.len())          // -> 2
}
