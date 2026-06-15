// LeetCode 206. Reverse Linked List.
// Iterative reversal of a singly-linked list. Classic O(n) / O(1).
//
// Forces: reference-typed nodes (`class` is a reference type, records
// are values), Option-typed links, mutation through `var` and `this.field`.

import fmt
import strconv

class Node {
  var value: int
  var next:  Option<Node>      // None for the tail
}

func reverse(head: Option<Node>): Option<Node> {
  var prev: Option<Node> = None
  var curr = head
  while true {
    match curr {
      None    => return prev,
      Some(n) => {
        let nxt = n.next
        n.next = prev
        prev = Some(n)
        curr = nxt
      },
    }
  }
}

// 1 -> 2 -> 3 -> None
func build(values: []int): Option<Node> {
  var head: Option<Node> = None
  // Build back-to-front so `head` ends up at values[0].
  var i = values.len() - 1
  while i >= 0 {
    head = Some(Node{ value: values[i], next: head })
    i = i - 1
  }
  return head
}

func render(head: Option<Node>): string {
  var out = ""
  var cur = head
  while true {
    match cur {
      None    => return out + "None",
      Some(n) => {
        out = out + strconv.itoa(n.value) + " -> "
        cur = n.next
      },
    }
  }
}

func main() {
  let xs = build([1, 2, 3, 4, 5])
  fmt.println("before:", render(xs))
  fmt.println("after: ", render(reverse(xs)))
}
