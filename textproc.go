package main

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token is one word of a document, with its byte offsets in the document text.
type Token struct {
	Norm       string // lowercase, homoglyph-folded form used for matching
	Start, End int
}

// Span is a byte range [Start, End) in the document text.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// homoglyphs maps look-alike letters from other scripts to the Latin letter they imitate.
// Students sometimes swap these in to defeat similarity checkers.
var homoglyphs = map[rune]rune{
	'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'у': 'y', 'х': 'x', 'і': 'i', 'ј': 'j', 'ѕ': 's', 'ԁ': 'd', 'ɡ': 'g', 'һ': 'h', 'ӏ': 'l', 'ԛ': 'q', 'ԝ': 'w',
	'А': 'A', 'В': 'B', 'Е': 'E', 'К': 'K', 'М': 'M', 'Н': 'H', 'О': 'O', 'Р': 'P', 'С': 'C', 'Т': 'T', 'Х': 'X', 'У': 'Y', 'І': 'I', 'Ј': 'J', 'Ѕ': 'S',
	'α': 'a', 'ο': 'o', 'ρ': 'p', 'ν': 'v', 'ι': 'i', 'κ': 'k', 'τ': 't', 'υ': 'u',
	'Α': 'A', 'Β': 'B', 'Ε': 'E', 'Ζ': 'Z', 'Η': 'H', 'Ι': 'I', 'Κ': 'K', 'Μ': 'M', 'Ν': 'N', 'Ο': 'O', 'Ρ': 'P', 'Τ': 'T', 'Υ': 'Y', 'Χ': 'X',
}

func isZeroWidth(r rune) bool {
	switch r {
	case '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff', '\u00ad':
		return true
	}
	return false
}

func isLatinLetter(r rune) bool {
	return r < 0x250 && unicode.IsLetter(r)
}

// Tokenize splits text into word tokens. Letters from other scripts that imitate Latin letters
// are folded when they appear inside an otherwise Latin word, and zero-width characters are skipped.
func Tokenize(text string) []Token {
	var toks []Token
	var b strings.Builder
	start := -1
	flush := func(end int) {
		if start >= 0 && b.Len() > 0 {
			w := strings.Trim(b.String(), "'")
			if w != "" {
				toks = append(toks, Token{Norm: w, Start: start, End: end})
			}
		}
		b.Reset()
		start = -1
	}
	for i, r := range text {
		if isZeroWidth(r) {
			continue
		}
		if g, ok := homoglyphs[r]; ok {
			r = g
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		if (r == '\'' || r == '’') && start >= 0 {
			b.WriteRune('\'')
			continue
		}
		flush(i)
	}
	flush(len(text))
	return toks
}

// ReplacedChars counts look-alike characters from other scripts used inside Latin words,
// and invisible zero-width characters used inside words.
func ReplacedChars(text string) (spans []Span) {
	runes := []rune(text)
	offs := make([]int, len(runes)+1)
	o := 0
	for i, r := range runes {
		offs[i] = o
		o += utf8.RuneLen(r)
	}
	offs[len(runes)] = o
	for i, r := range runes {
		_, glyph := homoglyphs[r]
		zw := isZeroWidth(r) && r != '\ufeff' && r != '\u00ad'
		if !glyph && !zw {
			continue
		}
		// Only suspicious when a neighbour is a Latin letter.
		prevLatin := i > 0 && isLatinLetter(runes[i-1])
		nextLatin := i+1 < len(runes) && isLatinLetter(runes[i+1])
		if prevLatin || nextLatin {
			spans = append(spans, Span{offs[i], offs[i+1]})
		}
	}
	return spans
}

var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true, "sr": true, "jr": true, "st": true, "vs": true,
	"etc": true, "e.g": true, "i.e": true, "al": true, "fig": true, "no": true, "vol": true, "pp": true, "p": true,
	"ed": true, "eds": true, "inc": true, "ltd": true, "co": true, "u.s": true, "u.k": true, "approx": true, "cf": true,
	"jan": true, "feb": true, "mar": true, "apr": true, "jun": true, "jul": true, "aug": true, "sep": true, "sept": true,
	"oct": true, "nov": true, "dec": true, "ch": true, "sec": true, "dept": true, "gen": true, "gov": true, "univ": true,
}

// Sentences splits text into sentence spans. Newlines always end a sentence.
func Sentences(text string) []Span {
	var out []Span
	start := -1
	n := len(text)
	emit := func(end int) {
		if start < 0 {
			return
		}
		s, e := start, end
		for s < e && isSpaceByte(text[s]) {
			s++
		}
		for e > s && isSpaceByte(text[e-1]) {
			e--
		}
		if e > s {
			out = append(out, Span{s, e})
		}
		start = -1
	}
	for i := 0; i < n; i++ {
		c := text[i]
		if start < 0 && !isSpaceByte(c) {
			start = i
		}
		if c == '\n' {
			emit(i)
			continue
		}
		if c == '.' || c == '!' || c == '?' {
			// swallow closing quotes/brackets and repeated punctuation
			j := i + 1
			for j < n && strings.IndexByte(".!?\"')]", text[j]) >= 0 {
				j++
			}
			for j+2 < n && (text[j:j+3] == "”" || text[j:j+3] == "’") {
				j += 3
			}
			if j < n && !isSpaceByte(text[j]) {
				i = j - 1
				continue
			}
			if c == '.' {
				// abbreviation or initial?
				k := i - 1
				for k >= 0 && (isAlnumByte(text[k]) || text[k] == '.') {
					k--
				}
				word := strings.ToLower(text[k+1 : i])
				if abbreviations[word] || (len(word) == 1 && word[0] >= 'a' && word[0] <= 'z') {
					i = j - 1
					continue
				}
				// next word lowercase => probably not a sentence end
				m := j
				for m < n && isSpaceByte(text[m]) && text[m] != '\n' {
					m++
				}
				if m < n && text[m] >= 'a' && text[m] <= 'z' {
					i = j - 1
					continue
				}
			}
			emit(j)
			i = j - 1
		}
	}
	emit(n)
	return out
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
func isAlnumByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// Paragraphs splits on line breaks.
func Paragraphs(text string) []Span {
	var out []Span
	s := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == '\n' {
			a, b := s, i
			for a < b && isSpaceByte(text[a]) {
				a++
			}
			for b > a && isSpaceByte(text[b-1]) {
				b--
			}
			if b > a {
				out = append(out, Span{a, b})
			}
			s = i + 1
		}
	}
	return out
}

var bibHeading = regexp.MustCompile(`(?im)^[ \t]*(references?|reference list|bibliography|works cited|works consulted|literature cited|sources( cited)?|citations|endnotes)[ \t]*:?[ \t]*$`)

// BibliographyStart returns the byte offset where a references section starts, or -1.
// Only headings in the second half of the document count, like Turnitin.
func BibliographyStart(text string) int {
	locs := bibHeading.FindAllStringIndex(text, -1)
	for i := len(locs) - 1; i >= 0; i-- {
		if locs[i][0] >= len(text)*2/5 {
			return locs[i][0]
		}
	}
	return -1
}

// QuoteSpans finds text inside quotation marks (straight or curly), within one paragraph.
func QuoteSpans(text string) []Span {
	var out []Span
	for _, p := range Paragraphs(text) {
		seg := text[p.Start:p.End]
		open := -1
		for i, r := range seg {
			switch r {
			case '“', '«':
				open = i
			case '”', '»':
				if open >= 0 {
					out = append(out, Span{p.Start + open, p.Start + i + utf8.RuneLen(r)})
					open = -1
				}
			case '"':
				if open >= 0 {
					out = append(out, Span{p.Start + open, p.Start + i + 1})
					open = -1
				} else {
					open = i
				}
			}
		}
	}
	return out
}

var citationRe = regexp.MustCompile(`\([^()]{0,120}?(1[5-9]\d\d|20\d\d|n\.d\.)[a-z]?[^()]{0,40}\)|\[\d+(\s*[,–-]\s*\d+)*\]|\(\s*(ibid|ibid\.|op\. cit\.)[^)]*\)|\b(according to|as stated by|as noted by|as cited in)\b`)

// CitedSentences returns sentence spans that contain an in-text citation.
func CitedSentences(text string, sents []Span) []bool {
	out := make([]bool, len(sents))
	for i, s := range sents {
		seg := text[s.Start:s.End]
		if citationRe.MatchString(strings.ToLower(seg)) || citationRe.MatchString(seg) {
			out[i] = true
			continue
		}
		// A citation that opens the next sentence, e.g. `... end." (Smith, 2020).` split oddly.
		if i+1 < len(sents) {
			nx := text[sents[i+1].Start:sents[i+1].End]
			if len(nx) < 40 && citationRe.MatchString(nx) {
				out[i] = true
			}
		}
	}
	return out
}

func inSpans(pos int, spans []Span) bool {
	for _, s := range spans {
		if pos >= s.Start && pos < s.End {
			return true
		}
	}
	return false
}

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the and or but if then else of to in on at by for with from as is are was were be been being it its this that these those there their they them he she his her him we our us you your i me my not no so than too very can could will would shall should may might must do does did done have has had having into onto over under about above below up down out off again further once here when where why how all any both each few more most other some such only own same just also which who whom whose what while during before after between through because until against among per via`) {
		m[w] = true
	}
	return m
}()
