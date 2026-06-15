// Showcase: a generic sum type, recursion, and exhaustive matching --
// exactly where Tide's type system pays off over Go, which has no sum types.

import fmt
import strconv

type Tree<T> =
  | Leaf
  | Node(value: T, left: Tree<T>, right: Tree<T>)

func invert<T>(t: Tree<T>): Tree<T> {
  match t {
    Leaf          => Leaf,
    Node(v, l, r) => Node(v, invert(r), invert(l)),
  }
}

// In-order traversal, rendered as "(L v R)" so the inversion is visible.
func render(t: Tree<int>): string {
  match t {
    Leaf          => ".",
    Node(v, l, r) => "(" + render(l) + " " + strconv.itoa(v) + " " + render(r) + ")",
  }
}

func main() {
  //       4
  //      / \
  //     2   7
  //    / \ / \
  //   1  3 6  9
  let t = Node(4,
    Node(2, Node(1, Leaf, Leaf), Node(3, Leaf, Leaf)),
    Node(7, Node(6, Leaf, Leaf), Node(9, Leaf, Leaf)))

  fmt.println("before:", render(t))
  fmt.println("after: ", render(invert(t)))
}
