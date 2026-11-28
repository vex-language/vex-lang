// The spectral norm of an infinite matrix: float math over slices.
package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
)

// Returns the entry at row i, column j of the matrix.
func a(i, j int) float64 { return 1.0 / float64((i+j)*(i+j+1)/2+i+1) }

// Sets out to the matrix times v.
func timesVec(v, out []float64) {
	n := len(v)
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum += a(i, j) * v[j]
		}
		out[i] = sum
	}
}

// Sets out to the transposed matrix times v.
func timesTransposedVec(v, out []float64) {
	n := len(v)
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum += a(j, i) * v[j]
		}
		out[i] = sum
	}
}

// Sets out to the transposed matrix times the matrix times v.
func timesAtA(v, out, tmp []float64) {
	timesVec(v, tmp)
	timesTransposedVec(tmp, out)
}

func main() {
	n := 3000
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	u := make([]float64, n)
	v := make([]float64, n)
	tmp := make([]float64, n)
	for i := range u {
		u[i] = 1
	}
	for i := 0; i < 10; i++ {
		timesAtA(u, v, tmp)
		timesAtA(v, u, tmp)
	}
	var vBv, vv float64
	for i := 0; i < n; i++ {
		vBv += u[i] * v[i]
		vv += v[i] * v[i]
	}
	fmt.Printf("spectral-norm(%d) = %.9f\n", n, math.Sqrt(vBv/vv))
}
