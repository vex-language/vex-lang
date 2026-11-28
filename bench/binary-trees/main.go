// Builds and walks many binary trees: allocation and garbage collection.
package main

import (
	"fmt"
	"os"
	"strconv"
)

// A tree node; leaves have no children.
type Node struct {
	left, right *Node
}

// Returns a full tree of the given depth.
func build(depth int) *Node {
	if depth == 0 {
		return &Node{}
	}
	return &Node{build(depth - 1), build(depth - 1)}
}

// Returns the number of nodes in the tree under node.
func check(node *Node) int {
	count := 1
	if node.left != nil {
		count += check(node.left)
	}
	if node.right != nil {
		count += check(node.right)
	}
	return count
}

func main() {
	n := 17
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	minDepth := 4
	maxDepth := n
	if n < minDepth+2 {
		maxDepth = minDepth + 2
	}

	stretch := maxDepth + 1
	fmt.Printf("stretch tree of depth %d\t check: %d\n", stretch, check(build(stretch)))

	longLived := build(maxDepth)
	total := 0
	for depth := minDepth; depth <= maxDepth; depth += 2 {
		iterations := 1 << (maxDepth - depth + minDepth)
		sum := 0
		for i := 0; i < iterations; i++ {
			sum += check(build(depth))
		}
		total += sum
		fmt.Printf("%d\t trees of depth %d\t check: %d\n", iterations, depth, sum)
	}
	fmt.Printf("long lived tree of depth %d\t check: %d\n", maxDepth, check(longLived))
	fmt.Printf("binary-trees(%d) = %d\n", n, total)
}
