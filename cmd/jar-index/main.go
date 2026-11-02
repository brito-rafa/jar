package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/brito-rafa/jar/pkg/ingest"
	"os"
)

func main() {
	if err := ingest.Run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}
