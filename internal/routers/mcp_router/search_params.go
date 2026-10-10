package mcp_router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/haierkeys/fast-note-sync-service/pkg/util"
)

// searchParams is decoded strictly: unsupported fields must never look successful.
type searchParams struct {
	Vault          string   `json:"vault,omitempty"`
	Vaults         []string `json:"vaults,omitempty"`
	Query          string   `json:"query,omitempty"`
	Keyword        string   `json:"keyword,omitempty"`
	Path           string   `json:"path,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	Page           int      `json:"page,omitempty"`
	PageSize       int      `json:"pageSize,omitempty"`
	IncludeSnippet bool     `json:"includeSnippet,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	DateFrom       string   `json:"dateFrom,omitempty"`
	DateTo         string   `json:"dateTo,omitempty"`
	MinSize        *int64   `json:"minSize,omitempty"`
	MaxSize        *int64   `json:"maxSize,omitempty"`
	SortBy         string   `json:"sortBy,omitempty"`
	SortOrder      string   `json:"sortOrder,omitempty"`
	MimePrefix     string   `json:"mimePrefix,omitempty"`
	from, to       *int64
}

type searchHit struct {
	Vault   string `json:"vault"`
	ID      int64  `json:"id"`
	NoteID  int64  `json:"noteId,omitempty"`
	Path    string `json:"path"`
	Version int64  `json:"version,omitempty"`
	Size    int64  `json:"size"`
	Ctime   int64  `json:"ctime"`
	Mtime   int64  `json:"mtime"`
	Mime    string `json:"mime,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

type searchOutput struct {
	Results    []searchHit `json:"results"`
	Total      int         `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"pageSize"`
	HasMore    bool        `json:"hasMore"`
	TotalPages int         `json:"totalPages"`
}

func decodeSearchParams(args map[string]interface{}, kind string) (searchParams, error) {
	p := searchParams{Page: 1, PageSize: 20, Mode: "auto", SortBy: "mtime", SortOrder: "desc"}
	allowed := map[string]bool{}
	for _, k := range []string{"query", "page", "pageSize", "sortBy", "sortOrder", "dateFrom", "dateTo", "minSize", "maxSize"} {
		allowed[k] = true
	}
	if kind == "vault_search" {
		allowed["vaults"] = true
	} else {
		allowed["vault"] = true
	}
	if kind != "file_search" {
		for _, k := range []string{"mode", "includeSnippet", "tags"} {
			allowed[k] = true
		}
	}
	if kind == "file_search" {
		allowed["mimePrefix"] = true
	}
	if kind == "note_history_search" {
		allowed["path"] = true
		allowed["keyword"] = true
	}
	for k := range args {
		if !allowed[k] {
			return p, fmt.Errorf("unsupported parameter %q for %s", k, kind)
		}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return p, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, fmt.Errorf("invalid search parameters: %w", err)
	}
	if p.Query != "" && p.Keyword != "" {
		return p, fmt.Errorf("use either query or keyword, not both")
	}
	if p.Keyword != "" {
		p.Query = p.Keyword
	}
	if p.Page < 1 || p.PageSize < 1 || p.PageSize > 100 || p.Page > math.MaxInt/p.PageSize {
		return p, fmt.Errorf("page must be positive and pageSize must be 1..100")
	}
	switch p.Mode {
	case "auto", "path", "content", "fuzzy":
	default:
		return p, fmt.Errorf("mode must be auto, path, content or fuzzy")
	}
	switch p.SortBy {
	case "mtime", "ctime", "path", "size":
	default:
		return p, fmt.Errorf("sortBy must be mtime, ctime, path or size")
	}
	if p.SortOrder != "asc" && p.SortOrder != "desc" {
		return p, fmt.Errorf("sortOrder must be asc or desc")
	}
	if p.MinSize != nil && *p.MinSize < 0 || p.MaxSize != nil && *p.MaxSize < 0 {
		return p, fmt.Errorf("sizes must be non-negative")
	}
	if p.MinSize != nil && p.MaxSize != nil && *p.MinSize > *p.MaxSize {
		return p, fmt.Errorf("minSize exceeds maxSize")
	}
	for _, tag := range p.Tags {
		if strings.Trim(strings.TrimSpace(tag), "#") == "" {
			return p, fmt.Errorf("tags must contain non-empty names")
		}
	}
	for _, v := range p.Vaults {
		if strings.TrimSpace(v) == "" {
			return p, fmt.Errorf("vaults must contain non-empty names")
		}
	}
	if p.DateFrom != "" {
		t, e := time.Parse(time.RFC3339, p.DateFrom)
		if e != nil {
			return p, fmt.Errorf("dateFrom must be RFC3339 with timezone")
		}
		n := t.UnixMilli()
		p.from = &n
	}
	if p.DateTo != "" {
		t, e := time.Parse(time.RFC3339, p.DateTo)
		if e != nil {
			return p, fmt.Errorf("dateTo must be RFC3339 with timezone")
		}
		n := t.UnixMilli()
		p.to = &n
	}
	if p.from != nil && p.to != nil && *p.from > *p.to {
		return p, fmt.Errorf("dateFrom exceeds dateTo")
	}
	return p, nil
}

func (p searchParams) metadataMatch(h searchHit) bool {
	return (p.from == nil || h.Mtime >= *p.from) && (p.to == nil || h.Mtime <= *p.to) && (p.MinSize == nil || h.Size >= *p.MinSize) && (p.MaxSize == nil || h.Size <= *p.MaxSize)
}

func searchTextMatch(mode, query, path, content string) bool {
	q := strings.ToLower(query)
	path = strings.ToLower(path)
	switch mode {
	case "path":
		return strings.Contains(path, q)
	case "content":
		return strings.Contains(strings.ToLower(content), q)
	case "fuzzy":
		runes := []rune(q)
		i := 0
		for _, r := range path {
			if i < len(runes) && r == runes[i] {
				i++
			}
		}
		return i == len(runes)
	default:
		return strings.Contains(path, q) || strings.Contains(strings.ToLower(content), q)
	}
}

var searchInlineTag = regexp.MustCompile(`(?:^|[\s])#([\p{L}\p{N}_/-]+)`)

func searchTagsMatch(content string, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	tags := map[string]bool{}
	add := func(s string) { tags[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "#"))] = true }
	fm, body, _ := util.ParseFrontmatter(content)
	switch v := fm["tags"].(type) {
	case string:
		for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			add(s)
		}
	case []interface{}:
		for _, s := range v {
			if str, ok := s.(string); ok {
				add(str)
			}
		}
	case []string:
		for _, s := range v {
			add(s)
		}
	}
	for _, m := range searchInlineTag.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	for _, s := range requested {
		if !tags[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "#"))] {
			return false
		}
	}
	return true
}

func searchSnippet(content, query string) string {
	r := []rune(content)
	start := 0
	q := []rune(strings.ToLower(query))
	// Rune-based matching keeps both offsets and the 200-character limit UTF-8 safe.
	if len(q) > 0 {
		lower := []rune(strings.ToLower(content))
		for i := 0; i+len(q) <= len(lower); i++ {
			if string(lower[i:i+len(q)]) == string(q) {
				start = i - 60
				if start < 0 {
					start = 0
				}
				break
			}
		}
	}
	end := start + 200
	if end > len(r) {
		end = len(r)
	}
	s := string(r[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(r) {
		s += "…"
	}
	return s
}

func searchMime(path string) string {
	m := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = m[:i]
	}
	if m == "" {
		return "application/octet-stream"
	}
	return m
}

func paginateSearch(hits []searchHit, p searchParams) searchOutput {
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		cmp := 0
		if p.SortBy == "path" {
			cmp = strings.Compare(a.Path, b.Path)
		} else {
			av, bv := a.Mtime, b.Mtime
			if p.SortBy == "ctime" {
				av, bv = a.Ctime, b.Ctime
			}
			if p.SortBy == "size" {
				av, bv = a.Size, b.Size
			}
			if av < bv {
				cmp = -1
			} else if av > bv {
				cmp = 1
			}
		}
		if cmp != 0 {
			if p.SortOrder == "desc" {
				return cmp > 0
			}
			return cmp < 0
		}
		if a.Vault != b.Vault {
			return a.Vault < b.Vault
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.ID < b.ID
	})
	total := len(hits)
	start := (p.Page - 1) * p.PageSize
	if start > total {
		start = total
	}
	end := start + p.PageSize
	if end > total {
		end = total
	}
	results := make([]searchHit, end-start)
	copy(results, hits[start:end])
	totalPages := total / p.PageSize
	if total%p.PageSize != 0 {
		totalPages++
	}
	return searchOutput{Results: results, Total: total, Page: p.Page, PageSize: p.PageSize, HasMore: end < total, TotalPages: totalPages}
}
