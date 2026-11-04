// Package driver adds temporary test models to a gateway and removes them again. It only ever adds
// entries of its own, named customs-probe-*, and never edits what is already configured.
package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Route is one temporary entry and how the probe selects it.
type Route struct {
	Key   string `json:"key"`   // alias key understood by probe.Target, e.g. "*->openai_chat" or "route:mistral"
	Model string `json:"model"` // model name a client sends to the gateway
	Ref   string `json:"ref"`   // the driver's handle for removing the entry
	// Spec is the driver-specific description of the entry.
	Spec map[string]any `json:"spec,omitempty"`
	// Also lists more alias keys served by this entry; Proto is its upstream protocol.
	Also  []string `json:"also,omitempty"`
	Proto string   `json:"proto,omitempty"`
}

// Aliases turns routes into the alias map probe.Target expects.
func Aliases(routes []Route) map[string]string {
	m := map[string]string{}
	set := func(k string, r Route) {
		if strings.HasPrefix(k, "route:") && r.Proto != "" {
			m[k] = r.Model + "@" + r.Proto
			return
		}
		m[k] = r.Model
	}
	for _, r := range routes {
		set(r.Key, r)
		for _, a := range r.Also {
			set(a, r)
		}
	}
	return m
}

// Driver knows one gateway's management interface.
type Driver interface {
	Name() string
	// Plan describes the entries for a fake upstream at fakeURL (as the gateway reaches it). It changes nothing.
	Plan(fakeURL, suffix string) []Route
	Add(ctx context.Context, r Route) error
	Remove(ctx context.Context, r Route) error
	// Describe renders one entry for the person approving the plan.
	Describe(r Route) string
}

// Options configure a driver.
type Options struct {
	URL     string
	Key     string
	Command []string // how to run the gateway's CLI, for drivers that use one
}

// New returns the named driver.
func New(name string, o Options) (Driver, error) {
	switch name {
	case "litellm":
		return &LiteLLM{URL: o.URL, Key: o.Key}, nil
	case "magpie":
		cmd := o.Command
		if len(cmd) == 0 {
			cmd = []string{"magpie"}
		}
		return &Magpie{Command: cmd}, nil
	}
	return nil, fmt.Errorf("no driver for %q (have: litellm, magpie)", name)
}

// Override is a manifest entry: enough to remove what was added, without any secret.
type Override struct {
	PlanID  string   `json:"plan_id"`
	Driver  string   `json:"driver"`
	URL     string   `json:"url"`
	Command []string `json:"command,omitempty"`
	Routes  []Route  `json:"routes"`
}

var manifestMu sync.Mutex

func manifestPath(dir string) string { return filepath.Join(dir, "overrides.json") }

// LoadManifest lists entries that were added and not yet removed.
func LoadManifest(dir string) ([]Override, error) {
	b, err := os.ReadFile(manifestPath(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Override
	return out, json.Unmarshal(b, &out)
}

func writeManifest(dir string, v []Override) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := manifestPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, manifestPath(dir))
}

// Remember records an override before anything is added, so a crash can still be cleaned up.
func Remember(dir string, o Override) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	cur, err := LoadManifest(dir)
	if err != nil {
		return err
	}
	return writeManifest(dir, append(cur, o))
}

// Forget drops an override from the manifest.
func Forget(dir, planID string) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	cur, err := LoadManifest(dir)
	if err != nil {
		return err
	}
	kept := []Override{}
	for _, o := range cur {
		if o.PlanID != planID {
			kept = append(kept, o)
		}
	}
	if len(kept) == 0 {
		err := os.Remove(manifestPath(dir))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return writeManifest(dir, kept)
}

// RemoveAll removes every entry of an override, returning the routes that could not be removed.
func RemoveAll(ctx context.Context, d Driver, routes []Route) (failed []Route, firstErr error) {
	for _, r := range routes {
		if err := d.Remove(ctx, r); err != nil {
			failed = append(failed, r)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return failed, firstErr
}
