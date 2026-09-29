package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/encoding/charmap"
)

// Extracted is the plain text of an uploaded file plus anything hidden inside it.
type Extracted struct {
	Text   string
	Hidden []Span // text a reader would not see: white, tiny or hidden-formatted text
}

var supportedExt = map[string]bool{".docx": true, ".pdf": true, ".txt": true, ".rtf": true, ".odt": true, ".md": true, ".html": true, ".htm": true}

func ExtractFile(name string, data []byte) (Extracted, error) {
	ext := strings.ToLower(filepath.Ext(name))
	var ex Extracted
	var err error
	switch ext {
	case ".docx":
		ex, err = extractDocx(data)
	case ".pdf":
		ex.Text, err = extractPDF(data)
	case ".odt":
		ex.Text, err = extractODT(data)
	case ".rtf":
		ex.Text = extractRTF(string(data))
	case ".html", ".htm":
		ex.Text = HTMLToText(data)
	case ".txt", ".md":
		ex.Text = decodeText(data)
	default:
		return ex, fmt.Errorf("%s files are not supported. Use .docx, .pdf, .txt, .rtf or .odt", ext)
	}
	if err != nil {
		return ex, err
	}
	ex.Text = strings.ReplaceAll(ex.Text, "\r\n", "\n")
	if len(strings.Fields(ex.Text)) < 20 {
		return ex, errors.New("the file has fewer than 20 words of readable text (scanned PDFs without a text layer can't be read)")
	}
	return ex, nil
}

func decodeText(b []byte) string {
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		b = b[3:]
	}
	if len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE || b[0] == 0xFE && b[1] == 0xFF) {
		le := b[0] == 0xFF
		var sb strings.Builder
		for i := 2; i+1 < len(b); i += 2 {
			var r rune
			if le {
				r = rune(b[i]) | rune(b[i+1])<<8
			} else {
				r = rune(b[i])<<8 | rune(b[i+1])
			}
			sb.WriteRune(r)
		}
		return sb.String()
	}
	if utf8.Valid(b) {
		return string(b)
	}
	s, _ := charmap.Windows1252.NewDecoder().Bytes(b)
	return string(s)
}

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 200<<20))
		}
	}
	return nil, fmt.Errorf("%s not found", name)
}

func extractDocx(data []byte) (Extracted, error) {
	var ex Extracted
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ex, errors.New("this doesn't look like a valid .docx file")
	}
	doc, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return ex, errors.New("this .docx has no document body")
	}
	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(doc))
	type runProps struct{ vanish, white, tiny bool }
	var rp runProps
	inRPr, inText, inPPr := false, false, false
	hiddenStart := -1
	closeHidden := func() {
		if hiddenStart >= 0 && sb.Len() > hiddenStart {
			ex.Hidden = append(ex.Hidden, Span{hiddenStart, sb.Len()})
		}
		hiddenStart = -1
	}
	attr := func(se xml.StartElement, local string) string {
		for _, a := range se.Attr {
			if a.Name.Local == local {
				return a.Value
			}
		}
		return ""
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pPr":
				inPPr = true
			case "r":
				rp = runProps{}
			case "rPr":
				if !inPPr {
					inRPr = true
				}
			case "vanish", "specVanish":
				if inRPr && attr(t, "val") != "0" && attr(t, "val") != "false" {
					rp.vanish = true
				}
			case "color":
				if inRPr {
					v := strings.ToUpper(attr(t, "val"))
					rp.white = v == "FFFFFF"
				}
			case "sz":
				if inRPr {
					if n, err := strconv.Atoi(attr(t, "val")); err == nil && n <= 4 {
						rp.tiny = true // 2pt or smaller
					}
				}
			case "t":
				inText = true
			case "tab":
				if !inPPr {
					sb.WriteByte('\t')
				}
			case "br", "cr":
				sb.WriteByte('\n')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "pPr":
				inPPr = false
			case "rPr":
				inRPr = false
			case "t":
				inText = false
			case "p":
				closeHidden()
				sb.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				hid := rp.vanish || rp.white || rp.tiny
				if hid && hiddenStart < 0 {
					hiddenStart = sb.Len()
				}
				if !hid {
					closeHidden()
				}
				sb.Write(t)
			}
		}
	}
	closeHidden()
	ex.Text = sb.String()
	return ex, nil
}

func extractODT(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", errors.New("this doesn't look like a valid .odt file")
	}
	c, err := readZipFile(zr, "content.xml")
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(c))
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p", "h":
				depth++
			case "tab":
				sb.WriteByte('\t')
			case "s":
				sb.WriteByte(' ')
			case "line-break":
				sb.WriteByte('\n')
			}
		case xml.EndElement:
			if t.Name.Local == "p" || t.Name.Local == "h" {
				depth--
				sb.WriteByte('\n')
			}
		case xml.CharData:
			if depth > 0 {
				sb.Write(t)
			}
		}
	}
	return sb.String(), nil
}

func extractPDF(data []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("this PDF couldn't be read (it may be damaged or password-protected)")
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", errors.New("this PDF couldn't be read (it may be damaged or password-protected)")
	}
	var sb strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		// Two readings of the page; the glyph layout is better ordered, the row reading
		// sometimes finds text the glyph reading misses. Keep the one that holds more letters.
		a := layoutPDFPage(p.Content().Text)
		b := rowsPDFPage(p)
		if countLetters(a)*10 >= countLetters(b)*9 {
			sb.WriteString(a)
		} else {
			sb.WriteString(b)
		}
		sb.WriteString("\n\n")
	}
	return reflowPDF(sb.String()), nil
}

var hyphenBreak = regexp.MustCompile(`(\p{L})-\n(\p{Ll})`)

// reflowPDF joins the hard line breaks a PDF puts inside paragraphs, keeping real paragraph breaks.
func reflowPDF(s string) string {
	s = hyphenBreak.ReplaceAllString(s, "$1$2")
	lines := strings.Split(s, "\n")
	// Typical full line length, to tell a paragraph's short last line from a wrapped line.
	var lens []int
	for _, l := range lines {
		if n := len(strings.TrimSpace(l)); n > 0 {
			lens = append(lens, n)
		}
	}
	sort.Ints(lens)
	typical := 60
	if len(lens) > 0 {
		typical = lens[len(lens)*3/4]
	}
	var sb strings.Builder
	for i, l := range lines {
		l = strings.TrimRight(l, " ")
		sb.WriteString(l)
		if i == len(lines)-1 {
			break
		}
		next := strings.TrimSpace(lines[i+1])
		t := strings.TrimSpace(l)
		if t == "" || next == "" {
			sb.WriteByte('\n')
			continue
		}
		endsSentence := strings.ContainsAny(t[len(t)-1:], ".!?:\"”")
		short := len(t) < 45 || len(t) < typical*4/5
		if (endsSentence && short) || bulletRe.MatchString(next) || bibHeading.MatchString(t) || bibHeading.MatchString(next) {
			sb.WriteByte('\n')
		} else {
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

var rtfControl = regexp.MustCompile(`\\([a-z]{1,32})(-?\d{1,10})? ?|\\'([0-9a-fA-F]{2})|\\([{}\\])|\\~|\\-|\\\*|[{}]|\r|\n`)

func extractRTF(s string) string {
	// Drop destination groups that hold no body text.
	for _, d := range []string{"fonttbl", "colortbl", "stylesheet", "info", "pict", "header", "footer", "listtable", "listoverridetable", "rsidtbl", "generator", "themedata", "datastore", "latentstyles"} {
		for {
			i := strings.Index(s, "{\\"+d)
			if i < 0 {
				i = strings.Index(s, "{\\*\\"+d)
			}
			if i < 0 {
				break
			}
			depth, j := 0, i
			for ; j < len(s); j++ {
				if s[j] == '\\' {
					j++
					continue
				}
				if s[j] == '{' {
					depth++
				} else if s[j] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if j >= len(s) {
				s = s[:i]
				break
			}
			s = s[:i] + s[j+1:]
		}
	}
	var sb strings.Builder
	last := 0
	for _, m := range rtfControl.FindAllStringSubmatchIndex(s, -1) {
		sb.WriteString(s[last:m[0]])
		last = m[1]
		switch {
		case m[2] >= 0:
			w := s[m[2]:m[3]]
			switch w {
			case "par", "line", "sect", "page":
				sb.WriteByte('\n')
			case "tab":
				sb.WriteByte('\t')
			case "u":
				if m[4] >= 0 {
					n, _ := strconv.Atoi(s[m[4]:m[5]])
					if n < 0 {
						n += 65536
					}
					sb.WriteRune(rune(n))
					if last < len(s) && s[last] == '?' {
						last++
					}
				}
			case "emdash":
				sb.WriteString("—")
			case "endash":
				sb.WriteString("–")
			case "lquote":
				sb.WriteString("‘")
			case "rquote":
				sb.WriteString("’")
			case "ldblquote":
				sb.WriteString("“")
			case "rdblquote":
				sb.WriteString("”")
			}
		case m[6] >= 0:
			b, _ := strconv.ParseUint(s[m[6]:m[7]], 16, 8)
			r, _ := charmap.Windows1252.NewDecoder().Bytes([]byte{byte(b)})
			sb.Write(r)
		case m[8] >= 0:
			sb.WriteString(s[m[8]:m[9]])
		}
	}
	sb.WriteString(s[last:])
	return sb.String()
}

// layoutPDFPage rebuilds lines from positioned glyphs: groups them by baseline, orders them
// left to right, and puts spaces where there are gaps. Paragraphs are separated by a blank
// line where the gap between lines is clearly bigger than usual.
func layoutPDFPage(texts []pdf.Text) string {
	positioned := 0
	for _, t := range texts {
		if t.X != 0 {
			positioned++
		}
	}
	if positioned < len(texts)/2 {
		// The reader couldn't place the text: keep content order, one piece per line.
		var sb strings.Builder
		for _, t := range texts {
			sb.WriteString(t.S)
			if len(t.S) > 1 {
				sb.WriteByte('\n')
			}
		}
		return sb.String()
	}
	type line struct {
		y, fs float64
		ts    []pdf.Text
	}
	// Lines in content order (the order the PDF draws them, which is nearly always reading order).
	var lines []*line
	var prevX float64
	for _, t := range texts {
		if strings.TrimSpace(t.S) == "" && t.S != " " {
			continue
		}
		fs := t.FontSize
		if fs <= 0 {
			fs = 10
		}
		var ln *line
		if n := len(lines); n > 0 && math.Abs(lines[n-1].y-t.Y) < fs*0.45 && t.X >= prevX-fs*2 {
			ln = lines[n-1]
		}
		if ln == nil {
			ln = &line{y: t.Y, fs: fs}
			lines = append(lines, ln)
		}
		ln.ts = append(ln.ts, t)
		prevX = t.X
	}
	// Typical distance between consecutive lines.
	var gaps []float64
	for k := 1; k < len(lines); k++ {
		if g := lines[k-1].y - lines[k].y; g > 0 {
			gaps = append(gaps, g)
		}
	}
	sort.Float64s(gaps)
	typical := 14.0
	if len(gaps) > 0 {
		typical = gaps[len(gaps)/3]
	}
	// If the page spells out its spaces, only a wide gap means a missing space.
	spaces := 0
	for _, t := range texts {
		if strings.Contains(t.S, " ") {
			spaces++
		}
	}
	gapFactor := 0.18
	if spaces > len(texts)/20 {
		gapFactor = math.Inf(1)
	}
	var sb strings.Builder
	for k, ln := range lines {
		if k > 0 {
			if d := lines[k-1].y - ln.y; d > typical*1.45 || d < -typical*3 {
				sb.WriteString("\n\n")
			} else {
				sb.WriteByte('\n')
			}
		}
		sort.SliceStable(ln.ts, func(a, b int) bool { return ln.ts[a].X < ln.ts[b].X })
		var lb strings.Builder
		end := math.Inf(-1)
		for _, t := range ln.ts {
			fs := t.FontSize
			if fs <= 0 {
				fs = 10
			}
			cur := lb.String()
			if end != math.Inf(-1) && t.X-end > fs*gapFactor && !strings.HasSuffix(cur, " ") && !strings.HasPrefix(t.S, " ") {
				lb.WriteByte(' ')
			}
			lb.WriteString(t.S)
			if e := t.X + t.W; e > end {
				end = e
			}
		}
		sb.WriteString(strings.Join(strings.Fields(lb.String()), " "))
	}
	return sb.String()
}

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

func rowsPDFPage(p pdf.Page) string {
	rows, err := p.GetTextByRow()
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for _, row := range rows {
		var line strings.Builder
		var lastX, lastW float64
		for k, w := range row.Content {
			if k > 0 && w.X <= lastX && len(w.S) > 1 {
				line.WriteByte('\n')
			} else if k > 0 && w.X-(lastX+lastW) > w.FontSize*0.15 && !strings.HasSuffix(line.String(), " ") && !strings.HasPrefix(w.S, " ") {
				line.WriteByte(' ')
			}
			line.WriteString(w.S)
			lastX, lastW = w.X, w.W
		}
		sb.WriteString(line.String())
		sb.WriteByte('\n')
	}
	return sb.String()
}
