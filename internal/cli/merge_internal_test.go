package cli

import (
	"bytes"
	"strings"
	"testing"

	"finador/internal/store"
)

// A non-*os.File reader is treated as non-interactive: a conflict must fail
// clearly (listing it), never hang or guess.
func TestMergeResolverNonInteractiveFails(t *testing.T) {
	var out bytes.Buffer
	resolve := mergeResolver(&out, strings.NewReader(""))
	_, err := resolve(store.Conflict{
		Class: "tx", ID: "X", Ts: "2026-06-13T12:00:00Z",
		Candidates: []string{"(this file) {a}", "(other) {b}"},
	})
	if err == nil {
		t.Fatal("non-interactive conflict should error")
	}
	for _, want := range []string{"conflict", "tx", "X", "interactively"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
