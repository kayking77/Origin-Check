package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36"

var httpClient = &http.Client{Timeout: 20 * time.Second}

func httpGet(ctx context.Context, u string, limit int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return b, resp.Header.Get("Content-Type"), err
}

// HTMLToText keeps the readable text of a page, dropping scripts, menus and footers.
func HTMLToText(b []byte) string {
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return ""
	}
	var sb strings.Builder
	skip := map[string]bool{"script": true, "style": true, "noscript": true, "nav": true, "footer": true, "header": true, "aside": true, "form": true, "svg": true, "button": true, "select": true, "iframe": true, "template": true, "head": true}
	block := map[string]bool{"p": true, "div": true, "br": true, "li": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "tr": true, "section": true, "article": true, "blockquote": true, "pre": true, "td": true, "dd": true, "dt": true, "figcaption": true, "title": true}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if skip[n.Data] {
				return
			}
			for _, a := range n.Attr {
				if (a.Key == "hidden") || (a.Key == "aria-hidden" && a.Val == "true") {
					return
				}
				if a.Key == "class" || a.Key == "id" {
					v := strings.ToLower(a.Val)
					if strings.Contains(v, "cookie") || strings.Contains(v, "sidebar") || strings.Contains(v, "comment") || strings.Contains(v, "share") || strings.Contains(v, "related") || strings.Contains(v, "advert") {
						return
					}
				}
			}
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && block[n.Data] {
			sb.WriteByte('\n')
		}
	}
	walk(doc)
	// Collapse whitespace inside lines, drop blank runs.
	var out strings.Builder
	for _, l := range strings.Split(sb.String(), "\n") {
		l = strings.Join(strings.Fields(l), " ")
		if l != "" {
			out.WriteString(l)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func htmlTitle(b []byte) string {
	m := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).FindSubmatch(b)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(strings.Join(strings.Fields(string(m[1])), " ")))
}

// SearchHit is one web search result.
type SearchHit struct {
	URL, Title, Snippet string
}

// Searcher runs web searches with whatever engines are configured.
type Searcher struct {
	Settings Settings
	mu       sync.Mutex
	last     map[string]time.Time
	blocked  map[string]bool
	Log      func(string)
}

func (s *Searcher) wait(engine string, gap time.Duration) {
	s.mu.Lock()
	if s.last == nil {
		s.last = map[string]time.Time{}
	}
	next := s.last[engine].Add(gap)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	s.last[engine] = next
	s.mu.Unlock()
	time.Sleep(time.Until(next))
}

func (s *Searcher) isBlocked(e string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blocked[e]
}

func (s *Searcher) block(e string) {
	s.mu.Lock()
	if s.blocked == nil {
		s.blocked = map[string]bool{}
	}
	s.blocked[e] = true
	s.mu.Unlock()
	if s.Log != nil {
		s.Log("Web search engine " + e + " stopped answering; using the others.")
	}
}

// Search tries the configured API engines first, then the keyless ones.
func (s *Searcher) Search(ctx context.Context, q string) ([]SearchHit, error) {
	type eng struct {
		name string
		gap  time.Duration
		fn   func(context.Context, string) ([]SearchHit, error)
	}
	var engines []eng
	if s.Settings.BraveKey != "" {
		engines = append(engines, eng{"Brave", 1100 * time.Millisecond, s.brave})
	}
	if s.Settings.GoogleKey != "" && s.Settings.GoogleCX != "" {
		engines = append(engines, eng{"Google", 200 * time.Millisecond, s.google})
	}
	if s.Settings.BingKey != "" {
		engines = append(engines, eng{"Bing API", 200 * time.Millisecond, s.bingAPI})
	}
	engines = append(engines, eng{"DuckDuckGo", 1500 * time.Millisecond, ddgSearch}, eng{"Bing", 1500 * time.Millisecond, bingSearch})
	var lastErr error
	for _, e := range engines {
		if s.isBlocked(e.name) {
			continue
		}
		s.wait(e.name, e.gap)
		hits, err := e.fn(ctx, q)
		if err == nil && len(hits) > 0 {
			return hits, nil
		}
		if err != nil {
			lastErr = err
			if errors.Is(err, errBlocked) {
				s.block(e.name)
			}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no results")
	}
	return nil, lastErr
}

var errBlocked = errors.New("search engine blocked the request")

var (
	ddgResult  = regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippet = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</a>`)
	tagRe      = regexp.MustCompile(`<[^>]+>`)
)

func stripTags(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
}

func ddgSearch(ctx context.Context, q string) ([]SearchHit, error) {
	form := url.Values{"q": {q}, "kl": {"us-en"}}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://html.duckduckgo.com/html/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", "https://html.duckduckgo.com/")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode == 202 || resp.StatusCode == 403 || resp.StatusCode == 429 || bytes.Contains(b, []byte("anomaly-modal")) {
		return nil, errBlocked
	}
	var hits []SearchHit
	links := ddgResult.FindAllSubmatch(b, -1)
	snips := ddgSnippet.FindAllSubmatch(b, -1)
	for i, m := range links {
		u := html.UnescapeString(string(m[1]))
		if strings.Contains(u, "duckduckgo.com/l/?") {
			if pu, err := url.Parse(u); err == nil {
				if real := pu.Query().Get("uddg"); real != "" {
					u = real
				}
			}
		}
		if strings.HasPrefix(u, "//") {
			u = "https:" + u
		}
		if strings.Contains(u, "duckduckgo.com/y.js") || !strings.HasPrefix(u, "http") {
			continue // ads
		}
		h := SearchHit{URL: u, Title: stripTags(string(m[2]))}
		if i < len(snips) {
			h.Snippet = stripTags(string(snips[i][1]))
		}
		hits = append(hits, h)
	}
	return hits, nil
}

var (
	bingItem    = regexp.MustCompile(`(?s)<li class="b_algo"(.*?)</li>`)
	bingLink    = regexp.MustCompile(`(?s)<h2[^>]*>\s*<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	bingSnippet = regexp.MustCompile(`(?s)<p[^>]*>(.*?)</p>`)
)

func bingSearch(ctx context.Context, q string) ([]SearchHit, error) {
	b, _, err := httpGet(ctx, "https://www.bing.com/search?setlang=en&count=10&q="+url.QueryEscape(q), 3<<20)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(b, []byte("/challenge")) && !bytes.Contains(b, []byte("b_algo")) {
		return nil, errBlocked
	}
	var hits []SearchHit
	for _, it := range bingItem.FindAllSubmatch(b, -1) {
		m := bingLink.FindSubmatch(it[1])
		if m == nil {
			continue
		}
		u := html.UnescapeString(string(m[1]))
		if strings.Contains(u, "bing.com/ck/a") {
			if pu, err := url.Parse(u); err == nil {
				enc := pu.Query().Get("u")
				if strings.HasPrefix(enc, "a1") {
					if d, err := base64.RawURLEncoding.DecodeString(enc[2:]); err == nil {
						u = string(d)
					}
				}
			}
		}
		if !strings.HasPrefix(u, "http") {
			continue
		}
		h := SearchHit{URL: u, Title: stripTags(string(m[2]))}
		if sm := bingSnippet.FindSubmatch(it[1]); sm != nil {
			h.Snippet = stripTags(string(sm[1]))
		}
		hits = append(hits, h)
	}
	return hits, nil
}

func (s *Searcher) brave(ctx context.Context, q string) ([]SearchHit, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.search.brave.com/res/v1/web/search?count=10&q="+url.QueryEscape(q), nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", s.Settings.BraveKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, errBlocked
	}
	var r struct {
		Web struct {
			Results []struct{ URL, Title, Description string } `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var hits []SearchHit
	for _, x := range r.Web.Results {
		hits = append(hits, SearchHit{x.URL, stripTags(x.Title), stripTags(x.Description)})
	}
	return hits, nil
}

func (s *Searcher) google(ctx context.Context, q string) ([]SearchHit, error) {
	u := "https://www.googleapis.com/customsearch/v1?key=" + url.QueryEscape(s.Settings.GoogleKey) + "&cx=" + url.QueryEscape(s.Settings.GoogleCX) + "&q=" + url.QueryEscape(q)
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode == 400 {
		return nil, errBlocked
	}
	var r struct {
		Items []struct{ Link, Title, Snippet string } `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var hits []SearchHit
	for _, x := range r.Items {
		hits = append(hits, SearchHit{x.Link, x.Title, x.Snippet})
	}
	return hits, nil
}

func (s *Searcher) bingAPI(ctx context.Context, q string) ([]SearchHit, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.bing.microsoft.com/v7.0/search?count=10&q="+url.QueryEscape(q), nil)
	req.Header.Set("Ocp-Apim-Subscription-Key", s.Settings.BingKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, errBlocked
	}
	var r struct {
		WebPages struct {
			Value []struct{ URL, Name, Snippet string } `json:"value"`
		} `json:"webPages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var hits []SearchHit
	for _, x := range r.WebPages.Value {
		hits = append(hits, SearchHit{x.URL, x.Name, x.Snippet})
	}
	return hits, nil
}

// Wikipedia and OpenAlex are free, keyless APIs that cover encyclopedic and published sources.

type fetchedSource struct {
	URL, Title, Type, Text string
}

func wikipediaSearch(ctx context.Context, q string, n int) ([]fetchedSource, error) {
	u := fmt.Sprintf("https://en.wikipedia.org/w/api.php?action=query&list=search&format=json&srlimit=%d&srsearch=%s", n, url.QueryEscape(q))
	b, _, err := httpGet(ctx, u, 1<<20)
	if err != nil {
		return nil, err
	}
	var r struct {
		Query struct {
			Search []struct {
				Title  string `json:"title"`
				PageID int    `json:"pageid"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	var out []fetchedSource
	for _, p := range r.Query.Search {
		eu := fmt.Sprintf("https://en.wikipedia.org/w/api.php?action=query&prop=extracts&explaintext=1&format=json&pageids=%d", p.PageID)
		b, _, err := httpGet(ctx, eu, 4<<20)
		if err != nil {
			continue
		}
		var er struct {
			Query struct {
				Pages map[string]struct {
					Extract string `json:"extract"`
				} `json:"pages"`
			} `json:"query"`
		}
		if json.Unmarshal(b, &er) != nil {
			continue
		}
		for _, pg := range er.Query.Pages {
			out = append(out, fetchedSource{
				URL:   "https://en.wikipedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(p.Title, " ", "_")),
				Title: p.Title + " - Wikipedia", Type: "Internet", Text: pg.Extract,
			})
		}
	}
	return out, nil
}

func openAlexSearch(ctx context.Context, q string, n int, key string) ([]fetchedSource, error) {
	u := fmt.Sprintf("https://api.openalex.org/works?per-page=%d&search=%s&select=id,doi,display_name,abstract_inverted_index,publication_year,primary_location,authorships", n, url.QueryEscape(q))
	if key != "" {
		u += "&api_key=" + url.QueryEscape(key)
	}
	b, _, err := httpGet(ctx, u, 4<<20)
	if err != nil {
		return nil, err
	}
	var r struct {
		Results []struct {
			ID       string           `json:"id"`
			DOI      string           `json:"doi"`
			Title    string           `json:"display_name"`
			Year     int              `json:"publication_year"`
			Abstract map[string][]int `json:"abstract_inverted_index"`
			Loc      struct {
				Source struct {
					Name string `json:"display_name"`
				} `json:"source"`
			} `json:"primary_location"`
			Authorships []struct {
				Author struct {
					Name string `json:"display_name"`
				} `json:"author"`
			} `json:"authorships"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	var out []fetchedSource
	for _, w := range r.Results {
		if len(w.Abstract) == 0 {
			continue
		}
		max := 0
		for _, ps := range w.Abstract {
			for _, p := range ps {
				if p > max {
					max = p
				}
			}
		}
		words := make([]string, max+1)
		for wd, ps := range w.Abstract {
			for _, p := range ps {
				words[p] = wd
			}
		}
		link := w.DOI
		if link == "" {
			link = w.ID
		}
		title := w.Title
		var meta []string
		if len(w.Authorships) > 0 {
			a := w.Authorships[0].Author.Name
			if len(w.Authorships) > 1 {
				a += " et al."
			}
			meta = append(meta, a)
		}
		if w.Loc.Source.Name != "" {
			meta = append(meta, w.Loc.Source.Name)
		}
		if w.Year > 0 {
			meta = append(meta, fmt.Sprint(w.Year))
		}
		if len(meta) > 0 {
			title += " (" + strings.Join(meta, ", ") + ")"
		}
		out = append(out, fetchedSource{URL: link, Title: title, Type: "Publication", Text: title + "\n" + strings.Join(words, " ")})
	}
	return out, nil
}

// FetchPage downloads a web page or PDF and returns its readable text.
func FetchPage(ctx context.Context, u string) (title, text string, err error) {
	b, ctype, err := httpGet(ctx, u, 15<<20)
	if err != nil {
		return "", "", err
	}
	if strings.Contains(ctype, "pdf") || bytes.HasPrefix(b, []byte("%PDF")) {
		t, err := extractPDF(b)
		return "", t, err
	}
	if strings.Contains(ctype, "officedocument.wordprocessingml") {
		ex, err := extractDocx(b)
		return "", ex.Text, err
	}
	if strings.Contains(ctype, "text/plain") {
		return "", decodeText(b), nil
	}
	return htmlTitle(b), HTMLToText(b), nil
}

func hostOf(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	return strings.TrimPrefix(pu.Hostname(), "www.")
}

// publicationHosts are sites whose pages are journal articles, books or papers.
var publicationHosts = []string{"doi.org", "jstor.org", "springer.com", "sciencedirect.com", "wiley.com", "tandfonline.com", "sagepub.com", "ncbi.nlm.nih.gov", "pubmed", "researchgate.net", "academia.edu", "arxiv.org", "ssrn.com", "semanticscholar.org", "books.google", "oup.com", "cambridge.org", "nature.com", "frontiersin.org", "mdpi.com", "plos.org", "ieee.org", "acm.org", "core.ac.uk", "scholar.archive.org", "eric.ed.gov", "openalex.org", "bmj.com", "thelancet.com", "apa.org", "emerald.com", "muse.jhu.edu", "hindawi.com", "elsevier.com", "biomedcentral.com"}

func classifySource(u string) string {
	h := hostOf(u)
	for _, p := range publicationHosts {
		if strings.Contains(h, p) || strings.Contains(u, p) {
			return "Publication"
		}
	}
	if strings.HasSuffix(strings.ToLower(u), ".pdf") && strings.HasSuffix(h, ".edu") {
		return "Publication"
	}
	return "Internet"
}
