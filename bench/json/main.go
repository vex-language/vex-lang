// Encodes a list of records as JSON and decodes it back.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// One record of the document.
type Item struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Tags   []string `json:"tags"`
	Active bool     `json:"active"`
	Parent *int     `json:"parent"`
}

func main() {
	rounds := 200
	if len(os.Args) > 1 {
		rounds, _ = strconv.Atoi(os.Args[1])
	}
	items := make([]Item, 0, 1000)
	for i := 0; i < 1000; i++ {
		var parent *int
		if i%4 != 0 {
			p := i / 4
			parent = &p
		}
		items = append(items, Item{
			ID:     i,
			Name:   "item number " + strconv.Itoa(i),
			Tags:   []string{"t" + strconv.Itoa(i%7), "group " + strconv.Itoa(i%13)},
			Active: i%3 == 0,
			Parent: parent,
		})
	}
	bytes, sum := 0, 0
	for r := 0; r < rounds; r++ {
		text, err := json.Marshal(items)
		if err != nil {
			panic(err)
		}
		var back []Item
		if err := json.Unmarshal(text, &back); err != nil {
			panic(err)
		}
		bytes += len(text)
		for _, item := range back {
			sum += item.ID + len(item.Tags)
			if item.Parent != nil {
				sum += *item.Parent
			}
			if item.Active {
				sum++
			}
		}
	}
	fmt.Printf("json(%d) = %d %d\n", rounds, bytes, sum)
}
