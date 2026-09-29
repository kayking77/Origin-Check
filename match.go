package main

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
)

const seedK = 5         // words in a seed shingle
const minMatchWords = 7 // shortest overlap kept as a match

// MatchBlock is one passage of the submission that matches a source.
type MatchBlock struct {
	From    int    `json:"from"` // token index range [From, ToTok) in the submission
	ToTok   int    `json:"to"`
	Excerpt string `json:"excerpt"` // the source's text around the match
	ExStart int    `json:"exStart"` // byte range of the matching text inside Excerpt
	ExEnd   int    `json:"exEnd"`
}

// SourceMatch is a candidate source with everything it matches.
type SourceMatch struct {
	ID     int          `json:"id"`
	URL    string       `json:"url"`
	Title  string       `json:"title"`
	Type   string       `json:"type"` // Internet, Publication, Student Paper
	Ref    string       `json:"ref,omitempty"`
	Blocks []MatchBlock `json:"blocks"`
}

func shingleHash(toks []Token, i, k int) uint64 {
	h := fnv.New64a()
	for j := i; j < i+k; j++ {
		h.Write([]byte(toks[j].Norm))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

func contentWords(toks []Token) int {
	n := 0
	for _, t := range toks {
		if !stopwords[t.Norm] && len(t.Norm) > 2 {
			n++
		}
	}
	return n
}

// MatchTexts finds passages of the submission that also appear in the source, tolerating
// small edits (a word swapped, added or dropped), the way light paraphrasing looks.
func MatchTexts(sub []Token, srcText string) []MatchBlock {
	src := Tokenize(srcText)
	if len(src) < seedK || len(sub) < seedK {
		return nil
	}
	index := make(map[uint64][]int, len(src))
	for j := 0; j+seedK <= len(src); j++ {
		h := shingleHash(src, j, seedK)
		if len(index[h]) < 40 {
			index[h] = append(index[h], j)
		}
	}
	eq := func(a, b int) bool { return sub[a].Norm == src[b].Norm }
	var blocks []MatchBlock
	i := 0
	for i+seedK <= len(sub) {
		cands := index[shingleHash(sub, i, seedK)]
		bestA, bestB, bestJ := i, 0, -1
		for _, j := range cands {
			a, b := i, j
			gaps := 0
			matched := 0
			for a < len(sub) && b < len(src) {
				if eq(a, b) {
					a++
					b++
					matched++
					continue
				}
				// Try to re-align after a small edit: 3 agreeing words within 3 steps.
				found := false
				for d := 1; d <= 3 && !found; d++ {
					for k1 := 0; k1 <= d; k1++ {
						k2 := d - k1
						if a+k1+3 > len(sub) || b+k2+3 > len(src) {
							continue
						}
						if eq(a+k1, b+k2) && eq(a+k1+1, b+k2+1) && eq(a+k1+2, b+k2+2) {
							a += k1
							b += k2
							found = true
							break
						}
					}
				}
				if !found {
					break
				}
				gaps++
				if gaps*6 > matched+6 {
					break
				}
			}
			if a-i > bestA-i {
				bestA, bestB, bestJ = a, b, j
			}
		}
		if bestJ >= 0 && bestA-i >= minMatchWords && contentWords(sub[i:bestA]) >= 3 {
			s0, s1 := src[bestJ].Start, src[bestB-1].End
			cs := s0 - 220
			if cs < 0 {
				cs = 0
			}
			ce := s1 + 220
			if ce > len(srcText) {
				ce = len(srcText)
			}
			cs = runeStart(srcText, cs)
			ce = runeStart(srcText, ce)
			blocks = append(blocks, MatchBlock{From: i, ToTok: bestA, Excerpt: srcText[cs:ce], ExStart: s0 - cs, ExEnd: s1 - cs})
			i = bestA
			continue
		}
		i++
	}
	return blocks
}

func runeStart(s string, i int) int {
	for i > 0 && i < len(s) && s[i]&0xC0 == 0x80 {
		i--
	}
	return i
}

// Filters are the Turnitin-style exclusions a teacher can toggle on a report.
type Filters struct {
	ExcludeQuotes   bool     `json:"excludeQuotes"`
	ExcludeBib      bool     `json:"excludeBib"`
	ExcludeCited    bool     `json:"excludeCited"`
	ExcludeSmall    int      `json:"excludeSmall"` // words; 0 = off
	ExcludedSources []int    `json:"excludedSources"`
	ExcludedMatches []string `json:"excludedMatches"` // "<source id>:<block index>"
}

// Highlight is one coloured passage in the report.
type Highlight struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Source int    `json:"source"` // rank number shown in the report
	SrcID  int    `json:"srcId"`
	Block  int    `json:"block"`
	Group  string `json:"group"`
	Words  int    `json:"words"`
}

type RankedSource struct {
	Rank     int     `json:"rank"`
	ID       int     `json:"id"`
	URL      string  `json:"url"`
	Title    string  `json:"title"`
	Type     string  `json:"type"`
	Words    int     `json:"words"`
	Percent  float64 `json:"percent"`
	Blocks   int     `json:"blocks"`
	Total    int     `json:"totalWords"` // everything it matches, including words credited to a higher source
	Excluded bool    `json:"excluded"`
}

type GroupStat struct {
	Name    string  `json:"name"`
	Count   int     `json:"count"`
	Words   int     `json:"words"`
	Percent float64 `json:"percent"`
}

type SimilarityReport struct {
	TotalWords   int            `json:"totalWords"`
	MatchedWords int            `json:"matchedWords"`
	Index        int            `json:"index"`
	IndexExact   float64        `json:"indexExact"`
	ByType       map[string]int `json:"byType"`
	Sources      []RankedSource `json:"sources"`
	Highlights   []Highlight    `json:"highlights"`
	Groups       []GroupStat    `json:"groups"`
	BibStart     int            `json:"bibStart"`
	Quotes       []Span         `json:"quotes"`
	Excluded     []Span         `json:"excluded"`
}

var groupNames = []string{"Not Cited or Quoted", "Missing Quotations", "Missing Citation", "Cited and Quoted"}

// BuildReport applies the filters and credits each matched word to one source, strongest
// source first, the way Turnitin's Match Overview does.
func BuildReport(text string, sources []SourceMatch, f Filters) SimilarityReport {
	toks := Tokenize(text)
	n := len(toks)
	rep := SimilarityReport{TotalWords: n, ByType: map[string]int{}, BibStart: BibliographyStart(text)}
	quotes := QuoteSpans(text)
	rep.Quotes = quotes
	sents := Sentences(text)
	cited := CitedSentences(text, sents)

	tokQuoted := make([]bool, n)
	tokCited := make([]bool, n)
	excluded := make([]bool, n)
	si := 0
	for i, t := range toks {
		tokQuoted[i] = inSpans(t.Start, quotes)
		for si < len(sents) && sents[si].End <= t.Start {
			si++
		}
		if si < len(sents) && t.Start >= sents[si].Start {
			tokCited[i] = cited[si]
		}
		if f.ExcludeBib && rep.BibStart >= 0 && t.Start >= rep.BibStart {
			excluded[i] = true
		}
		if f.ExcludeQuotes && tokQuoted[i] {
			excluded[i] = true
		}
		if f.ExcludeCited && tokCited[i] {
			excluded[i] = true
		}
	}
	// Report excluded regions so the document view can grey them out.
	for i := 0; i < n; {
		if !excluded[i] {
			i++
			continue
		}
		j := i
		for j < n && excluded[j] {
			j++
		}
		rep.Excluded = append(rep.Excluded, Span{toks[i].Start, toks[j-1].End})
		i = j
	}

	exSrc := map[int]bool{}
	for _, id := range f.ExcludedSources {
		exSrc[id] = true
	}
	exMatch := map[string]bool{}
	for _, k := range f.ExcludedMatches {
		exMatch[k] = true
	}

	type cand struct {
		src   *SourceMatch
		tok   []int // block index per token, -1 = none
		total int
	}
	var cands []*cand
	var excludedList []RankedSource
	for si := range sources {
		s := &sources[si]
		c := &cand{src: s, tok: make([]int, n)}
		for i := range c.tok {
			c.tok[i] = -1
		}
		for bi, b := range s.Blocks {
			if exMatch[matchKey(s.ID, bi)] {
				continue
			}
			for i := b.From; i < b.ToTok && i < n; i++ {
				if !excluded[i] {
					c.tok[i] = bi
				}
			}
		}
		// Small-match filter applies to what is left of each passage.
		if f.ExcludeSmall > 0 {
			for i := 0; i < n; {
				if c.tok[i] < 0 {
					i++
					continue
				}
				j := i
				for j < n && c.tok[j] == c.tok[i] {
					j++
				}
				if j-i < f.ExcludeSmall {
					for k := i; k < j; k++ {
						c.tok[k] = -1
					}
				}
				i = j
			}
		}
		for _, v := range c.tok {
			if v >= 0 {
				c.total++
			}
		}
		if c.total == 0 {
			continue
		}
		if exSrc[s.ID] {
			excludedList = append(excludedList, RankedSource{ID: s.ID, URL: s.URL, Title: s.Title, Type: s.Type, Total: c.total, Excluded: true})
			continue
		}
		cands = append(cands, c)
	}

	// Coverage by source type, each counted on its own (so they can add up to more than the index).
	for _, typ := range []string{"Internet", "Publication", "Student Paper"} {
		cov := make([]bool, n)
		cnt := 0
		for _, c := range cands {
			if c.src.Type != typ {
				continue
			}
			for i, v := range c.tok {
				if v >= 0 && !cov[i] {
					cov[i] = true
					cnt++
				}
			}
		}
		rep.ByType[typ] = pct(cnt, n)
	}

	claimed := make([]int, n) // rank, 0 = unclaimed
	owner := make([]*cand, 0)
	used := make([]bool, len(cands))
	for {
		best, bestN := -1, 0
		for ci, c := range cands {
			if used[ci] {
				continue
			}
			k := 0
			for i, v := range c.tok {
				if v >= 0 && claimed[i] == 0 {
					k++
				}
			}
			if k > bestN || (k == bestN && k > 0 && best >= 0 && c.total > cands[best].total) {
				best, bestN = ci, k
			}
		}
		if best < 0 || bestN == 0 {
			break
		}
		used[best] = true
		owner = append(owner, cands[best])
		rank := len(owner)
		for i, v := range cands[best].tok {
			if v >= 0 && claimed[i] == 0 {
				claimed[i] = rank
			}
		}
	}
	matched := 0
	words := make([]int, len(owner)+1)
	for _, r := range claimed {
		if r > 0 {
			matched++
			words[r]++
		}
	}
	rep.MatchedWords = matched
	rep.IndexExact = 100 * float64(matched) / math.Max(1, float64(n))
	rep.Index = pct(matched, n)

	groupCount := make([]GroupStat, 4)
	for i := range groupCount {
		groupCount[i].Name = groupNames[i]
	}
	blocksPer := make([]int, len(owner)+1)
	for i := 0; i < n; {
		r := claimed[i]
		if r == 0 {
			i++
			continue
		}
		c := owner[r-1]
		bi := c.tok[i]
		j := i
		q, ct := 0, false
		for j < n && claimed[j] == r && c.tok[j] == bi && tokQuoted[j] == tokQuoted[i] {
			if tokQuoted[j] {
				q++
			}
			if tokCited[j] {
				ct = true
			}
			j++
		}
		quoted := q*2 > j-i
		g := 0
		switch {
		case quoted && ct:
			g = 3
		case quoted:
			g = 2
		case ct:
			g = 1
		}
		groupCount[g].Count++
		groupCount[g].Words += j - i
		blocksPer[r]++
		rep.Highlights = append(rep.Highlights, Highlight{Start: toks[i].Start, End: toks[j-1].End, Source: r, SrcID: c.src.ID, Block: bi, Group: groupNames[g], Words: j - i})
		i = j
	}
	for i := range groupCount {
		groupCount[i].Percent = round1(100 * float64(groupCount[i].Words) / math.Max(1, float64(n)))
	}
	rep.Groups = groupCount
	for r, c := range owner {
		rep.Sources = append(rep.Sources, RankedSource{Rank: r + 1, ID: c.src.ID, URL: c.src.URL, Title: c.src.Title, Type: c.src.Type,
			Words: words[r+1], Percent: round1(100 * float64(words[r+1]) / math.Max(1, float64(n))), Blocks: blocksPer[r+1], Total: c.total})
	}
	// Sources fully overshadowed by stronger ones are still listed, without a number.
	for ci, c := range cands {
		if !used[ci] {
			rep.Sources = append(rep.Sources, RankedSource{ID: c.src.ID, URL: c.src.URL, Title: c.src.Title, Type: c.src.Type, Total: c.total})
		}
	}
	sort.SliceStable(excludedList, func(a, b int) bool { return excludedList[a].Total > excludedList[b].Total })
	rep.Sources = append(rep.Sources, excludedList...)
	return rep
}

func matchKey(src, block int) string {
	return strings.Join([]string{itoa(src), itoa(block)}, ":")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func pct(a, b int) int {
	if b == 0 {
		return 0
	}
	return int(math.Round(100 * float64(a) / float64(b)))
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }
