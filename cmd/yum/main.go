package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/hunchom/yum/internal/cmd"
	"github.com/hunchom/yum/internal/ui/theme"
)

func main() {
	root := cmd.NewRoot()
	if err := root.Execute(); err != nil {
		var ec *cmd.ExitErr
		msg := err.Error()
		if errors.As(err, &ec) {
			msg = ec.Short
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", theme.Err.Render("yum:"), msg)
		if ec != nil && ec.Code > 0 {
			os.Exit(ec.Code)
		}
		os.Exit(1)
	}
}
