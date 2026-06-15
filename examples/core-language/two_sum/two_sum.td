// LeetCode 1. Two Sum.
// Given an array of integers and a target, return the indices of the two
// numbers that add up to the target. Single pass, hash-map: O(n) time.
//
// Forces: slices, hash maps (Map<int, int>), iteration with index.

import fmt

func twoSum(nums: []int, target: int): Option<(int, int)> {
  var seen = Map<int, int>{}      // value -> its index in `nums`
  for (i, n) in nums {
    let want = target - n
    match seen.get(want) {
      Some(j) => return Some((j, i)),
      None    => seen.set(n, i),
    }
  }
  return None
}

func main() {
  let nums = [2, 7, 11, 15]
  let target = 9

  match twoSum(nums, target) {
    Some((i, j)) => fmt.println("indices:", i, j),
    None         => fmt.println("no pair"),
  }
}
