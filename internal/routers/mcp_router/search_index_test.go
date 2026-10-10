package mcp_router

import (
	"context"
	"testing"

	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	"github.com/haierkeys/fast-note-sync-service/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

type indexedTestNotes struct {
	service.NoteService
	pages [][]*dto.NoteDTO
	calls int
}

func (s *indexedTestNotes) WithClient(string, string, string) service.NoteService { return s }
func (s *indexedTestNotes) SearchCandidates(ctx context.Context, uid int64, vault, query, mode string, after int64, limit int) ([]*dto.NoteDTO, int64, error) {
	s.calls++
	if s.calls > len(s.pages) {
		return nil, 0, nil
	}
	next := int64(s.calls)
	if s.calls == len(s.pages) {
		next = 0
	}
	return s.pages[s.calls-1], next, nil
}
func TestIndexedSearchVerifiesCandidatesAndPages(t *testing.T) {
	source := &indexedTestNotes{pages: [][]*dto.NoteDTO{
		{{ID: 1, Path: "false.md", Content: "target scrambled", Mtime: 1}, {ID: 2, Path: "match.md", Content: "TARGET #work", Mtime: 2}},
		{{ID: 3, Path: "other.md", Content: "target #work", Mtime: 3}},
	}}
	a := &app.App{Services: &app.Services{NoteService: source}}
	p := searchParams{Query: "target", Mode: "content", Tags: []string{"work"}, IncludeSnippet: true, Page: 2, PageSize: 1, SortBy: "mtime", SortOrder: "desc"}
	hits, err := searchNotes(context.Background(), a, "A", p)
	if err != nil {
		t.Fatal(err)
	}
	out := paginateSearch(hits, p)
	if source.calls != 2 || out.Total != 2 || len(out.Results) != 1 || out.Results[0].ID != 2 || out.Results[0].Snippet != "TARGET #work" {
		t.Fatalf("unexpected %+v calls=%d", out, source.calls)
	}
}
func TestIndexedSearchContinuesEmptyCandidatePage(t *testing.T) {
	source := &indexedTestNotes{pages: [][]*dto.NoteDTO{nil, {{ID: 5, Path: "match.md", Content: "target"}}}}
	hits, err := searchIndexedNotes(context.Background(), source, "A", searchParams{Query: "target", Mode: "content"})
	if err != nil || len(hits) != 1 || hits[0].ID != 5 || source.calls != 2 {
		t.Fatalf("empty page truncated search: %+v err=%v calls=%d", hits, err, source.calls)
	}
}

func TestIndexedSearchRejectsNonAdvancingCursor(t *testing.T) {
	_, err := searchIndexedNotes(context.Background(), stuckIndex{}, "A", searchParams{})
	if err == nil {
		t.Fatal("expected cursor error")
	}
}

func TestIndexedSearchChecksVaultBeforeBackend(t *testing.T) {
	source := &indexedTestNotes{}
	a := &app.App{Infra: &app.Infra{}, Services: &app.Services{NoteService: source, VaultService: &searchTestVaults{names: []string{"secret", "A"}}}}
	srv := mcpsrv.NewMCPServer("search-test", "1")
	registerSearchTools(srv, a)
	ctx := context.WithValue(context.Background(), "vaults", "A")
	var req mcp.CallToolRequest
	req.Params.Name = "vault_search"
	req.Params.Arguments = map[string]interface{}{"query": "target", "vaults": []string{"secret"}}
	result, err := srv.GetTool("vault_search").Handler(ctx, req)
	if err != nil || !result.IsError || source.calls != 0 {
		t.Fatalf("denied vault reached index: %+v err=%v calls=%d", result, err, source.calls)
	}
}

type stuckIndex struct{}

func (stuckIndex) SearchCandidates(context.Context, int64, string, string, string, int64, int) ([]*dto.NoteDTO, int64, error) {
	return nil, 1, nil
}
