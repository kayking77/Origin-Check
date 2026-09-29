package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RepoIndex is the local "student paper repository": every stored submission, indexed by
// word shingles so a new paper finds earlier papers it overlaps with quickly.
type RepoIndex struct {
	mu     sync.RWMutex
	grams  map[uint64][]string
	texts  map[string]string
	hashes map[string][]uint64
}

func NewRepoIndex() *RepoIndex {
	return &RepoIndex{grams: map[uint64][]string{}, texts: map[string]string{}, hashes: map[string][]uint64{}}
}

func docShingles(toks []Token) []uint64 {
	seen := map[uint64]bool{}
	var out []uint64
	for i := 0; i+seedK <= len(toks); i++ {
		h := shingleHash(toks, i, seedK)
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func (r *RepoIndex) Add(id, text string) {
	hs := docShingles(Tokenize(text))
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.texts[id]; ok {
		return
	}
	r.texts[id] = text
	r.hashes[id] = hs
	for _, h := range hs {
		r.grams[h] = append(r.grams[h], id)
	}
}

func (r *RepoIndex) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.hashes[id] {
		ids := r.grams[h]
		for i, x := range ids {
			if x == id {
				ids = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		if len(ids) == 0 {
			delete(r.grams, h)
		} else {
			r.grams[h] = ids
		}
	}
	delete(r.hashes, id)
	delete(r.texts, id)
}

// Candidates returns repository papers that share at least a few shingles with toks.
func (r *RepoIndex) Candidates(toks []Token, exclude string) map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := map[string]int{}
	for _, h := range docShingles(toks) {
		ids := r.grams[h]
		if len(ids) > 200 {
			continue // boilerplate everyone shares
		}
		for _, id := range ids {
			count[id]++
		}
	}
	out := map[string]string{}
	for id, c := range count {
		if id != exclude && c >= 2 {
			out[id] = r.texts[id]
		}
	}
	return out
}

func (r *RepoIndex) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.texts)
}

// Checker runs queued submissions one at a time in the background.
type Checker struct {
	st    *Store
	queue chan string
}

func NewChecker(st *Store) *Checker {
	c := &Checker{st: st, queue: make(chan string, 10000)}
	go c.loop()
	for _, s := range st.Summaries("") {
		if s.Status == "queued" {
			c.queue <- s.ID
		}
	}
	return c
}

func (c *Checker) Enqueue(id string) {
	c.st.SetProgress(id, "queued", "Waiting to be checked")
	c.queue <- id
}

func (c *Checker) loop() {
	for id := range c.queue {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("check %s crashed: %v", id, r)
					c.st.Update(id, func(s *Submission) { s.Status = "error"; s.Error = fmt.Sprint("Internal error: ", r) })
				}
			}()
			c.run(id)
		}()
	}
}

func (c *Checker) run(id string) {
	sub, ok := c.st.Get(id)
	if !ok {
		return
	}
	progress := func(msg string) { c.st.SetProgress(id, "checking", msg) }
	progress("Reading the document")
	text := sub.Text
	toks := Tokenize(text)
	bib := BibliographyStart(text)
	var notes []string

	// AI writing detection runs locally and is quick.
	progress("Checking for AI-generated writing")
	ai := DetectAI(text, bib)

	var sources []SourceMatch
	nextID := 1
	addSource := func(u, title, typ, ref, srcText string) {
		blocks := MatchTexts(toks, srcText)
		if len(blocks) == 0 {
			return
		}
		sources = append(sources, SourceMatch{ID: nextID, URL: u, Title: title, Type: typ, Ref: ref, Blocks: blocks})
		nextID++
	}

	// 1. Student paper repository.
	if sub.Options.Repository {
		progress("Comparing with papers in your repository")
		for rid, rtext := range c.st.repo.Candidates(toks, sub.ID) {
			other, ok := c.st.Get(rid)
			title := "Student paper"
			if ok {
				a, _ := c.st.Assignment(other.Assignment)
				title = fmt.Sprintf("Submitted to %s: %s", firstNonEmpty(a.Name, "an assignment"), firstNonEmpty(other.Author, "unknown student"))
				if other.Title != "" {
					title += " (" + other.Title + ")"
				}
				title += " on " + other.Uploaded.Format("2006-01-02")
			}
			addSource("", title, "Student Paper", rid, rtext)
		}
	}

	// 2. Internet and publications.
	if sub.Options.Web || sub.Options.Publications {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		found, webNotes := c.searchSources(ctx, sub, text, toks, bib, progress)
		cancel()
		notes = append(notes, webNotes...)
		progress("Comparing with sources found online")
		for _, f := range found {
			addSource(f.URL, f.Title, f.Type, "", f.Text)
		}
	}

	// Merge sources that are the same page reached by different URLs.
	sources = dedupeSources(sources)

	st := c.st
	st.Update(id, func(s *Submission) {
		s.Sources = sources
		s.AI = ai
		s.Notes = notes
		s.Status = "done"
		s.Progress = ""
		s.Error = ""
		s.Checked = time.Now()
		s.Index = BuildReport(s.Text, s.Sources, s.Filters).Index
		if s.Options.Store && !s.InRepo {
			s.InRepo = true
		}
	})
	if sub.Options.Store {
		st.repo.Add(sub.ID, sub.Text)
	}
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func dedupeSources(in []SourceMatch) []SourceMatch {
	seen := map[string]int{}
	var out []SourceMatch
	for _, s := range in {
		key := s.Ref
		if key == "" {
			key = normalizeURL(s.URL)
		}
		if i, ok := seen[key]; ok {
			if len(s.Blocks) > len(out[i].Blocks) {
				s.ID = out[i].ID
				out[i] = s
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, s)
	}
	return out
}

func normalizeURL(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	pu.Fragment = ""
	pu.Scheme = "https"
	pu.Host = strings.TrimPrefix(strings.ToLower(pu.Host), "www.")
	pu.Host = strings.TrimPrefix(pu.Host, "m.")
	q := pu.Query()
	for k := range q {
		if strings.HasPrefix(k, "utm_") || k == "fbclid" || k == "gclid" {
			q.Del(k)
		}
	}
	pu.RawQuery = q.Encode()
	return strings.TrimSuffix(pu.String(), "/")
}

// pickQueries chooses sentences to search for: spread across the document, favouring
// sentences with distinctive words (common filler sentences find nothing useful).
func pickQueries(text string, bib, max int) []string {
	type cand struct {
		q     string
		score float64
		pos   int
	}
	var cands []cand
	for _, s := range Sentences(text) {
		if bib >= 0 && s.Start >= bib {
			break
		}
		seg := text[s.Start:s.End]
		st := Tokenize(seg)
		if len(st) < 8 {
			continue
		}
		// Use up to 14 consecutive words from the middle of the sentence.
		a := 0
		if len(st) > 14 {
			a = (len(st) - 14) / 2
		}
		b := a + 14
		if b > len(st) {
			b = len(st)
		}
		var words []string
		var rare float64
		for _, t := range st[a:b] {
			w := seg[t.Start:t.End] // token offsets are relative to the sentence
			words = append(words, w)
			z := zipf(t.Norm)
			if !stopwords[t.Norm] {
				rare += math.Max(0, 6-z)
			}
		}
		cands = append(cands, cand{strings.Join(words, " "), rare / float64(len(words)), len(cands)})
	}
	if len(cands) <= max {
		var out []string
		for _, c := range cands {
			out = append(out, c.q)
		}
		return out
	}
	// Split into max buckets in document order and take the most distinctive sentence of each.
	var out []string
	for k := 0; k < max; k++ {
		lo, hi := k*len(cands)/max, (k+1)*len(cands)/max
		best := lo
		for i := lo; i < hi; i++ {
			if cands[i].score > cands[best].score {
				best = i
			}
		}
		out = append(out, cands[best].q)
	}
	return out
}

func topKeywords(toks []Token, n int) []string {
	cnt := map[string]int{}
	for _, t := range toks {
		if stopwords[t.Norm] || len(t.Norm) < 4 || isNumber(t.Norm) {
			continue
		}
		cnt[t.Norm]++
	}
	type kv struct {
		w string
		s float64
	}
	var kvs []kv
	for w, c := range cnt {
		z := zipf(w)
		if z == 0 {
			z = 2
		}
		kvs = append(kvs, kv{w, float64(c) * (7 - z)})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].s > kvs[j].s })
	var out []string
	for i := 0; i < n && i < len(kvs); i++ {
		out = append(out, kvs[i].w)
	}
	return out
}

var skipHosts = []string{"youtube.com", "facebook.com", "instagram.com", "tiktok.com", "twitter.com", "x.com", "pinterest.com", "linkedin.com", "amazon.", "ebay."}

func (c *Checker) searchSources(ctx context.Context, sub *Submission, text string, toks []Token, bib int, progress func(string)) ([]fetchedSource, []string) {
	set := c.st.Settings()
	var notes []string
	var mu sync.Mutex
	var found []fetchedSource

	// For offline testing: treat text files in a folder as the web.
	if dir := os.Getenv("ORIGINCHECK_MOCK_WEB"); dir != "" {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			u := "https://example.org/" + e.Name()
			found = append(found, fetchedSource{URL: u, Title: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), Type: classifySource(u), Text: string(b)})
		}
		return found, notes
	}

	// Keyless scholarly and encyclopedia APIs.
	kw := strings.Join(topKeywords(toks, 6), " ")
	queries := pickQueries(text, bib, set.MaxQueries)
	if sub.Options.Web {
		progress("Searching Wikipedia")
		if ws, err := wikipediaSearch(ctx, kw, 4); err == nil {
			found = append(found, ws...)
		} else {
			notes = append(notes, explainErr("Wikipedia", err))
		}
		for i, q := range queries {
			if i >= 3 {
				break
			}
			if ws, err := wikipediaSearch(ctx, q, 2); err == nil {
				found = append(found, ws...)
			}
		}
	}
	if sub.Options.Publications {
		progress("Searching published papers and research databases")
		pq := []string{kw}
		for i, q := range queries {
			if i%4 == 0 && len(pq) < 6 {
				pq = append(pq, q)
			}
		}
		type db struct {
			name string
			fn   func(context.Context, string) ([]fetchedSource, error)
		}
		dbs := []db{
			{"OpenAlex", func(c context.Context, q string) ([]fetchedSource, error) {
				return openAlexSearch(c, q, 8, set.OpenAlexKey)
			}},
			{"Semantic Scholar", func(c context.Context, q string) ([]fetchedSource, error) {
				return semanticScholarSearch(c, q, 8, set.SemanticScholarKey)
			}},
			{"Crossref", func(c context.Context, q string) ([]fetchedSource, error) { return crossrefSearch(c, q, 8) }},
			{"Europe PMC", func(c context.Context, q string) ([]fetchedSource, error) { return europePMCSearch(c, q, 6) }},
			{"arXiv", func(c context.Context, q string) ([]fetchedSource, error) { return arxivSearch(c, q, 5) }},
		}
		if set.CoreKey != "" {
			dbs = append(dbs, db{"CORE", func(c context.Context, q string) ([]fetchedSource, error) { return coreSearch(c, q, 5, set.CoreKey) }})
		}
		var wg sync.WaitGroup
		for _, d := range dbs {
			wg.Add(1)
			go func(d db) {
				defer wg.Done()
				var lastErr error
				ok := false
				for _, q := range pq {
					ps, err := d.fn(ctx, q)
					if err != nil {
						lastErr = err
						if strings.Contains(err.Error(), "HTTP 4") {
							break // refused: asking again won't help
						}
						continue
					}
					ok = true
					mu.Lock()
					found = append(found, ps...)
					mu.Unlock()
				}
				if !ok && lastErr != nil {
					mu.Lock()
					notes = append(notes, explainErr(d.name, lastErr))
					mu.Unlock()
				}
			}(d)
		}
		wg.Wait()
	}

	// General web search.
	type urlInfo struct {
		hits     int
		title    string
		snippets []string
	}
	urls := map[string]*urlInfo{}
	if sub.Options.Web && len(queries) > 0 {
		se := &Searcher{Settings: set}
		var done, failed int
		var lastErr error
		work := make(chan string)
		var wg sync.WaitGroup
		for w := 0; w < 3; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for q := range work {
					hits, err := se.Search(ctx, q)
					mu.Lock()
					done++
					if err != nil {
						failed++
						lastErr = err
					}
					for i, h := range hits {
						if i >= 6 {
							break
						}
						skip := false
						for _, sh := range skipHosts {
							if strings.Contains(hostOf(h.URL), sh) {
								skip = true
							}
						}
						if skip {
							continue
						}
						k := normalizeURL(h.URL)
						ui := urls[k]
						if ui == nil {
							ui = &urlInfo{title: h.Title}
							urls[k] = ui
						}
						ui.hits++
						if h.Snippet != "" {
							ui.snippets = append(ui.snippets, h.Snippet)
						}
					}
					progress(fmt.Sprintf("Searching the web (%d of %d searches)", done, len(queries)))
					mu.Unlock()
				}
			}()
		}
		for _, q := range queries {
			select {
			case work <- q:
			case <-ctx.Done():
			}
		}
		close(work)
		wg.Wait()
		if failed == len(queries) {
			msg := "Web search did not work for this check"
			if lastErr != nil {
				msg += " (" + shortErr(lastErr) + ")"
			}
			notes = append(notes, msg+". Only the repository, Wikipedia and publications were compared. Adding a free Brave Search key in Settings makes web search reliable.")
		} else if failed > len(queries)/3 {
			notes = append(notes, fmt.Sprintf("%d of %d web searches failed, so some internet sources may be missing. Adding a free Brave Search key in Settings makes web search reliable.", failed, len(queries)))
		}
	}

	// Fetch the most promising pages.
	type ranked struct {
		u  string
		ui *urlInfo
	}
	var rs []ranked
	for u, ui := range urls {
		rs = append(rs, ranked{u, ui})
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].ui.hits > rs[j].ui.hits })
	if len(rs) > set.MaxPages {
		rs = rs[:set.MaxPages]
	}
	var fetched int
	work := make(chan ranked)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range work {
				var title, body string
				if cp, ok := c.st.CachedPage(r.u); ok {
					title, body = cp.Title, cp.Text
				} else {
					fctx, cancel := context.WithTimeout(ctx, 25*time.Second)
					t, b, err := FetchPage(fctx, r.u)
					cancel()
					if err == nil {
						title, body = t, b
					}
					c.st.CachePage(cachedPage{URL: r.u, Title: t, Text: b, Failed: err != nil})
				}
				if strings.TrimSpace(body) == "" {
					// Page couldn't be read (paywall, blocked): the search snippets still show what it says.
					body = strings.Join(r.ui.snippets, "\n")
				}
				title = firstNonEmpty(title, r.ui.title, hostOf(r.u))
				mu.Lock()
				found = append(found, fetchedSource{URL: r.u, Title: title, Type: classifySource(r.u), Text: body})
				fetched++
				progress(fmt.Sprintf("Reading sources found online (%d of %d)", fetched, len(rs)))
				mu.Unlock()
			}
		}()
	}
	for _, r := range rs {
		select {
		case work <- r:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	if !sub.Options.Web {
		var keep []fetchedSource
		for _, f := range found {
			if f.Type == "Publication" {
				keep = append(keep, f)
			}
		}
		found = keep
	}
	return found, notes
}
