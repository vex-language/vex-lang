// Fannkuch-redux: flips prefixes of every permutation, in place on slices.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	n := 10
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	perm := make([]int, n)
	perm1 := make([]int, n)
	count := make([]int, n)
	for i := range perm1 {
		perm1[i] = i
	}
	maxFlips, checksum, permCount := 0, 0, 0
	r := n
	for {
		for r != 1 {
			count[r-1] = r
			r--
		}
		copy(perm, perm1)
		flips := 0
		for k := perm[0]; k != 0; k = perm[0] {
			for i, j := 0, k; i < j; i, j = i+1, j-1 {
				perm[i], perm[j] = perm[j], perm[i]
			}
			flips++
		}
		if flips > maxFlips {
			maxFlips = flips
		}
		if permCount%2 == 0 {
			checksum += flips
		} else {
			checksum -= flips
		}

		// Moves to the next permutation.
		for {
			if r == n {
				fmt.Println(checksum)
				fmt.Printf("fannkuch(%d) = %d\n", n, maxFlips)
				return
			}
			first := perm1[0]
			for i := 0; i < r; i++ {
				perm1[i] = perm1[i+1]
			}
			perm1[r] = first
			count[r]--
			if count[r] > 0 {
				break
			}
			r++
		}
		permCount++
	}
}
