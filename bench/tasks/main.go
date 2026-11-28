// Starts a binary tree of goroutines, each waiting for the one it started.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
)

// Returns the number of tasks in a tree of the given depth.
func tree(depth int) int {
	if depth == 0 {
		return 1
	}
	left := make(chan int, 1)
	go func() { left <- tree(depth - 1) }()
	right := tree(depth - 1)
	return <-left + right + 1
}

func main() {
	// Vex tasks share one thread, so goroutines do too.
	runtime.GOMAXPROCS(1)
	depth := 20
	if len(os.Args) > 1 {
		depth, _ = strconv.Atoi(os.Args[1])
	}
	fmt.Printf("tasks(%d) = %d\n", depth, tree(depth))
}
