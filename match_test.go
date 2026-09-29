package main

import "testing"

func TestMatchAndReport(t *testing.T) {
	src := `The Industrial Revolution was the transition to new manufacturing processes in Great Britain, continental Europe, and the United States, that occurred during the period from around 1760 to about 1820–1840. This transition included going from hand production methods to machines, new chemical manufacturing and iron production processes.`
	sub := "My essay starts here with my own words about history and change.\n" +
		`The Industrial Revolution was the move to new manufacturing processes in Great Britain, continental Europe, and the Unіted States, which occurred during the period from around 1760 to about 1820. ` +
		`As Smith wrote, "this transition included going from hand production methods to machines" (Smith, 2019). Then I conclude with something original entirely.` + "\n\nReferences\nSmith, J. (2019). History of machines. Oxford."
	toks := Tokenize(sub)
	blocks := MatchTexts(toks, src)
	if len(blocks) == 0 {
		t.Fatal("no blocks")
	}
	for _, b := range blocks {
		t.Logf("block %d-%d: %q", b.From, b.ToTok, sub[toks[b.From].Start:toks[b.ToTok-1].End])
	}
	rep := BuildReport(sub, []SourceMatch{{ID: 1, URL: "https://en.wikipedia.org/wiki/Industrial_Revolution", Title: "IR", Type: "Internet", Blocks: blocks}}, Filters{})
	t.Logf("index=%d matched=%d total=%d groups=%+v", rep.Index, rep.MatchedWords, rep.TotalWords, rep.Groups)
	for _, h := range rep.Highlights {
		t.Logf("hl %q %s", sub[h.Start:h.End], h.Group)
	}
	rep2 := BuildReport(sub, []SourceMatch{{ID: 1, Blocks: blocks, Type: "Internet"}}, Filters{ExcludeQuotes: true, ExcludeBib: true})
	t.Logf("excl quotes index=%d", rep2.Index)
	if len(ReplacedChars(sub)) != 1 {
		t.Errorf("replaced chars: %v", ReplacedChars(sub))
	}
	if BibliographyStart(sub) < 0 {
		t.Error("no bib")
	}
}
