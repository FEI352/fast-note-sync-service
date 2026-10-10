package mcp_router

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	"github.com/haierkeys/fast-note-sync-service/internal/service"
	pkgapp "github.com/haierkeys/fast-note-sync-service/pkg/app"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

func searchToolSchema(kind string) json.RawMessage {
	props := map[string]interface{}{}
	str := func(k, description string) {
		props[k] = map[string]interface{}{"type": "string", "description": description}
	}
	str("query", "Case-insensitive literal substring; omit to list all matching filters.")
	str("sortBy", "Sort globally by mtime (default), ctime, path or size.")
	str("sortOrder", "asc or desc (default). Ties use vault, path and ID ascending.")
	str("dateFrom", "Inclusive modification-time lower bound, RFC3339 with timezone; history uses version creation time.")
	str("dateTo", "Inclusive modification-time upper bound, RFC3339 with timezone; history uses version creation time.")
	for _, k := range []string{"minSize", "maxSize"} {
		props[k] = map[string]interface{}{"type": "integer", "minimum": 0, "description": "Inclusive size bound in bytes."}
	}
	props["page"] = map[string]interface{}{"type": "integer", "minimum": 1, "default": 1}
	props["pageSize"] = map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}
	if kind == "vault_search" {
		props["vaults"] = map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional exact vault names; omit to search all token-accessible vaults."}
	} else {
		str("vault", "Exact vault name; omit or use default for the first token-accessible vault or client default.")
	}
	if kind != "file_search" {
		props["mode"] = map[string]interface{}{"type": "string", "enum": []string{"auto", "path", "content", "fuzzy"}, "default": "auto", "description": "auto matches path OR body; path and content match only that field; fuzzy matches query characters in order in the path."}
		props["includeSnippet"] = map[string]interface{}{"type": "boolean", "default": false, "description": "Include up to 200 Unicode characters of body around the first literal query match."}
		props["tags"] = map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Require every tag, case-insensitively, from YAML tags or whitespace-prefixed inline hashtags."}
	}
	if kind == "file_search" {
		str("mimePrefix", "Case-insensitive prefix of MIME inferred from the filename extension; unknown extensions are application/octet-stream.")
	}
	if kind == "note_history_search" {
		str("path", "Optional exact current vault-relative note path, including recycled notes. Omit to search histories reachable from all current/recycled notes.")
		str("keyword", "Alias of query. Supply only one. Matches historical path/body according to mode.")
	}
	b, _ := json.Marshal(map[string]interface{}{"type": "object", "properties": props, "additionalProperties": false})
	return b
}

// registerSearchTools adds the read-only search surface; the parent owns server wiring.
func registerSearchTools(srv *mcpsrv.MCPServer, appContainer *app.App) {
	descriptions := map[string]string{
		"note_search":         "Search notes in one vault, returning globally sorted metadata and optional snippets with exact filtered totals.",
		"vault_search":        "Search notes across token-accessible vaults, merge matches, then sort and paginate globally.",
		"file_search":         "Search file paths and filter extension-inferred MIME and metadata, then sort and paginate globally.",
		"note_history_search": "Search stored historical path/content snapshots reachable from current or recycled notes. Orphaned histories and permanently removed notes are unavailable through this service.",
	}
	for _, kind := range []string{"note_search", "vault_search", "file_search", "note_history_search"} {
		name := kind
		scopes := []string{"notes:read"}
		permission := "note_r"
		if name == "file_search" {
			scopes = []string{"files:read"}
			permission = "file_r"
		}
		tool := mcp.NewTool(name, mcp.WithDescription(descriptions[name]), mcp.WithRawInputSchema(searchToolSchema(name)), mcp.WithOutputSchema[searchOutput]())
		srv.AddTool(readOnlyMCPTool(tool, appContainer.Config(), scopes...), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := checkPermission(ctx, permission); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			p, err := decodeSearchParams(getArgs(req), name)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			vaults, err := searchVaultNames(ctx, appContainer, p, name == "vault_search")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			hits := []searchHit{}
			for _, vault := range vaults {
				var part []searchHit
				switch name {
				case "file_search":
					part, err = searchFiles(ctx, appContainer, vault, p)
				case "note_history_search":
					part, err = searchHistories(ctx, appContainer, vault, p)
				default:
					part, err = searchNotes(ctx, appContainer, vault, p)
				}
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				hits = append(hits, part...)
			}
			out := paginateSearch(hits, p)
			fallback, _ := json.Marshal(out)
			return mcp.NewToolResultStructured(out, string(fallback)), nil
		})
	}
}

func searchVaultNames(ctx context.Context, a *app.App, p searchParams, cross bool) ([]string, error) {
	if !cross && p.Vault != "" && !strings.EqualFold(p.Vault, "default") {
		if err := checkVaultAccess(ctx, p.Vault); err != nil {
			return nil, err
		}
		return []string{p.Vault}, nil
	}
	if !cross {
		if v, _ := ctx.Value("default_vault_name").(string); v != "" {
			if err := checkVaultAccess(ctx, v); err == nil {
				return []string{v}, nil
			}
		}
	}
	// Listing vault metadata is the only discovery service. Filter names before any note/file/history read.
	vaults, err := a.VaultService.List(ctx, getUIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	available := map[string]bool{}
	names := []string{}
	for _, v := range vaults {
		if err := checkVaultAccess(ctx, v.Name); err == nil {
			available[v.Name] = true
			names = append(names, v.Name)
		}
	}
	if cross && len(p.Vaults) > 0 {
		names = []string{}
		seen := map[string]bool{}
		for _, v := range p.Vaults {
			if err := checkVaultAccess(ctx, v); err != nil {
				return nil, err
			}
			if !available[v] {
				return nil, fmt.Errorf("vault not found: %s", v)
			}
			if !seen[v] {
				names = append(names, v)
				seen[v] = true
			}
		}
	}
	if !cross {
		if len(names) == 0 {
			return nil, fmt.Errorf("no accessible vault")
		}
		names = names[:1]
	}
	return names, nil
}

// Full candidate pagination is necessary because service lists do not implement tags,
// MIME or date filters, and content search silently becomes path search without FTS.
// No candidate cap is applied: cancellation/errors fail instead of returning partial totals.
func collectSearchPages[T any](ctx context.Context, fetch func(*pkgapp.Pager) ([]T, error)) ([]T, error) {
	all := []T{}
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := fetch(&pkgapp.Pager{Page: page, PageSize: 100})
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
}

func searchNoteCandidates(ctx context.Context, a *app.App, vault string, recycle bool) ([]*dto.NoteNoContentDTO, error) {
	return collectSearchPages(ctx, func(pager *pkgapp.Pager) ([]*dto.NoteNoContentDTO, error) {
		notes, _, err := a.NoteService.WithClient(getClientInfoFromContext(ctx)).List(ctx, getUIDFromContext(ctx), &dto.NoteListRequest{Vault: vault, IsRecycle: recycle, SortBy: "path", SortOrder: "asc"}, pager)
		return notes, err
	})
}

// indexedNoteSearch is optional so integrations without the accelerated repository
// retain the same complete-search fallback.
type indexedNoteSearch = service.NoteSearchService

func searchIndexedNotes(ctx context.Context, source indexedNoteSearch, vault string, p searchParams) ([]searchHit, error) {
	hits := []searchHit{}
	var cursor int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		notes, next, err := source.SearchCandidates(ctx, getUIDFromContext(ctx), vault, p.Query, p.Mode, cursor, 100)
		if err != nil {
			return nil, err
		}
		for _, n := range notes {
			h := searchHit{Vault: vault, ID: n.ID, Path: n.Path, Version: n.Version, Size: n.Size, Ctime: n.Ctime, Mtime: n.Mtime}
			if !p.metadataMatch(h) || !searchTextMatch(p.Mode, p.Query, n.Path, n.Content) || !searchTagsMatch(n.Content, p.Tags) {
				continue
			}
			if p.IncludeSnippet {
				h.Snippet = searchSnippet(n.Content, p.Query)
			}
			hits = append(hits, h)
		}
		if next == 0 {
			return hits, nil
		}
		if next <= cursor {
			return nil, fmt.Errorf("search candidate cursor did not advance")
		}
		cursor = next
	}
}

func searchNotes(ctx context.Context, a *app.App, vault string, p searchParams) ([]searchHit, error) {
	if p.Query != "" && p.Mode != "fuzzy" {
		if source, ok := a.NoteService.WithClient(getClientInfoFromContext(ctx)).(indexedNoteSearch); ok {
			return searchIndexedNotes(ctx, source, vault, p)
		}
	}
	notes, err := searchNoteCandidates(ctx, a, vault, false)
	if err != nil {
		return nil, err
	}
	hits := []searchHit{}
	for _, n := range notes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h := searchHit{Vault: vault, ID: n.ID, Path: n.Path, Version: n.Version, Size: n.Size, Ctime: n.Ctime, Mtime: n.Mtime}
		if !p.metadataMatch(h) {
			continue
		}
		content := ""
		needBody := len(p.Tags) > 0 || p.IncludeSnippet || p.Query != "" && (p.Mode == "content" || p.Mode == "auto" && !searchTextMatch("path", p.Query, n.Path, ""))
		if needBody {
			full, e := a.NoteService.WithClient(getClientInfoFromContext(ctx)).Get(ctx, getUIDFromContext(ctx), &dto.NoteGetRequest{Vault: vault, Path: n.Path, PathHash: n.PathHash})
			if e != nil {
				return nil, e
			}
			content = full.Content
		}
		if !searchTextMatch(p.Mode, p.Query, n.Path, content) || !searchTagsMatch(content, p.Tags) {
			continue
		}
		if p.IncludeSnippet {
			h.Snippet = searchSnippet(content, p.Query)
		}
		hits = append(hits, h)
	}
	return hits, nil
}

func searchFiles(ctx context.Context, a *app.App, vault string, p searchParams) ([]searchHit, error) {
	files, err := collectSearchPages(ctx, func(pager *pkgapp.Pager) ([]*dto.FileDTO, error) {
		files, _, e := a.FileService.WithClient(getClientInfoFromContext(ctx)).List(ctx, getUIDFromContext(ctx), &dto.FileListRequest{Vault: vault, SortBy: "path", SortOrder: "asc"}, pager)
		return files, e
	})
	if err != nil {
		return nil, err
	}
	hits := []searchHit{}
	for _, f := range files {
		h := searchHit{Vault: vault, ID: f.ID, Path: f.Path, Size: f.Size, Ctime: f.Ctime, Mtime: f.Mtime, Mime: searchMime(f.Path)}
		if p.metadataMatch(h) && searchTextMatch("path", p.Query, f.Path, "") && strings.HasPrefix(strings.ToLower(h.Mime), strings.ToLower(p.MimePrefix)) {
			hits = append(hits, h)
		}
	}
	return hits, nil
}

func searchHistories(ctx context.Context, a *app.App, vault string, p searchParams) ([]searchHit, error) {
	hits := []searchHit{}
	seen := map[int64]bool{}
	for _, recycle := range []bool{false, true} {
		var paths []string
		notes, err := searchNoteCandidates(ctx, a, vault, recycle)
		if err != nil {
			return nil, err
		}
		for _, n := range notes {
			if p.Path == "" || n.Path == p.Path {
				paths = append(paths, n.Path)
			}
		}
		for _, path := range paths {
			versions, err := collectSearchPages(ctx, func(pager *pkgapp.Pager) ([]*dto.NoteHistoryNoContentDTO, error) {
				v, _, e := a.NoteHistoryService.List(ctx, getUIDFromContext(ctx), &dto.NoteHistoryListRequest{Vault: vault, Path: path, PathHash: util.EncodeHash32(path), IsRecycle: recycle}, pager)
				return v, e
			})
			if err != nil {
				// With an exact path, resolve its active/recycled state before listing instead
				// of suppressing arbitrary storage failures.
				return nil, err
			}
			for _, v := range versions {
				if seen[v.ID] {
					continue
				}
				seen[v.ID] = true
				full, e := a.NoteHistoryService.Get(ctx, getUIDFromContext(ctx), v.ID)
				if e != nil {
					return nil, e
				}
				h := searchHit{Vault: vault, ID: v.ID, NoteID: v.NoteID, Path: v.Path, Version: v.Version, Size: int64(len(full.Content)), Ctime: v.CreatedAt.UnixMilli(), Mtime: v.CreatedAt.UnixMilli()}
				if !p.metadataMatch(h) || !searchTextMatch(p.Mode, p.Query, h.Path, full.Content) || !searchTagsMatch(full.Content, p.Tags) {
					continue
				}
				if p.IncludeSnippet {
					h.Snippet = searchSnippet(full.Content, p.Query)
				}
				hits = append(hits, h)
			}
		}
	}
	return hits, nil
}
