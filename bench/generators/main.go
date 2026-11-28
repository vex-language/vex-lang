// Reads values from iterators through lazy filter, map and take.
package main

import (
	"fmt"
	"iter"
	"os"
	"strconv"
)

// Yields 0, 1, 2 and so on without end.
func naturals() iter.Seq[int] {
	return func(yield func(int) bool) {
		for n := 0; ; n++ {
			if !yield(n) {
				return
			}
		}
	}
}

func filter(seq iter.Seq[int], keep func(int) bool) iter.Seq[int] {
	return func(yield func(int) bool) {
		for x := range seq {
			if keep(x) && !yield(x) {
				return
			}
		}
	}
}

func mapSeq(seq iter.Seq[int], f func(int) int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for x := range seq {
			if !yield(f(x)) {
				return
			}
		}
	}
}

func take(seq iter.Seq[int], n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		if n <= 0 {
			return
		}
		i := 0
		for x := range seq {
			if !yield(x) {
				return
			}
			i++
			if i == n {
				return
			}
		}
	}
}

// Yields the values of source in runs of size, as their sums.
func chunkSums(source iter.Seq[int], size int) iter.Seq[int] {
	return func(yield func(int) bool) {
		sum, count := 0, 0
		for x := range source {
			sum += x
			count++
			if count == size {
				if !yield(sum) {
					return
				}
				sum, count = 0, 0
			}
		}
	}
}

func main() {
	n := 10_000_000
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	values := take(mapSeq(filter(naturals(), func(x int) bool { return x%3 != 0 }), func(x int) int { return x * x }), n)
	total, count := 0, 0
	for s := range chunkSums(values, 10) {
		total = total*31 + s
		count++
	}
	fmt.Printf("generators(%d) = %d %d\n", n, count, total)
}
