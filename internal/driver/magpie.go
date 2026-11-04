package driver

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Magpie uses the magpie command line: custom providers with one entry point per protocol.
type Magpie struct {
	// Command runs magpie, e.g. ["magpie"] or ["docker","exec","customs-magpie","magpie"].
	Command []string
}

func (m *Magpie) Name() string { return "magpie" }

func (m *Magpie) Plan(fakeURL, suffix string) []Route {
	fake := strings.TrimRight(fakeURL, "/")
	mk := func(key, short, modelName string, args ...string) Route {
		id := "customs-probe-" + suffix + "-" + short
		return Route{Key: key, Model: id + "/" + modelName, Ref: id, Proto: strings.TrimPrefix(key, "*->"), Spec: map[string]any{"args": append([]string{"models=" + modelName, "key=customs-fake", "id=" + id}, args...)}}
	}
	return []Route{
		mk("*->openai_chat", "chat", "customs-chat", "url="+fake+"/v1"),
		// Each provider offers one entry point only, otherwise magpie prefers the one matching the client.
		mk("*->openai_responses", "responses", "customs-responses", "responses="+fake+"/v1"),
		mk("*->anthropic_messages", "anthropic", "customs-anthropic", "anthropic="+fake),
	}
}

func (m *Magpie) args(r Route) []string {
	var out []string
	switch a := r.Spec["args"].(type) {
	case []string:
		out = a
	case []any:
		for _, x := range a {
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

func (m *Magpie) Describe(r Route) string {
	return strings.Join(m.Command, " ") + " provider add " + r.Ref + " " + strings.Join(m.args(r), " ")
}

func (m *Magpie) run(ctx context.Context, args ...string) error {
	all := append(append([]string{}, m.Command[1:]...), args...)
	out, err := exec.CommandContext(ctx, m.Command[0], all...).CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if len(s) > 300 {
			s = s[:300]
		}
		return fmt.Errorf("magpie %s: %v: %s", args[0], err, s)
	}
	return nil
}

func (m *Magpie) Add(ctx context.Context, r Route) error {
	return m.run(ctx, append([]string{"provider", "add", r.Ref}, m.args(r)...)...)
}

func (m *Magpie) Remove(ctx context.Context, r Route) error {
	return m.run(ctx, "provider", "rm", r.Ref)
}
