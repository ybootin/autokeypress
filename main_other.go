//go:build !windows && !darwin

package main

import "fmt"

func main() {
	fmt.Println("This app currently supports Windows and macOS only.")
}
