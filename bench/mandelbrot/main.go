// Counts the points of a grid inside the Mandelbrot set: tight float loops.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	n := 3000
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	const maxIter = 50
	inside, hash := 0, 0
	for y := 0; y < n; y++ {
		ci := 2.0*float64(y)/float64(n) - 1.0
		for x := 0; x < n; x++ {
			cr := 2.0*float64(x)/float64(n) - 1.5
			zr, zi := 0.0, 0.0
			i := 0
			for i < maxIter && zr*zr+zi*zi <= 4.0 {
				t := zr*zr - zi*zi + cr
				zi = 2.0*zr*zi + ci
				zr = t
				i++
			}
			if i == maxIter {
				inside++
			}
			hash = hash*31 + i
		}
	}
	fmt.Printf("mandelbrot(%d) = %d %d\n", n, inside, hash)
}
