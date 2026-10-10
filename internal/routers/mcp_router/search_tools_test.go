package mcp_router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	"github.com/haierkeys/fast-note-sync-service/internal/service"
	pkgapp "github.com/haierkeys/fast-note-sync-service/pkg/app"
	"github.com/haierkeys/fast-note-sync-service/pkg/timex"
)

func TestSearchHandlerCrossVaultAndDeniedReads(t *testing.T) {
	ns := &searchTestNotes{notes: map[string][]*dto.NoteNoContentDTO{"A": {{ID: 1, Path: "a.md", Mtime: 1}}, "B": {{ID: 2, Path: "b.md", Mtime: 2}}, "secret": {{ID: 3, Path: "secret.md", Mtime: 99}}}, bodies: map[int64]string{1: "target #work", 2: "target #work", 3: "target #work"}}
	a := &app.App{Infra: &app.Infra{}, Services: &app.Services{NoteService: ns, VaultService: &searchTestVaults{names: []string{"secret", "A", "B"}}}}
	srv := mcpsrv.NewMCPServer("search-test", "1")
	registerSearchTools(srv, a)
	ctx := context.WithValue(context.Background(), "vaults", "A,B")
	var req mcp.CallToolRequest
	req.Params.Name = "vault_search"
	req.Params.Arguments = map[string]interface{}{"query": "target", "tags": []string{"work"}, "includeSnippet": true, "page": 2, "pageSize": 1}
	result, err := srv.GetTool("vault_search").Handler(ctx, req)
	if err != nil || result.IsError {
		t.Fatalf("handler: %v %+v", err, result)
	}
	b, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out searchOutput
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || out.TotalPages != 2 || out.Results[0].Vault != "A" || out.Results[0].Snippet != "target #work" || ns.calls != 2 || ns.gets != 2 {
		t.Fatalf("global handler output or secret read: %+v reads=%d/%d", out, ns.calls, ns.gets)
	}
	before := ns.calls
	req.Params.Arguments = map[string]interface{}{"vaults": []string{"secret"}}
	result, err = srv.GetTool("vault_search").Handler(ctx, req)
	if err != nil || !result.IsError || ns.calls != before {
		t.Fatal("forbidden vault read")
	}
	ctx = context.WithValue(ctx, "scope", "f:file_r")
	result, err = srv.GetTool("vault_search").Handler(ctx, req)
	if err != nil || !result.IsError || ns.calls != before {
		t.Fatal("permission did not stop reads")
	}
	ctx = context.WithValue(context.Background(), "vaults", "A,B")
	ctx = context.WithValue(ctx, "default_vault_name", "secret")
	names, err := searchVaultNames(ctx, a, searchParams{}, false)
	if err != nil || names[0] != "A" {
		t.Fatalf("denied header default fallback: %v %v", names, err)
	}
	p := searchParams{Page: 3, PageSize: 1, SortBy: "mtime", SortOrder: "desc"}
	empty := paginateSearch([]searchHit{{ID: 1}, {ID: 2}}, p)
	if len(empty.Results) != 0 || empty.TotalPages != 2 || empty.HasMore {
		t.Fatalf("out of range page: %+v", empty)
	}
}

type searchTestNotes struct {
	service.NoteService
	notes  map[string][]*dto.NoteNoContentDTO
	bodies map[int64]string
	calls  int
	gets   int
}

func (s *searchTestNotes) WithClient(string, string, string) service.NoteService { return s }
func (s *searchTestNotes) List(ctx context.Context, uid int64, p *dto.NoteListRequest, pager *pkgapp.Pager) ([]*dto.NoteNoContentDTO, int, error) {
	s.calls++
	if p.IsRecycle {
		return nil, 0, nil
	}
	all := s.notes[p.Vault]
	start := (pager.Page - 1) * pager.PageSize
	if start > len(all) {
		start = len(all)
	}
	end := start + pager.PageSize
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], len(all), nil
}
func (s *searchTestNotes) Get(ctx context.Context, uid int64, p *dto.NoteGetRequest) (*dto.NoteDTO, error) {
	s.gets++
	for _, n := range s.notes[p.Vault] {
		if n.Path == p.Path {
			return &dto.NoteDTO{ID: n.ID, Content: s.bodies[n.ID]}, nil
		}
	}
	return nil, errors.New("missing")
}

type searchTestVaults struct {
	service.VaultService
	names []string
}

func (s *searchTestVaults) List(context.Context, int64) ([]*dto.VaultDTO, error) {
	v := []*dto.VaultDTO{}
	for _, name := range s.names {
		v = append(v, &dto.VaultDTO{Name: name})
	}
	return v, nil
}

type searchTestFiles struct {
	service.FileService
	files []*dto.FileDTO
	calls int
}

func (s *searchTestFiles) WithClient(string, string, string) service.FileService { return s }
func (s *searchTestFiles) List(ctx context.Context, uid int64, p *dto.FileListRequest, pager *pkgapp.Pager) ([]*dto.FileDTO, int, error) {
	s.calls++
	start := (pager.Page - 1) * pager.PageSize
	if start > len(s.files) {
		start = len(s.files)
	}
	end := start + pager.PageSize
	if end > len(s.files) {
		end = len(s.files)
	}
	return s.files[start:end], len(s.files), nil
}

type searchTestHistory struct {
	service.NoteHistoryService
	versions []*dto.NoteHistoryNoContentDTO
	content  map[int64]string
	calls    int
}

func (s *searchTestHistory) List(ctx context.Context, uid int64, p *dto.NoteHistoryListRequest, pager *pkgapp.Pager) ([]*dto.NoteHistoryNoContentDTO, int64, error) {
	s.calls++
	start := (pager.Page - 1) * pager.PageSize
	if start > len(s.versions) {
		start = len(s.versions)
	}
	end := start + pager.PageSize
	if end > len(s.versions) {
		end = len(s.versions)
	}
	return s.versions[start:end], int64(len(s.versions)), nil
}
func (s *searchTestHistory) Get(ctx context.Context, uid, id int64) (*dto.NoteHistoryDTO, error) {
	return &dto.NoteHistoryDTO{ID: id, Content: s.content[id]}, nil
}

func TestSearchGlobalFilterSortPaginationBeyondFirstCandidatePage(t *testing.T) {
	ns := &searchTestNotes{notes: map[string][]*dto.NoteNoContentDTO{}, bodies: map[int64]string{}}
	for i := 1; i <= 205; i++ {
		id := int64(i)
		ns.notes["A"] = append(ns.notes["A"], &dto.NoteNoContentDTO{ID: id, Path: fmt.Sprintf("note-%03d.md", i), Mtime: id, Size: 50})
		ns.bodies[id] = "irrelevant"
		if i > 100 {
			ns.bodies[id] = "---\ntags: [work, urgent]\n---\nmeeting 内容 #inline"
		}
	}
	a := &app.App{Services: &app.Services{NoteService: ns}}
	p, err := decodeSearchParams(map[string]interface{}{"query": "meeting", "mode": "content", "tags": []string{"work", "inline"}, "page": 2, "pageSize": 3, "includeSnippet": true}, "note_search")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := searchNotes(context.Background(), a, "A", p)
	if err != nil {
		t.Fatal(err)
	}
	out := paginateSearch(hits, p)
	if out.Total != 105 || len(out.Results) != 3 || out.Results[0].ID != 202 || !out.HasMore || ns.calls != 3 {
		t.Fatalf("wrong filtered global page: %+v calls=%d", out, ns.calls)
	}
	if !strings.Contains(out.Results[0].Snippet, "meeting") {
		t.Fatal("snippet missing match")
	}
	// Merge a newer result from another vault before paginating, never page each vault.
	hits = append(hits, searchHit{Vault: "B", ID: 999, Path: "new.md", Mtime: 999})
	out = paginateSearch(hits, p)
	if out.Results[0].ID != 203 || out.Total != 106 {
		t.Fatalf("wrong merged page: %+v", out)
	}
}

func TestSearchVaultAllowlistBeforeReads(t *testing.T) {
	a := &app.App{Services: &app.Services{VaultService: &searchTestVaults{names: []string{"secret", "allowed"}}}}
	ctx := context.WithValue(context.Background(), "vaults", "allowed")
	names, err := searchVaultNames(ctx, a, searchParams{}, true)
	if err != nil || len(names) != 1 || names[0] != "allowed" {
		t.Fatalf("allowlist: %v %v", names, err)
	}
	names, err = searchVaultNames(ctx, a, searchParams{}, false)
	if err != nil || names[0] != "allowed" {
		t.Fatalf("safe default: %v %v", names, err)
	}
	_, err = searchVaultNames(ctx, a, searchParams{Vault: "secret"}, false)
	if err == nil {
		t.Fatal("explicit forbidden vault accepted")
	}
	_, err = searchVaultNames(ctx, a, searchParams{Vaults: []string{"secret"}}, true)
	if err == nil {
		t.Fatal("cross vault forbidden selection accepted")
	}
}

func TestSearchFileMimeFilterBeforePagination(t *testing.T) {
	fs := &searchTestFiles{}
	for i := 1; i <= 105; i++ {
		path := fmt.Sprintf("%03d.txt", i)
		if i > 100 {
			path = fmt.Sprintf("%03d.PNG", i)
		}
		fs.files = append(fs.files, &dto.FileDTO{ID: int64(i), Path: path, Mtime: int64(i), Size: 100})
	}
	a := &app.App{Services: &app.Services{FileService: fs}}
	p, err := decodeSearchParams(map[string]interface{}{"mimePrefix": "IMAGE/", "pageSize": 2}, "file_search")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := searchFiles(context.Background(), a, "A", p)
	if err != nil {
		t.Fatal(err)
	}
	out := paginateSearch(hits, p)
	if out.Total != 5 || out.Results[0].ID != 105 || fs.calls != 2 {
		t.Fatalf("MIME global page: %+v calls=%d", out, fs.calls)
	}
	if searchMime("unknown.unknownextension") != "application/octet-stream" {
		t.Fatal("unknown MIME fallback")
	}
}

func TestSearchHistoryKeywordPathAndAllPages(t *testing.T) {
	ns := &searchTestNotes{notes: map[string][]*dto.NoteNoContentDTO{"A": {{ID: 1, Path: "current.md"}, {ID: 2, Path: "other.md"}}}}
	hs := &searchTestHistory{content: map[int64]string{}}
	for i := 1; i <= 103; i++ {
		id := int64(i)
		hs.versions = append(hs.versions, &dto.NoteHistoryNoContentDTO{ID: id, NoteID: 1, Path: "old.md", Version: id, CreatedAt: timex.Time(time.UnixMilli(id))})
		hs.content[id] = "no match"
		if i > 100 {
			hs.content[id] = "historical keyword #work"
		}
	}
	a := &app.App{Services: &app.Services{NoteService: ns, NoteHistoryService: hs}}
	p, err := decodeSearchParams(map[string]interface{}{"path": "current.md", "keyword": "keyword", "tags": []string{"work"}, "pageSize": 2}, "note_history_search")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := searchHistories(context.Background(), a, "A", p)
	if err != nil {
		t.Fatal(err)
	}
	out := paginateSearch(hits, p)
	if out.Total != 3 || out.Results[0].ID != 103 || out.Results[0].Path != "old.md" || hs.calls != 2 {
		t.Fatalf("history page: %+v calls=%d", out, hs.calls)
	}
}

func TestSearchValidationModesDatesSizesAndUnicode(t *testing.T) {
	for _, args := range []map[string]interface{}{{"page": 0}, {"pageSize": 101}, {"mode": "typo"}, {"sortBy": "unknown"}, {"includeSnippet": "true"}, {"dateFrom": "2026-10-10"}, {"minSize": -1}, {"minSize": 5, "maxSize": 2}, {"ignored": true}, {"page": 1.5}, {"tags": []string{""}}} {
		if _, err := decodeSearchParams(args, "note_search"); err == nil {
			t.Fatalf("accepted invalid params: %v", args)
		}
	}
	if _, err := decodeSearchParams(map[string]interface{}{"tags": []string{"work"}}, "file_search"); err == nil {
		t.Fatal("silently ignored file tags")
	}
	p, err := decodeSearchParams(map[string]interface{}{"dateFrom": "2026-10-10T00:00:00Z", "dateTo": "2026-10-10T01:00:00Z", "minSize": 0, "maxSize": 0}, "note_search")
	if err != nil {
		t.Fatal(err)
	}
	if !p.metadataMatch(searchHit{Mtime: *p.from}) || p.metadataMatch(searchHit{Mtime: *p.to + 1}) || p.metadataMatch(searchHit{Mtime: *p.from, Size: 1}) {
		t.Fatal("inclusive date/zero size bounds")
	}
	for _, tc := range []struct {
		mode, path, body string
		want             bool
	}{{"auto", "x.md", "KEYWORD", true}, {"content", "keyword.md", "none", false}, {"path", "KEYWORD.md", "none", true}, {"fuzzy", "k-e-y-w-o-r-d.md", "", true}, {"fuzzy", "drowyek.md", "keyword", false}} {
		if got := searchTextMatch(tc.mode, "keyword", tc.path, tc.body); got != tc.want {
			t.Fatalf("mode %s got %v", tc.mode, got)
		}
	}
	snippet := searchSnippet(strings.Repeat("你", 300)+"目标"+strings.Repeat("好", 300), "目标")
	if !utf8.ValidString(snippet) || !strings.Contains(snippet, "目标") || utf8.RuneCountInString(snippet) > 202 {
		t.Fatalf("invalid snippet: %s", snippet)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err = collectSearchPages(ctx, func(*pkgapp.Pager) ([]int, error) { calls++; return nil, nil })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("cancellation did not stop reads")
	}
}
