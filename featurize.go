package main

import (
	"bufio"
	"encoding/json"
	"os"
	"runtime"
	"sync"
)

// runFeaturize turns labelled documents (JSON lines on stdin) into detector features
// (JSON lines on stdout), using exactly the windowing the app uses. Used to train the model.
func runFeaturize() {
	type in struct {
		ID    string `json:"id"`
		Label int    `json:"label"`
		Group string `json:"group"`
		Text  string `json:"text"`
	}
	type out struct {
		ID    string    `json:"id"`
		Label int       `json:"label"`
		Group string    `json:"group"`
		Win   int       `json:"win"`
		Words int       `json:"words"`
		Dense []float64 `json:"dense"`
		SI    []uint32  `json:"si"`
		SV    []float64 `json:"sv"`
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	jobs := make(chan in)
	res := make(chan []out)
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				sents := QualifyingSentences(d.Text, BibliographyStart(d.Text))
				var outs []out
				for wi, win := range AIWindows(d.Text, sents) {
					t := windowText(d.Text, sents, win)
					dense, sp := AIFeatures(t)
					o := out{ID: d.ID, Label: d.Label, Group: d.Group, Win: wi, Words: len(Tokenize(t)), Dense: dense}
					for k, v := range sp {
						o.SI = append(o.SI, k)
						o.SV = append(o.SV, v)
					}
					outs = append(outs, o)
				}
				res <- outs
			}
		}()
	}
	go func() {
		for sc.Scan() {
			var d in
			if json.Unmarshal(sc.Bytes(), &d) == nil {
				jobs <- d
			}
		}
		close(jobs)
		wg.Wait()
		close(res)
	}()
	bw := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(bw)
	for outs := range res {
		for _, o := range outs {
			enc.Encode(o)
		}
	}
	bw.Flush()
}
