package main

import (
	"fmt"
	"os"

	"gomake/internal/cli"
)

type exitCodedError interface {
	ExitCode() int
	Silent() bool
}

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		if coded, ok := err.(exitCodedError); ok {
			if !coded.Silent() {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			os.Exit(coded.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
