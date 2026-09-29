package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Free scholarly databases searched for "Publications" matches, besides OpenAlex.

func getJSON(ctx context.Context, u string, headers map[string]string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "OriginCheck/"+version+" (plagiarism checker; https://github.com/kayking77/Origin-Check)")
	req.Header.Set("Accept", "application/json")
	for k, val := range headers {
		req.Header.Set(k, val)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(v)
}

func authorLine(names []string, venue string, year int) string {
	var meta []string
	if len(names) > 0 {
		a := names[0]
		if len(names) > 1 {
			a += " et al."
		}
		meta = append(meta, a)
	}
	if venue != "" {
		meta = append(meta, venue)
	}
	if year > 0 {
		meta = append(meta, fmt.Sprint(year))
	}
	if len(meta) == 0 {
		return ""
	}
	return " (" + strings.Join(meta, ", ") + ")"
}

func semanticScholarSearch(ctx context.Context, q string, n int, key string) ([]fetchedSource, error) {
	u := fmt.Sprintf("https://api.semanticscholar.org/graph/v1/paper/search?limit=%d&fields=title,abstract,url,year,venue,authors,externalIds&query=%s", n, url.QueryEscape(q))
	h := map[string]string{}
	if key != "" {
		h["x-api-key"] = key
	}
	var r struct {
		Data []struct {
			Title, Abstract, URL, Venue string
			Year                        int
			Authors                     []struct{ Name string }
			ExternalIDs                 map[string]any `json:"externalIds"`
		} `json:"data"`
	}
	if err := getJSON(ctx, u, h, &r); err != nil {
		return nil, err
	}
	var out []fetchedSource
	for _, p := range r.Data {
		if p.Abstract == "" {
			continue
		}
		var names []string
		for _, a := range p.Authors {
			names = append(names, a.Name)
		}
		link := p.URL
		if d, ok := p.ExternalIDs["DOI"].(string); ok && d != "" {
			link = "https://doi.org/" + d
		}
		out = append(out, fetchedSource{URL: link, Title: p.Title + authorLine(names, p.Venue, p.Year), Type: "Publication", Text: p.Title + "\n" + p.Abstract})
	}
	return out, nil
}

func crossrefSearch(ctx context.Context, q string, n int) ([]fetchedSource, error) {
	u := fmt.Sprintf("https://api.crossref.org/works?rows=%d&select=DOI,title,abstract,author,issued,container-title&query=%s", n, url.QueryEscape(q))
	var r struct {
		Message struct {
			Items []struct {
				DOI       string   `json:"DOI"`
				Title     []string `json:"title"`
				Abstract  string   `json:"abstract"`
				Container []string `json:"container-title"`
				Author    []struct{ Given, Family string }
				Issued    struct {
					Parts [][]int `json:"date-parts"`
				} `json:"issued"`
			} `json:"items"`
		} `json:"message"`
	}
	if err := getJSON(ctx, u, nil, &r); err != nil {
		return nil, err
	}
	var out []fetchedSource
	for _, it := range r.Message.Items {
		if it.Abstract == "" || len(it.Title) == 0 {
			continue
		}
		var names []string
		for _, a := range it.Author {
			names = append(names, strings.TrimSpace(a.Given+" "+a.Family))
		}
		year := 0
		if len(it.Issued.Parts) > 0 && len(it.Issued.Parts[0]) > 0 {
			year = it.Issued.Parts[0][0]
		}
		venue := ""
		if len(it.Container) > 0 {
			venue = it.Container[0]
		}
		abs := stripTags(it.Abstract)
		out = append(out, fetchedSource{URL: "https://doi.org/" + it.DOI, Title: it.Title[0] + authorLine(names, venue, year), Type: "Publication", Text: it.Title[0] + "\n" + abs})
	}
	return out, nil
}

func arxivSearch(ctx context.Context, q string, n int) ([]fetchedSource, error) {
	u := fmt.Sprintf("https://export.arxiv.org/api/query?max_results=%d&search_query=%s", n, url.QueryEscape("all:"+q))
	b, _, err := httpGet(ctx, u, 4<<20)
	if err != nil {
		return nil, err
	}
	var feed struct {
		Entries []struct {
			ID        string `xml:"id"`
			Title     string `xml:"title"`
			Summary   string `xml:"summary"`
			Published string `xml:"published"`
			Authors   []struct {
				Name string `xml:"name"`
			} `xml:"author"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(b, &feed); err != nil {
		return nil, err
	}
	var out []fetchedSource
	for _, e := range feed.Entries {
		var names []string
		for _, a := range e.Authors {
			names = append(names, a.Name)
		}
		year := 0
		if len(e.Published) >= 4 {
			fmt.Sscan(e.Published[:4], &year)
		}
		t := strings.Join(strings.Fields(e.Title), " ")
		out = append(out, fetchedSource{URL: e.ID, Title: t + authorLine(names, "arXiv", year), Type: "Publication", Text: t + "\n" + strings.Join(strings.Fields(e.Summary), " ")})
	}
	return out, nil
}

func europePMCSearch(ctx context.Context, q string, n int) ([]fetchedSource, error) {
	u := fmt.Sprintf("https://www.ebi.ac.uk/europepmc/webservices/rest/search?format=json&resultType=core&pageSize=%d&query=%s", n, url.QueryEscape(q))
	var r struct {
		ResultList struct {
			Result []struct {
				ID, Source, Title, AbstractText, AuthorString, PubYear, DOI string
				JournalTitle                                                string `json:"journalTitle"`
			} `json:"result"`
		} `json:"resultList"`
	}
	if err := getJSON(ctx, u, nil, &r); err != nil {
		return nil, err
	}
	var out []fetchedSource
	for _, p := range r.ResultList.Result {
		if p.AbstractText == "" {
			continue
		}
		link := fmt.Sprintf("https://europepmc.org/article/%s/%s", p.Source, p.ID)
		if p.DOI != "" {
			link = "https://doi.org/" + p.DOI
		}
		year := 0
		fmt.Sscan(p.PubYear, &year)
		var names []string
		if p.AuthorString != "" {
			names = strings.Split(p.AuthorString, ", ")
		}
		out = append(out, fetchedSource{URL: link, Title: stripTags(p.Title) + authorLine(names, p.JournalTitle, year), Type: "Publication", Text: stripTags(p.Title) + "\n" + stripTags(p.AbstractText)})
	}
	return out, nil
}

// CORE holds the full text of millions of open-access papers and theses. Needs a free key.
func coreSearch(ctx context.Context, q string, n int, key string) ([]fetchedSource, error) {
	u := fmt.Sprintf("https://api.core.ac.uk/v3/search/works?limit=%d&q=%s", n, url.QueryEscape(q))
	var r struct {
		Results []struct {
			Title         string `json:"title"`
			Abstract      string `json:"abstract"`
			FullText      string `json:"fullText"`
			DOI           string `json:"doi"`
			YearPublished int    `json:"yearPublished"`
			Publisher     string `json:"publisher"`
			Authors       []struct{ Name string }
			Links         []struct{ Type, URL string } `json:"links"`
			DownloadURL   string                       `json:"downloadUrl"`
		} `json:"results"`
	}
	if err := getJSON(ctx, u, map[string]string{"Authorization": "Bearer " + key}, &r); err != nil {
		return nil, err
	}
	var out []fetchedSource
	for _, p := range r.Results {
		text := p.FullText
		if text == "" {
			text = p.Abstract
		}
		if text == "" {
			continue
		}
		link := p.DownloadURL
		for _, l := range p.Links {
			if l.Type == "display" {
				link = l.URL
			}
		}
		if p.DOI != "" {
			link = "https://doi.org/" + p.DOI
		}
		var names []string
		for _, a := range p.Authors {
			names = append(names, a.Name)
		}
		out = append(out, fetchedSource{URL: link, Title: p.Title + authorLine(names, p.Publisher, p.YearPublished), Type: "Publication", Text: p.Title + "\n" + text})
	}
	return out, nil
}

// explainErr turns a failed request into something a teacher can act on.
func explainErr(db string, err error) string {
	msg := shortErr(err)
	switch {
	case strings.Contains(msg, "HTTP 401"), strings.Contains(msg, "HTTP 403"):
		return db + " refused the request (" + msg + "). It may need a free API key: add one in Settings."
	case strings.Contains(msg, "HTTP 429"):
		return db + " is limiting how many searches it answers (" + msg + "). Adding a free API key in Settings raises the limit."
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "dial tcp"), strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return db + " could not be reached from this computer (" + msg + "). Check the internet connection, or whether a firewall or school network blocks it."
	}
	return db + " could not be searched (" + msg + ")."
}

// shortErr drops the request URL from network errors, keeping just the reason.
func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}
