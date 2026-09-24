package main

import "fmt"

func main() {
	var blocks [][]byte
	for i := 0; ; i++ {
		b := make([]byte, 1<<24)
		for j := range b {
			b[j] = byte(i)
		}
		blocks = append(blocks, b)
		if len(blocks) < 0 {
			fmt.Println(len(blocks))
		}
	}
}
