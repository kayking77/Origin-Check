package main

import (
	"strings"
	"testing"
)

// Search phrases must be real words from the paper, not text cut at the wrong offsets.
func TestQueriesUseRealWords(t *testing.T) {
	text := "Intro line.\nThe aim of this experiment was to carry out an enzyme assay to study the effects of pH and temperature. " +
		"Kinetics of an enzyme catalysed reaction can be measured by measuring the rate of appearance of products."
	qs := pickQueries(text, -1, 5)
	if len(qs) == 0 {
		t.Fatal("no queries")
	}
	for _, q := range qs {
		for _, w := range strings.Fields(q) {
			if !strings.Contains(text, w) {
				t.Errorf("query %q has word %q that is not in the text", q, w)
			}
		}
	}
}
