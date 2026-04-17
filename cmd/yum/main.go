package main

import (
	"fmt"
	"os"

	"github.com/hunchom/yum/internal/cmd"
)

func main() {
	if err := cmd.NewRoot().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "yum: %v\n", err)
		os.Exit(1)
	}
}
