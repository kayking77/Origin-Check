package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

//go:embed assets/accuracy.json
var accuracyJSON []byte

type Server struct {
	st        *Store
	ck        *Checker
	password  string
	localOnly bool
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/assignments", func(w http.ResponseWriter, r *http.Request) {
		type row struct {
			Assignment
			Count int `json:"count"`
		}
		var out []row
		for _, a := range s.st.Assignments() {
			out = append(out, row{a, len(s.st.Summaries(a.ID))})
		}
		writeJSONResp(w, out)
	})
	mux.HandleFunc("POST /api/assignments", func(w http.ResponseWriter, r *http.Request) {
		var a Assignment
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			httpErr(w, 400, "Bad request")
			return
		}
		a, err := s.st.SaveAssignment(a)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		writeJSONResp(w, a)
	})
	mux.HandleFunc("DELETE /api/assignments/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.st.DeleteAssignment(r.PathValue("id"))
		writeJSONResp(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/assignments/{id}/submissions", func(w http.ResponseWriter, r *http.Request) {
		out := s.st.Summaries(r.PathValue("id"))
		if out == nil {
			out = []Summary{}
		}
		writeJSONResp(w, out)
	})
	mux.HandleFunc("GET /api/assignments/{id}/compare", s.compare)
	mux.HandleFunc("POST /api/submit", s.submit)
	mux.HandleFunc("GET /api/submissions/{id}/report", func(w http.ResponseWriter, r *http.Request) {
		sub, ok := s.st.Get(r.PathValue("id"))
		if !ok {
			httpErr(w, 404, "Submission not found")
			return
		}
		writeJSONResp(w, s.report(sub))
	})
	mux.HandleFunc("POST /api/submissions/{id}/filters", func(w http.ResponseWriter, r *http.Request) {
		var f Filters
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
			httpErr(w, 400, "Bad request")
			return
		}
		sub, ok := s.st.Update(r.PathValue("id"), func(x *Submission) {
			x.Filters = f
			x.Index = BuildReport(x.Text, x.Sources, f).Index
		})
		if !ok {
			httpErr(w, 404, "Submission not found")
			return
		}
		writeJSONResp(w, s.report(sub))
	})
	mux.HandleFunc("POST /api/submissions/{id}/recheck", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := s.st.Update(id, func(x *Submission) { x.Status = "queued"; x.Error = "" }); !ok {
			httpErr(w, 404, "Submission not found")
			return
		}
		s.ck.Enqueue(id)
		writeJSONResp(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/submissions/{id}/update", func(w http.ResponseWriter, r *http.Request) {
		var m struct{ Author, Title string }
		json.NewDecoder(r.Body).Decode(&m)
		sub, ok := s.st.Update(r.PathValue("id"), func(x *Submission) { x.Author = m.Author; x.Title = m.Title })
		if !ok {
			httpErr(w, 404, "Submission not found")
			return
		}
		writeJSONResp(w, sub.Summary())
	})
	mux.HandleFunc("DELETE /api/submissions/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.st.DeleteSubmission(r.PathValue("id"))
		writeJSONResp(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/submissions/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		sub, ok := s.st.Get(r.PathValue("id"))
		if !ok {
			httpErr(w, 404, "Submission not found")
			return
		}
		p := s.st.OriginalPath(sub.ID, sub.FileName)
		f, err := os.Open(p)
		if err != nil {
			httpErr(w, 404, "The original file is not stored")
			return
		}
		defer f.Close()
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sub.FileName))
		io.Copy(w, f)
	})
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, s.st.Settings())
	})
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var set Settings
		if err := json.NewDecoder(r.Body).Decode(&set); err != nil {
			httpErr(w, 400, "Bad request")
			return
		}
		if err := s.st.SetSettings(set); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		writeJSONResp(w, s.st.Settings())
	})
	mux.HandleFunc("POST /api/settings/test", s.testSearch)
	return s.auth(mux)
}

func (s *Server) auth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.password != "" {
			_, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(p), []byte(s.password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="OriginCheck"`)
				http.Error(w, "Password required", 401)
				return
			}
		}
		// Block other websites open in the same browser from driving this app (CSRF, DNS rebinding).
		if s.localOnly {
			host := r.Host
			if i := strings.LastIndex(host, ":"); i >= 0 {
				host = host[:i]
			}
			if host != "127.0.0.1" && host != "localhost" && host != "[::1]" {
				http.Error(w, "Forbidden", 403)
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-OriginCheck") != "1" {
			http.Error(w, "Forbidden", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	writeJSONResp(w, map[string]any{
		"version":    version,
		"repository": s.st.repo.Size(),
		"dataDir":    s.st.dir,
		"aiModel":    loadModel().ok,
		"accuracy":   json.RawMessage(accuracyJSON),
	})
}

func writeJSONResp(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type Flag struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Spans  []Span `json:"spans"`
}

type ReportResp struct {
	Summary
	Text        string           `json:"text"`
	Checked     time.Time        `json:"checked"`
	Chars       int              `json:"chars"`
	FileSize    int              `json:"fileSize"`
	Assignment  Assignment       `json:"assignmentInfo"`
	Options     CheckOptions     `json:"options"`
	Filters     Filters          `json:"filters"`
	Similarity  SimilarityReport `json:"similarity"`
	AIReport    AIResult         `json:"aiReport"`
	Flags       []Flag           `json:"flags"`
	Notes       []string         `json:"notes"`
	Sources     []SourceMatch    `json:"sources"`
	Institution string           `json:"institution"`
}

func (s *Server) report(sub *Submission) ReportResp {
	a, _ := s.st.Assignment(sub.Assignment)
	rr := ReportResp{Summary: sub.Summary(), Text: sub.Text, Checked: sub.Checked, Chars: sub.Chars, FileSize: sub.FileSize, Assignment: a,
		Options: sub.Options, Filters: sub.Filters, AIReport: sub.AI, Notes: sub.Notes, Sources: sub.Sources, Institution: s.st.Settings().InstitutionName}
	if sub.Status == "done" {
		rr.Similarity = BuildReport(sub.Text, sub.Sources, sub.Filters)
		rr.Index = rr.Similarity.Index
	}
	if len(sub.Replaced) > 0 {
		rr.Flags = append(rr.Flags, Flag{Kind: "replaced", Title: "Replaced characters",
			Detail: fmt.Sprintf("%d characters from other alphabets that look like English letters, or invisible characters, were found inside words. This is a known trick to stop similarity checkers from matching text. The report already reads them as the letters they imitate.", len(sub.Replaced)),
			Spans:  sub.Replaced})
	}
	if len(sub.Hidden) > 0 {
		words := 0
		for _, h := range sub.Hidden {
			words += len(Tokenize(sub.Text[h.Start:h.End]))
		}
		rr.Flags = append(rr.Flags, Flag{Kind: "hidden", Title: "Hidden text",
			Detail: fmt.Sprintf("%d words are formatted so a reader can't see them (white, hidden or 2-point text). Hidden text is sometimes used to change word counts or confuse checkers.", words),
			Spans:  sub.Hidden})
	}
	if rr.Flags == nil {
		rr.Flags = []Flag{}
	}
	// Empty lists, not nulls, so the page never has to guard against them.
	if rr.Sources == nil {
		rr.Sources = []SourceMatch{}
	}
	if rr.Notes == nil {
		rr.Notes = []string{}
	}
	if rr.AIReport.Sentences == nil {
		rr.AIReport.Sentences = []AISent{}
	}
	sim := &rr.Similarity
	if sim.Sources == nil {
		sim.Sources = []RankedSource{}
	}
	if sim.Highlights == nil {
		sim.Highlights = []Highlight{}
	}
	if sim.Excluded == nil {
		sim.Excluded = []Span{}
	}
	if sim.Quotes == nil {
		sim.Quotes = []Span{}
	}
	if sim.Groups == nil {
		sim.Groups = []GroupStat{}
	}
	if sim.ByType == nil {
		sim.ByType = map[string]int{}
	}
	return rr
}

const maxUpload = 100 << 20

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 400<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		httpErr(w, 400, "The upload could not be read: "+err.Error())
		return
	}
	aid := r.FormValue("assignment")
	a, ok := s.st.Assignment(aid)
	if !ok {
		httpErr(w, 400, "Pick an assignment first")
		return
	}
	opts := a.Options
	if o := r.FormValue("options"); o != "" {
		json.Unmarshal([]byte(o), &opts)
	}
	type result struct {
		File  string `json:"file"`
		ID    string `json:"id,omitempty"`
		Error string `json:"error,omitempty"`
	}
	var results []result
	author := strings.TrimSpace(r.FormValue("author"))
	title := strings.TrimSpace(r.FormValue("title"))

	add := func(name string, data []byte, ex Extracted) {
		sub := &Submission{ID: newID(), Assignment: aid, FileName: name, FileSize: len(data), Uploaded: time.Now(), Status: "queued",
			Text: ex.Text, Hidden: ex.Hidden, Replaced: ReplacedChars(ex.Text), Options: opts, Filters: a.Filters}
		sub.Words = len(Tokenize(ex.Text))
		sub.Chars = len([]rune(ex.Text))
		sub.Author, sub.Title = author, title
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if sub.Title == "" {
			sub.Title = base
		}
		if sub.Author == "" {
			sub.Author = guessAuthor(base)
		}
		if len(data) > 0 {
			s.st.SaveOriginal(sub.ID, name, data)
		}
		if err := s.st.Put(sub); err != nil {
			results = append(results, result{File: name, Error: err.Error()})
			return
		}
		s.ck.Enqueue(sub.ID)
		results = append(results, result{File: name, ID: sub.ID})
	}

	if txt := r.FormValue("text"); strings.TrimSpace(txt) != "" {
		ex := Extracted{Text: strings.ReplaceAll(txt, "\r\n", "\n")}
		if len(strings.Fields(ex.Text)) < 20 {
			httpErr(w, 400, "Paste at least 20 words")
			return
		}
		name := firstNonEmpty(title, "Pasted text") + ".txt"
		add(name, []byte(ex.Text), ex)
	}
	files := r.MultipartForm.File["files"]
	multi := len(files) > 1
	for _, fh := range files {
		if fh.Size > maxUpload {
			results = append(results, result{File: fh.Filename, Error: "File is over 100 MB"})
			continue
		}
		f, err := fh.Open()
		if err != nil {
			results = append(results, result{File: fh.Filename, Error: err.Error()})
			continue
		}
		data, _ := io.ReadAll(f)
		f.Close()
		ex, err := ExtractFile(fh.Filename, data)
		if err != nil {
			results = append(results, result{File: fh.Filename, Error: err.Error()})
			continue
		}
		if multi {
			// Several files at once: one per student, named from the file.
			author, title = "", ""
		}
		add(fh.Filename, data, ex)
	}
	if len(results) == 0 {
		httpErr(w, 400, "Choose a file or paste some text")
		return
	}
	writeJSONResp(w, results)
}

// guessAuthor turns "Jane_Doe_Essay1" or "Doe, Jane - essay" into a readable name.
func guessAuthor(base string) string {
	b := strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(base)
	f := strings.Fields(b)
	if len(f) >= 2 {
		return strings.Join(f[:2], " ")
	}
	return ""
}

// compare shows how much each pair of papers in an assignment overlaps (collusion check).
func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	subs := s.st.Summaries(r.PathValue("id"))
	type doc struct {
		id, name string
		toks     []Token
		text     string
	}
	var docs []doc
	for _, sm := range subs {
		full, ok := s.st.Get(sm.ID)
		if !ok {
			continue
		}
		docs = append(docs, doc{full.ID, firstNonEmpty(full.Author, full.Title, full.FileName), Tokenize(full.Text), full.Text})
	}
	type pair struct {
		A, B       string
		AName      string `json:"aName"`
		BName      string `json:"bName"`
		AInB, BInA int
		Words      int `json:"words"`
	}
	var pairs []pair
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range docs {
		for j := i + 1; j < len(docs); j++ {
			wg.Add(1)
			sem <- struct{}{}
			go func(a, b doc) {
				defer wg.Done()
				defer func() { <-sem }()
				blocks := MatchTexts(a.toks, b.text)
				words := 0
				for _, bl := range blocks {
					words += bl.ToTok - bl.From
				}
				if words == 0 {
					return
				}
				mu.Lock()
				pairs = append(pairs, pair{a.id, b.id, a.name, b.name, pct(words, len(a.toks)), pct(words, len(b.toks)), words})
				mu.Unlock()
			}(docs[i], docs[j])
		}
	}
	wg.Wait()
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Words > pairs[j].Words })
	if pairs == nil {
		pairs = []pair{}
	}
	writeJSONResp(w, map[string]any{"papers": len(docs), "pairs": pairs})
}

func (s *Server) testSearch(w http.ResponseWriter, r *http.Request) {
	var set Settings
	json.NewDecoder(r.Body).Decode(&set)
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	q := "the mitochondria is the powerhouse of the cell"
	type res struct {
		Engine string `json:"engine"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	var out []res
	try := func(name string, fn func() (int, error)) {
		n, err := fn()
		if err != nil {
			out = append(out, res{name, false, err.Error()})
		} else if n == 0 {
			out = append(out, res{name, false, "answered, but returned no results"})
		} else {
			out = append(out, res{name, true, fmt.Sprintf("%d results", n)})
		}
	}
	se := &Searcher{Settings: set}
	if set.BraveKey != "" {
		try("Brave Search API", func() (int, error) { h, err := se.brave(ctx, q); return len(h), err })
	}
	if set.GoogleKey != "" {
		try("Google Custom Search API", func() (int, error) { h, err := se.google(ctx, q); return len(h), err })
	}
	if set.BingKey != "" {
		try("Bing Search API", func() (int, error) { h, err := se.bingAPI(ctx, q); return len(h), err })
	}
	try("DuckDuckGo (no key)", func() (int, error) { h, err := ddgSearch(ctx, q); return len(h), err })
	try("Bing (no key)", func() (int, error) { h, err := bingSearch(ctx, q); return len(h), err })
	try("Wikipedia", func() (int, error) { h, err := wikipediaSearch(ctx, "mitochondria", 1); return len(h), err })
	pq := "mitochondria energy metabolism"
	try("OpenAlex", func() (int, error) { h, err := openAlexSearch(ctx, pq, 3, set.OpenAlexKey); return len(h), err })
	try("Semantic Scholar", func() (int, error) {
		h, err := semanticScholarSearch(ctx, pq, 3, set.SemanticScholarKey)
		return len(h), err
	})
	try("Crossref", func() (int, error) { h, err := crossrefSearch(ctx, pq, 10); return len(h), err })
	try("Europe PMC", func() (int, error) { h, err := europePMCSearch(ctx, pq, 3); return len(h), err })
	try("arXiv", func() (int, error) { h, err := arxivSearch(ctx, pq, 3); return len(h), err })
	if set.CoreKey != "" {
		try("CORE", func() (int, error) { h, err := coreSearch(ctx, pq, 3, set.CoreKey); return len(h), err })
	}
	writeJSONResp(w, out)
}
