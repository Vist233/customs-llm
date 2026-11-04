package jsonq

import (
	"encoding/json"
	"testing"
)

func TestSelect(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`{"a":{"b":[{"c":1},{"c":2},{"d":null}]},"n":null}`), &v)
	for path, want := range map[string]int{
		"$.a.b[0].c": 1, "$.a.b[*].c": 2, "$.a.b[*].d": 1, "$.a.b[5]": 0, "$.n": 1, "$.x.y": 0, "$": 1,
	} {
		got, err := Select(v, path)
		if err != nil || len(got) != want {
			t.Errorf("%s: got %d matches, err %v, want %d", path, len(got), err, want)
		}
	}
	if _, err := Select(v, "a.b"); err == nil {
		t.Error("expected error for path without $")
	}
}
