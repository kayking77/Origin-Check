package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"sync"
)

//go:embed assets/wordfreq_en.tsv.gz
var wordfreqGz []byte

var (
	zipfOnce sync.Once
	zipfMap  map[string]float64
)

func zipf(w string) float64 {
	zipfOnce.Do(func() {
		zipfMap = make(map[string]float64, 130000)
		zr, err := gzip.NewReader(bytes.NewReader(wordfreqGz))
		if err != nil {
			return
		}
		sc := bufio.NewScanner(zr)
		for sc.Scan() {
			line := sc.Text()
			if i := strings.IndexByte(line, '\t'); i > 0 {
				v, _ := strconv.ParseFloat(line[i+1:], 64)
				zipfMap[line[:i]] = v
			}
		}
	})
	if v, ok := zipfMap[w]; ok {
		return v
	}
	if i := strings.IndexByte(w, '\''); i > 0 {
		if v, ok := zipfMap[w[:i]]; ok {
			return v
		}
	}
	return 0
}

// Words that chat assistants use far more often than students do.
var aiMarkers = map[string]bool{}
var transitions = map[string]bool{}
var firstPerson = map[string]bool{"i": true, "me": true, "my": true, "mine": true, "myself": true, "we": true, "our": true, "us": true, "ours": true}
var secondPerson = map[string]bool{"you": true, "your": true, "yours": true, "yourself": true}

func init() {
	for _, w := range strings.Fields(`delve delves delving tapestry intricate intricacies multifaceted pivotal crucial vital
		foster fosters fostering underscore underscores underscoring highlight highlights highlighting showcase showcasing
		realm landscape nuanced nuances navigate navigating testament profound seamless seamlessly robust comprehensive
		enhance enhancing enhances holistic paramount leverage leveraging embark resonate resonates interplay unwavering
		notably additionally furthermore moreover ultimately overall essentially significantly invaluable meticulous
		meticulously commendable noteworthy transformative endeavor endeavors realm vibrant bustling indelible poignant
		complexities encompasses encompassing facilitate facilitates garner harness harnessing myriad plethora strive
		striving thereby insightful compelling pinnacle cornerstone`) {
		aiMarkers[w] = true
	}
	for _, w := range strings.Fields(`additionally furthermore moreover however therefore thus consequently overall ultimately
		in conclusion similarly likewise nevertheless nonetheless firstly secondly lastly finally importantly notably
		conversely meanwhile subsequently hence`) {
		transitions[w] = true
	}
}

const sparseBits = 18
const sparseDim = 1 << sparseBits

var denseNames = []string{
	"zipf_mean", "zipf_std", "z_ge6", "z_5_6", "z_4_5", "z_3_4", "z_lt3", "oov",
	"sent_mean", "sent_std", "sent_cv", "sent_max_ratio", "short_sent", "long_sent",
	"wordlen_mean", "wordlen_std", "mattr",
	"comma", "semicolon", "colon", "dash", "paren", "question", "exclaim", "quote",
	"contraction", "first_person", "second_person", "ai_marker", "transition_start",
	"digit", "capital", "lower_start", "conj_start", "rep_start", "adj_sent_diff",
}

func hashFeat(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32() & (sparseDim - 1)
}

// AIFeatures computes the detector's input for one passage of prose.
func AIFeatures(text string) (dense []float64, sparse map[uint32]float64) {
	toks := Tokenize(text)
	sents := Sentences(text)
	nw := float64(len(toks))
	if nw < 1 {
		nw = 1
	}
	dense = make([]float64, len(denseNames))

	// Word frequency profile (a unigram stand-in for GLTR's rank buckets).
	var zs []float64
	var zsum, zsq float64
	var b6, b5, b4, b3, blt3, oov float64
	var wl, wlsq float64
	var contr, fp, sp, marker, digit float64
	for _, t := range toks {
		z := zipf(t.Norm)
		if z == 0 && !isNumber(t.Norm) {
			oov++
		}
		zs = append(zs, z)
		zsum += z
		zsq += z * z
		switch {
		case z >= 6:
			b6++
		case z >= 5:
			b5++
		case z >= 4:
			b4++
		case z >= 3:
			b3++
		default:
			blt3++
		}
		l := float64(len([]rune(t.Norm)))
		wl += l
		wlsq += l * l
		if strings.ContainsRune(t.Norm, '\'') {
			if strings.HasSuffix(t.Norm, "n't") || strings.HasSuffix(t.Norm, "'re") || strings.HasSuffix(t.Norm, "'m") || strings.HasSuffix(t.Norm, "'ll") || strings.HasSuffix(t.Norm, "'ve") || strings.HasSuffix(t.Norm, "'d") {
				contr++
			}
		}
		if firstPerson[t.Norm] {
			fp++
		}
		if secondPerson[t.Norm] {
			sp++
		}
		if aiMarkers[t.Norm] {
			marker++
		}
		if isNumber(t.Norm) {
			digit++
		}
	}
	zm := zsum / nw
	dense[0] = zm
	dense[1] = math.Sqrt(math.Max(0, zsq/nw-zm*zm))
	dense[2], dense[3], dense[4], dense[5], dense[6] = b6/nw, b5/nw, b4/nw, b3/nw, blt3/nw
	dense[7] = oov / nw

	// Sentence length rhythm ("burstiness").
	var lens []float64
	var capital, lowerStart, conjStart, transStart float64
	firstWords := map[string]int{}
	for _, s := range sents {
		st := Tokenize(text[s.Start:s.End])
		if len(st) == 0 {
			continue
		}
		lens = append(lens, float64(len(st)))
		c := text[s.Start]
		if c >= 'a' && c <= 'z' {
			lowerStart++
		}
		fw := st[0].Norm
		firstWords[fw]++
		if fw == "and" || fw == "but" || fw == "so" || fw == "or" || fw == "because" {
			conjStart++
		}
		if transitions[fw] || (len(st) > 1 && transitions[fw+" "+st[1].Norm]) {
			transStart++
		}
		for _, t := range st[1:] {
			r := text[t.Start]
			if r >= 'A' && r <= 'Z' {
				capital++
			}
		}
	}
	ns := float64(len(lens))
	if ns < 1 {
		ns = 1
	}
	var lm, lsq, lmax, lmin, short, long, adj float64
	lmin = 1e9
	for i, l := range lens {
		lm += l
		lsq += l * l
		lmax = math.Max(lmax, l)
		lmin = math.Min(lmin, l)
		if l < 8 {
			short++
		}
		if l > 35 {
			long++
		}
		if i > 0 {
			adj += math.Abs(l - lens[i-1])
		}
	}
	lm /= ns
	lstd := math.Sqrt(math.Max(0, lsq/ns-lm*lm))
	dense[8] = lm / 20
	dense[9] = lstd / 10
	if lm > 0 {
		dense[10] = lstd / lm
	}
	if lmin > 0 && lmin < 1e9 {
		dense[11] = math.Log(lmax / lmin)
	}
	dense[12] = short / ns
	dense[13] = long / ns
	wlm := wl / nw
	dense[14] = wlm
	dense[15] = math.Sqrt(math.Max(0, wlsq/nw-wlm*wlm))
	dense[16] = mattr(toks, 40)

	// Punctuation habits.
	cnt := func(s string) float64 { return float64(strings.Count(text, s)) }
	dense[17] = cnt(",") / nw * 10
	dense[18] = cnt(";") / nw * 10
	dense[19] = cnt(":") / nw * 10
	dense[20] = (cnt("—") + cnt("–") + cnt(" - ") + cnt("--")) / nw * 10
	dense[21] = cnt("(") / nw * 10
	dense[22] = cnt("?") / nw * 10
	dense[23] = cnt("!") / nw * 10
	dense[24] = (cnt("\"") + cnt("“") + cnt("”")) / nw * 10
	dense[25] = contr / nw * 10
	dense[26] = fp / nw * 10
	dense[27] = sp / nw * 10
	dense[28] = marker / nw * 10
	dense[29] = transStart / ns
	dense[30] = digit / nw * 10
	dense[31] = capital / nw * 10
	dense[32] = lowerStart / ns
	dense[33] = conjStart / ns
	var rep float64
	for _, c := range firstWords {
		if c > 1 {
			rep += float64(c - 1)
		}
	}
	dense[34] = rep / ns
	if ns > 1 {
		dense[35] = adj / (ns - 1) / 10
	}

	// Word and word-pair counts, hashed.
	sparse = map[uint32]float64{}
	for i, t := range toks {
		sparse[hashFeat("u:"+t.Norm)]++
		if i > 0 {
			sparse[hashFeat("b:"+toks[i-1].Norm+" "+t.Norm)]++
		}
	}
	// Punctuation tokens and sentence starts carry style too.
	for _, p := range []string{",", ";", ":", "—", "(", "?", "!", "\""} {
		if c := cnt(p); c > 0 {
			sparse[hashFeat("p:"+p)] += c
		}
	}
	for w, c := range firstWords {
		sparse[hashFeat("s:"+w)] += float64(c)
	}
	var norm float64
	for k, v := range sparse {
		v = math.Log1p(v)
		sparse[k] = v
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for k := range sparse {
			sparse[k] /= norm
		}
	}
	return dense, sparse
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// mattr is the moving-average type/token ratio, which does not depend on passage length.
func mattr(toks []Token, w int) float64 {
	if len(toks) == 0 {
		return 0
	}
	if len(toks) <= w {
		seen := map[string]bool{}
		for _, t := range toks {
			seen[t.Norm] = true
		}
		return float64(len(seen)) / float64(len(toks))
	}
	var sum float64
	var n int
	for i := 0; i+w <= len(toks); i += 5 {
		seen := map[string]bool{}
		for _, t := range toks[i : i+w] {
			seen[t.Norm] = true
		}
		sum += float64(len(seen)) / float64(w)
		n++
	}
	return sum / float64(n)
}
