package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Vist233/customs-llm/internal/testset"
	testsetdata "github.com/Vist233/customs-llm/testset"
)

func loadTestset(dir string) ([]testset.Case, error) {
	if dir == "" {
		return testset.LoadFS(testsetdata.Cases, "cases")
	}
	return testset.Load(dir)
}

func cmdTestset(args []string) error {
	fs := flag.NewFlagSet("testset", flag.ExitOnError)
	dir := fs.String("dir", "", "load cases from this directory instead of the ones embedded in the binary")
	if len(args) == 0 {
		return fmt.Errorf("usage: customs testset validate|list|stats [--dir DIR]")
	}
	sub := args[0]
	_ = fs.Parse(args[1:])
	cases, err := loadTestset(*dir)
	if err != nil {
		return err
	}
	switch sub {
	case "validate":
		fmt.Printf("ok: %d cases\n", len(cases))
	case "list":
		for _, c := range cases {
			fmt.Printf("%-58s %-18s %-9s %-8s %s\n", c.ID, c.Gateway.Name, c.Direction, c.Severity, c.Title)
		}
	case "stats":
		testset.PrintStats(os.Stdout, cases)
	default:
		return fmt.Errorf("unknown testset command %q", sub)
	}
	return nil
}
