package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Settings struct {
	BraveKey        string `json:"braveKey"`
	GoogleKey       string `json:"googleKey"`
	GoogleCX        string `json:"googleCx"`
	BingKey         string `json:"bingKey"`
	MaxQueries      int    `json:"maxQueries"`
	MaxPages        int    `json:"maxPages"`
	InstitutionName string `json:"institutionName"`
}

func defaultSettings() Settings {
	return Settings{MaxQueries: 40, MaxPages: 40, InstitutionName: "My School"}
}

type Assignment struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Class   string    `json:"class"`
	Created time.Time `json:"created"`
	// Defaults for new submissions.
	Options CheckOptions `json:"options"`
	Filters Filters      `json:"filters"`
}

type CheckOptions struct {
	Web          bool `json:"web"`
	Publications bool `json:"publications"`
	Repository   bool `json:"repository"`
	Store        bool `json:"store"` // add this paper to the repository so later papers are checked against it
}

type Submission struct {
	ID         string        `json:"id"`
	Assignment string        `json:"assignment"`
	Author     string        `json:"author"`
	Title      string        `json:"title"`
	FileName   string        `json:"fileName"`
	FileSize   int           `json:"fileSize"`
	Uploaded   time.Time     `json:"uploaded"`
	Checked    time.Time     `json:"checked"`
	Status     string        `json:"status"` // queued, checking, done, error
	Progress   string        `json:"progress"`
	Error      string        `json:"error,omitempty"`
	Notes      []string      `json:"notes,omitempty"`
	Text       string        `json:"text"`
	Hidden     []Span        `json:"hidden"`
	Replaced   []Span        `json:"replaced"`
	Words      int           `json:"words"`
	Chars      int           `json:"chars"`
	Options    CheckOptions  `json:"options"`
	Filters    Filters       `json:"filters"`
	Sources    []SourceMatch `json:"sources"`
	AI         AIResult      `json:"ai"`
	Index      int           `json:"index"`
	InRepo     bool          `json:"inRepo"`
}

// Summary is what the assignment inbox lists.
type Summary struct {
	ID         string    `json:"id"`
	Assignment string    `json:"assignment"`
	Author     string    `json:"author"`
	Title      string    `json:"title"`
	FileName   string    `json:"fileName"`
	Uploaded   time.Time `json:"uploaded"`
	Status     string    `json:"status"`
	Progress   string    `json:"progress"`
	Error      string    `json:"error,omitempty"`
	Words      int       `json:"words"`
	Index      int       `json:"index"`
	AI         int       `json:"ai"`
	AIAvail    bool      `json:"aiAvailable"`
	Flags      int       `json:"flags"`
}

func (s *Submission) Summary() Summary {
	flags := 0
	if len(s.Hidden) > 0 {
		flags++
	}
	if len(s.Replaced) > 0 {
		flags++
	}
	return Summary{s.ID, s.Assignment, s.Author, s.Title, s.FileName, s.Uploaded, s.Status, s.Progress, s.Error, s.Words, s.Index, s.AI.Percent, s.AI.Available, flags}
}

type Store struct {
	dir         string
	mu          sync.RWMutex
	settings    Settings
	assignments map[string]*Assignment
	subs        map[string]*Submission
	repo        *RepoIndex
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func OpenStore(dir string) (*Store, error) {
	for _, d := range []string{dir, filepath.Join(dir, "submissions"), filepath.Join(dir, "files"), filepath.Join(dir, "cache")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	st := &Store{dir: dir, settings: defaultSettings(), assignments: map[string]*Assignment{}, subs: map[string]*Submission{}, repo: NewRepoIndex()}
	readJSON(filepath.Join(dir, "settings.json"), &st.settings)
	var as []*Assignment
	readJSON(filepath.Join(dir, "assignments.json"), &as)
	for _, a := range as {
		st.assignments[a.ID] = a
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "submissions"))
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var s Submission
		if readJSON(filepath.Join(dir, "submissions", e.Name()), &s) != nil {
			continue
		}
		if s.Status == "checking" || s.Status == "queued" {
			s.Status = "queued" // resume after a restart
		}
		st.subs[s.ID] = &s
		if s.InRepo {
			st.repo.Add(s.ID, s.Text)
		}
	}
	if len(st.assignments) == 0 {
		a := &Assignment{ID: newID(), Name: "Quick Submit", Class: "", Created: time.Now(), Options: CheckOptions{true, true, true, true}}
		st.assignments[a.ID] = a
		st.saveAssignments()
	}
	return st, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (st *Store) Settings() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.settings
}

func (st *Store) SetSettings(s Settings) error {
	if s.MaxQueries <= 0 {
		s.MaxQueries = 40
	}
	if s.MaxQueries > 200 {
		s.MaxQueries = 200
	}
	if s.MaxPages <= 0 {
		s.MaxPages = 40
	}
	if s.MaxPages > 150 {
		s.MaxPages = 150
	}
	st.mu.Lock()
	st.settings = s
	st.mu.Unlock()
	return writeJSON(filepath.Join(st.dir, "settings.json"), s)
}

func (st *Store) saveAssignments() error {
	var as []*Assignment
	for _, a := range st.assignments {
		as = append(as, a)
	}
	sort.Slice(as, func(i, j int) bool { return as[i].Created.Before(as[j].Created) })
	return writeJSON(filepath.Join(st.dir, "assignments.json"), as)
}

func (st *Store) Assignments() []Assignment {
	st.mu.RLock()
	defer st.mu.RUnlock()
	var out []Assignment
	for _, a := range st.assignments {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func (st *Store) Assignment(id string) (Assignment, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	a, ok := st.assignments[id]
	if !ok {
		return Assignment{}, false
	}
	return *a, true
}

func (st *Store) SaveAssignment(a Assignment) (Assignment, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if a.ID == "" {
		a.ID = newID()
		a.Created = time.Now()
	} else if old, ok := st.assignments[a.ID]; ok {
		a.Created = old.Created
	} else {
		return a, errors.New("assignment not found")
	}
	if strings.TrimSpace(a.Name) == "" {
		a.Name = "Untitled assignment"
	}
	st.assignments[a.ID] = &a
	return a, st.saveAssignments()
}

func (st *Store) DeleteAssignment(id string) error {
	st.mu.Lock()
	delete(st.assignments, id)
	var ids []string
	for _, s := range st.subs {
		if s.Assignment == id {
			ids = append(ids, s.ID)
		}
	}
	err := st.saveAssignments()
	st.mu.Unlock()
	for _, sid := range ids {
		st.DeleteSubmission(sid)
	}
	return err
}

func (st *Store) Summaries(assignment string) []Summary {
	st.mu.RLock()
	defer st.mu.RUnlock()
	var out []Summary
	for _, s := range st.subs {
		if assignment == "" || s.Assignment == assignment {
			out = append(out, s.Summary())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Uploaded.After(out[j].Uploaded) })
	return out
}

// Get returns a copy of the submission that is safe to read without the lock.
func (st *Store) Get(id string) (*Submission, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	s, ok := st.subs[id]
	if !ok {
		return nil, false
	}
	c := *s
	return &c, true
}

func (st *Store) Put(s *Submission) error {
	st.mu.Lock()
	c := *s
	st.subs[s.ID] = &c
	st.mu.Unlock()
	return writeJSON(filepath.Join(st.dir, "submissions", s.ID+".json"), &c)
}

// Update changes a submission in place under the lock and saves it.
func (st *Store) Update(id string, fn func(s *Submission)) (*Submission, bool) {
	st.mu.Lock()
	s, ok := st.subs[id]
	if !ok {
		st.mu.Unlock()
		return nil, false
	}
	fn(s)
	c := *s
	st.mu.Unlock()
	writeJSON(filepath.Join(st.dir, "submissions", id+".json"), &c)
	return &c, true
}

// SetProgress updates the progress line without writing to disk.
func (st *Store) SetProgress(id, status, msg string) {
	st.mu.Lock()
	if s, ok := st.subs[id]; ok {
		s.Status = status
		s.Progress = msg
	}
	st.mu.Unlock()
}

func (st *Store) DeleteSubmission(id string) {
	st.mu.Lock()
	delete(st.subs, id)
	st.mu.Unlock()
	st.repo.Remove(id)
	os.Remove(filepath.Join(st.dir, "submissions", id+".json"))
	matches, _ := filepath.Glob(filepath.Join(st.dir, "files", id+".*"))
	for _, m := range matches {
		os.Remove(m)
	}
}

func (st *Store) SaveOriginal(id, name string, data []byte) error {
	return os.WriteFile(filepath.Join(st.dir, "files", id+strings.ToLower(filepath.Ext(name))), data, 0o644)
}

func (st *Store) OriginalPath(id, name string) string {
	return filepath.Join(st.dir, "files", id+strings.ToLower(filepath.Ext(name)))
}

// Source page cache, so re-checking or checking similar papers doesn't refetch the web.

type cachedPage struct {
	URL, Title, Text string
	Fetched          time.Time
	Failed           bool
}

func (st *Store) cachePath(u string) string {
	h := sha1.Sum([]byte(u))
	return filepath.Join(st.dir, "cache", hex.EncodeToString(h[:])+".json")
}

func (st *Store) CachedPage(u string) (cachedPage, bool) {
	var c cachedPage
	if readJSON(st.cachePath(u), &c) != nil {
		return c, false
	}
	if time.Since(c.Fetched) > 30*24*time.Hour || (c.Failed && time.Since(c.Fetched) > 24*time.Hour) {
		return c, false
	}
	return c, true
}

func (st *Store) CachePage(c cachedPage) {
	c.Fetched = time.Now()
	writeJSON(st.cachePath(c.URL), c)
}
