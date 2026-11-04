// Package jsonq evaluates the small JSONPath subset used by the test set: $.a.b[0].c with [*].
package jsonq

import (
	"fmt"
	"strconv"
	"strings"
)

type step struct {
	key   string
	index int
	any   bool
	isKey bool
}

// Parse splits a path such as $.a[0].b[*] into steps.
func Parse(path string) ([]step, error) {
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("path %q must start with $", path)
	}
	var steps []step
	rest := path[1:]
	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			if end == 0 {
				return nil, fmt.Errorf("path %q: empty key", path)
			}
			steps = append(steps, step{key: rest[:end], isKey: true})
			rest = rest[end:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("path %q: missing ]", path)
			}
			inner := rest[1:end]
			if inner == "*" {
				steps = append(steps, step{any: true})
			} else {
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("path %q: bad index %q", path, inner)
				}
				steps = append(steps, step{index: n})
			}
			rest = rest[end+1:]
		default:
			return nil, fmt.Errorf("path %q: unexpected %q", path, rest)
		}
	}
	return steps, nil
}

// Select returns every value the path matches in v (decoded JSON). A key holding null still matches.
func Select(v any, path string) ([]any, error) {
	steps, err := Parse(path)
	if err != nil {
		return nil, err
	}
	cur := []any{v}
	for _, s := range steps {
		var next []any
		for _, c := range cur {
			switch {
			case s.isKey:
				if m, ok := c.(map[string]any); ok {
					if x, ok := m[s.key]; ok {
						next = append(next, x)
					}
				}
			case s.any:
				if a, ok := c.([]any); ok {
					next = append(next, a...)
				}
			default:
				if a, ok := c.([]any); ok && s.index < len(a) {
					next = append(next, a[s.index])
				}
			}
		}
		cur = next
	}
	return cur, nil
}

// TypeName names the JSON type of a decoded value.
func TypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}
