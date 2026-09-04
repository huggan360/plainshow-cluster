//go:build !desktop || !linux

package main

import "fmt"

func main() {
	fmt.Println("Plainshow Cluster Desktop is built for Linux with the desktop build tag.")
}
