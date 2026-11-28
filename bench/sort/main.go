// Sorts pseudo-random ints and strings.
package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
)

func main() {
	n := 3_000_000
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	seed := uint64(42)
	nums := make([]int, 0, n)
	for i := 0; i < n; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		nums = append(nums, int(int64(seed)>>33))
	}
	slices.Sort(nums)
	hash := 0
	for _, x := range nums {
		hash = hash*31 + x
	}

	words := make([]string, 0, n/4)
	for i := 0; i < n/4; i++ {
		words = append(words, "k"+strconv.Itoa(nums[(i*7919)%n]%100_000))
	}
	sorted := slices.Clone(words)
	slices.Sort(sorted)
	for _, w := range sorted {
		hash = hash*31 + len(w)
	}
	fmt.Printf("sort(%d) = %d %d %s %s %d\n", n, nums[0], nums[n-1], sorted[0], sorted[len(sorted)-1], hash)
}
