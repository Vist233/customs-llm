// Command tsctl maintains the test set: validate it, list its cases and summarise coverage.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Vist233/customs-llm/internal/testset"
)

func main() {
	dir := flag.String("dir", "testset/cases", "directory holding the cases")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tsctl [-dir testset/cases] validate|list|stats")
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	cases, err := testset.Load(*dir)
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
		stats(cases)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func stats(cases []testset.Case) {
	groups := []struct {
		name string
		key  func(testset.Case) string
	}{
		{"gateway", func(c testset.Case) string { return c.Gateway.Name }},
		{"direction", func(c testset.Case) string { return c.Direction }},
		{"category", func(c testset.Case) string { return c.Category }},
		{"severity", func(c testset.Case) string { return c.Severity }},
		{"payload_origin", func(c testset.Case) string { return c.PayloadOrigin }},
		{"detectable", func(c testset.Case) string { return c.Detectable }},
		{"verified", func(c testset.Case) string { return fmt.Sprint(c.Verified) }},
		{"has examples", func(c testset.Case) string { return fmt.Sprint(c.Examples != nil) }},
	}
	fmt.Printf("%d cases\n", len(cases))
	for _, g := range groups {
		counts := map[string]int{}
		for _, c := range cases {
			counts[g.key(c)]++
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s=%d", k, counts[k])
		}
		fmt.Printf("%-15s %s\n", g.name, strings.Join(parts, " "))
	}
}
