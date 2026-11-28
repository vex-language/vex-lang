// Builds, joins, splits and searches strings.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	rounds := 3000
	if len(os.Args) > 1 {
		rounds, _ = strconv.Atoi(os.Args[1])
	}
	total := 0
	for round := 0; round < rounds; round++ {
		parts := []string{}
		for i := 0; i < 1000; i++ {
			parts = append(parts, "item-"+strconv.Itoa(i)+"-"+strconv.Itoa(round))
		}
		line := strings.Join(parts, ",")
		total += len(line) + len(strings.Split(line, ","))
		total += len(strings.ReplaceAll(line, "item", "x"))
		if strings.Contains(line, "item-999-"+strconv.Itoa(round)) {
			total++
		}
		s := ""
		for i := 0; i < 100; i++ {
			s += "ab"
			s = s + strconv.Itoa(i%10)
		}
		total += len(s)
	}
	fmt.Printf("strings(%d) = %d\n", rounds, total)
}
