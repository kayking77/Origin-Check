package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// runEvaluate scores labelled documents (JSON lines on stdin) with the full AI report and
// prints, per group, how often the document score reaches Turnitin's 20% display threshold.
func runEvaluate() {
	type in struct {
		ID    string `json:"id"`
		Label int    `json:"label"`
		Group string `json:"group"`
		Text  string `json:"text"`
	}
	type agg struct{ n, avail, ge20, any, sum int }
	groups := map[string]*agg{}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var d in
		if json.Unmarshal(sc.Bytes(), &d) != nil {
			continue
		}
		a := groups[d.Group]
		if a == nil {
			a = &agg{}
			groups[d.Group] = a
		}
		a.n++
		r := DetectAI(d.Text, BibliographyStart(d.Text))
		if !r.Available {
			continue
		}
		a.avail++
		a.sum += r.Percent
		if r.Percent >= 20 {
			a.ge20++
		}
		if r.Percent > 0 {
			a.any++
		}
	}
	var keys []string
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("%-32s %6s %6s %8s %8s %8s\n", "group", "docs", "scored", ">=20%", ">0%", "mean%")
	for _, k := range keys {
		a := groups[k]
		if a.avail == 0 {
			fmt.Printf("%-32s %6d %6d\n", k, a.n, 0)
			continue
		}
		fmt.Printf("%-32s %6d %6d %7.1f%% %7.1f%% %7.1f\n", k, a.n, a.avail, 100*float64(a.ge20)/float64(a.avail), 100*float64(a.any)/float64(a.avail), float64(a.sum)/float64(a.avail))
	}
}
