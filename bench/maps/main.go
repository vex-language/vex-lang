// Inserts, looks up and removes keys in hash maps with int and string keys.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	n := 2_000_000
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	ints := map[int]int{}
	for i := 0; i < n; i++ {
		ints[(i*2654435761)&0xFFFFFFF] = i
	}
	found, sum := 0, 0
	for i := 0; i < 2*n; i++ {
		if v, ok := ints[(i*2654435761)&0xFFFFFFF]; ok {
			found++
			sum += v
		}
	}
	for i := 0; i < n; i += 2 {
		delete(ints, (i*2654435761)&0xFFFFFFF)
	}

	words := map[string]int{}
	for i := 0; i < n; i++ {
		words["w"+strconv.Itoa(i%5000)]++
	}
	most := 0
	for _, count := range words {
		if count > most {
			most = count
		}
	}
	fmt.Printf("maps(%d) = %d %d %d %d %d\n", n, found, sum, len(ints), len(words), most)
}
