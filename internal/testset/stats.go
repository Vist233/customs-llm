package testset

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// PrintStats summarises coverage of the test set.
func PrintStats(w io.Writer, cases []Case) {
	groups := []struct {
		name string
		key  func(Case) string
	}{
		{"gateway", func(c Case) string { return c.Gateway.Name }},
		{"direction", func(c Case) string { return c.Direction }},
		{"protocol in->out", func(c Case) string { return c.Protocol.In + "->" + c.Protocol.Out }},
		{"category", func(c Case) string { return c.Category }},
		{"severity", func(c Case) string { return c.Severity }},
		{"payload_origin", func(c Case) string { return c.PayloadOrigin }},
		{"detectable", func(c Case) string { return c.Detectable }},
		{"verified", func(c Case) string { return fmt.Sprint(c.Verified) }},
		{"has examples", func(c Case) string { return fmt.Sprint(c.Examples != nil) }},
	}
	fmt.Fprintf(w, "%d cases\n", len(cases))
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
		fmt.Fprintf(w, "%-15s %s\n", g.name, strings.Join(parts, " "))
	}
}
