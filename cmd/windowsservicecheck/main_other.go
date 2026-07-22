//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "Windows service recovery verification is only available on Windows")
	os.Exit(1)
}
