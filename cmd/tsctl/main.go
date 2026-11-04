// Command tsctl maintains the test set: validate it, list its cases and summarise coverage.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Vist233/customs-llm/internal/testset"
	testsetdata "github.com/Vist233/customs-llm/testset"
)

func main() {
	dir := flag.String("dir", "", "directory holding the cases (default: the test set embedded in the binary)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tsctl [-dir DIR] validate|list|stats")
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	var cases []testset.Case
	var err error
	if *dir == "" {
		cases, err = testset.LoadFS(testsetdata.Cases, "cases")
	} else {
		cases, err = testset.Load(*dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid test set:", err)
		os.Exit(1)
	}
	switch flag.Arg(0) {
	case "validate":
		fmt.Printf("ok: %d cases\n", len(cases))
	case "list":
		for _, c := range cases {
			fmt.Printf("%-52s %-10s %-9s %-8s %s\n", c.ID, c.Gateway.Name, c.Direction, c.Severity, c.Title)
		}
	case "stats":
		testset.PrintStats(os.Stdout, cases)
	default:
		flag.Usage()
		os.Exit(2)
	}
}
