package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

//go:embed assets/aimodel.bin.gz
var aiModelGz []byte

type aiModel struct {
	Mean, Std, WDense []float64
	WSparse           []float32
	Bias              float64
	// Sentence threshold on the calibrated probability, chosen for a low false-positive rate.
	Threshold float64
	ok        bool
}

var (
	modelOnce sync.Once
	model     aiModel
)

func loadModel() *aiModel {
	modelOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(aiModelGz))
		if err != nil {
			return
		}
		raw, err := io.ReadAll(zr)
		if err != nil || len(raw) < 16 {
			return
		}
		r := bytes.NewReader(raw)
		var nd, ns uint32
		binary.Read(r, binary.LittleEndian, &nd)
		binary.Read(r, binary.LittleEndian, &ns)
		if int(nd) != len(denseNames) || int(ns) != sparseDim {
			return
		}
		rd := func(n int) []float64 {
			f := make([]float32, n)
			binary.Read(r, binary.LittleEndian, f)
			o := make([]float64, n)
			for i, v := range f {
				o[i] = float64(v)
			}
			return o
		}
		model.Mean = rd(int(nd))
		model.Std = rd(int(nd))
		model.WDense = rd(int(nd))
		model.WSparse = make([]float32, ns)
		binary.Read(r, binary.LittleEndian, model.WSparse)
		tail := rd(2)
		model.Bias, model.Threshold = tail[0], tail[1]
		if v, err := strconv.ParseFloat(os.Getenv("ORIGINCHECK_AI_THRESHOLD"), 64); err == nil {
			model.Threshold = v
		}
		model.ok = true
	})
	return &model
}

func (m *aiModel) prob(text string) float64 {
	d, s := AIFeatures(text)
	z := m.Bias
	for i, v := range d {
		sd := m.Std[i]
		if sd == 0 {
			sd = 1
		}
		z += m.WDense[i] * (v - m.Mean[i]) / sd
	}
	for k, v := range s {
		z += float64(m.WSparse[k]) * v
	}
	return 1 / (1 + math.Exp(-z))
}

var bulletRe = regexp.MustCompile(`^\s*([-•*▪◦–]|\d{1,2}[.)]|[a-zA-Z][.)])\s+`)

// QualifyingSentences returns sentences of long-form prose, the only text Turnitin's AI detector scores.
// Headings, bullet lists, short lines, tables and the bibliography are left out.
func QualifyingSentences(text string, bibStart int) []Span {
	var out []Span
	for _, p := range Paragraphs(text) {
		if bibStart >= 0 && p.Start >= bibStart {
			break
		}
		seg := text[p.Start:p.End]
		words := len(Tokenize(seg))
		if bulletRe.MatchString(seg) && words < 40 {
			continue
		}
		if strings.Count(seg, "\t") >= 2 || strings.Count(seg, "|") >= 2 {
			continue
		}
		last := seg[len(seg)-1]
		if words < 12 && !strings.ContainsRune(".!?\"”'", rune(last)) {
			continue // heading
		}
		sents := Sentences(seg)
		if len(sents) < 2 && words < 25 {
			continue
		}
		for _, s := range sents {
			if len(Tokenize(seg[s.Start:s.End])) >= 4 {
				out = append(out, Span{p.Start + s.Start, p.Start + s.End})
			}
		}
	}
	return out
}

// AIWindows groups consecutive qualifying sentences into overlapping passages of about
// 150 words. Each window is a list of sentence indexes.
func AIWindows(text string, sents []Span) [][]int {
	const target = 150
	var wins [][]int
	n := len(sents)
	wc := make([]int, n)
	total := 0
	for i, s := range sents {
		wc[i] = len(Tokenize(text[s.Start:s.End]))
		total += wc[i]
	}
	if n == 0 {
		return nil
	}
	if total <= target*3/2 {
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		return [][]int{all}
	}
	i := 0
	for i < n {
		var w []int
		c := 0
		j := i
		for j < n && c < target {
			w = append(w, j)
			c += wc[j]
			j++
		}
		if c < target*2/3 && len(wins) > 0 {
			// Too short at the end: extend backwards instead.
			for k := w[0] - 1; k >= 0 && c < target; k-- {
				w = append([]int{k}, w...)
				c += wc[k]
			}
		}
		wins = append(wins, w)
		if j >= n {
			break
		}
		// Advance by about half a window.
		adv, a := 0, i
		for a < j && adv < target/2 {
			adv += wc[a]
			a++
		}
		if a == i {
			a = i + 1
		}
		i = a
	}
	return wins
}

func windowText(text string, sents []Span, w []int) string {
	var b strings.Builder
	for k, i := range w {
		if k > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text[sents[i].Start:sents[i].End])
	}
	return b.String()
}

// AIResult is the AI writing report for one document.
type AIResult struct {
	Available       bool     `json:"available"`
	Reason          string   `json:"reason,omitempty"`
	Percent         int      `json:"percent"`
	QualifyingWords int      `json:"qualifyingWords"`
	FlaggedWords    int      `json:"flaggedWords"`
	Sentences       []AISent `json:"sentences"`
}

type AISent struct {
	Start   int     `json:"start"`
	End     int     `json:"end"`
	Score   float64 `json:"score"`
	Flagged bool    `json:"flagged"`
}

func DetectAI(text string, bibStart int) AIResult {
	m := loadModel()
	if !m.ok {
		return AIResult{Reason: "The AI detection model is missing from this build."}
	}
	sents := QualifyingSentences(text, bibStart)
	res := AIResult{}
	words := make([]int, len(sents))
	for i, s := range sents {
		words[i] = len(Tokenize(text[s.Start:s.End]))
		res.QualifyingWords += words[i]
	}
	if res.QualifyingWords < 300 {
		res.Reason = "At least 300 words of prose (full sentences in paragraphs) are needed for an AI writing score."
		return res
	}
	if res.QualifyingWords > 30000 {
		res.Reason = "Documents over 30,000 words of prose are not scored."
		return res
	}
	res.Available = true
	sum := make([]float64, len(sents))
	cnt := make([]float64, len(sents))
	for _, w := range AIWindows(text, sents) {
		p := m.prob(windowText(text, sents, w))
		for _, i := range w {
			sum[i] += p
			cnt[i]++
		}
	}
	for i, s := range sents {
		sc := sum[i] / math.Max(cnt[i], 1)
		fl := sc >= m.Threshold
		if fl {
			res.FlaggedWords += words[i]
		}
		res.Sentences = append(res.Sentences, AISent{Start: s.Start, End: s.End, Score: math.Round(sc*1000) / 1000, Flagged: fl})
	}
	// Isolated flagged sentences between human ones are the most error-prone: require
	// flagged text to come in runs of at least two sentences, like Turnitin's segment view.
	for i := range res.Sentences {
		if !res.Sentences[i].Flagged {
			continue
		}
		prev := i > 0 && res.Sentences[i-1].Flagged
		next := i+1 < len(res.Sentences) && res.Sentences[i+1].Flagged
		if !prev && !next {
			res.Sentences[i].Flagged = false
			res.FlaggedWords -= words[i]
		}
	}
	res.Percent = int(math.Round(float64(res.FlaggedWords) * 100 / float64(res.QualifyingWords)))
	return res
}
