package mcp_router

import (
	"context"
	"fmt"
	"math"

	pkgapp "github.com/haierkeys/fast-note-sync-service/pkg/app"
	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

var canonicalMCPTools = map[string]bool{
	"note_search": true, "note_get": true, "note_write": true, "note_delete": true,
	"note_rename": true, "note_links": true, "note_search_replace": true,
	"file_search": true, "file_read": true, "file_write": true,
	"trash_action": true, "vault_manage": true, "vault_search": true, "note_history_search": true,
}

// Filter discovery responses only: SDK tool filters also deny calls, breaking aliases.
func consolidatedDiscoveryHooks() *mcpsrv.Hooks {
	hooks := &mcpsrv.Hooks{}
	hooks.AddAfterListTools(func(_ context.Context, _ any, _ *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		tools := make([]mcp.Tool, 0, len(result.Tools))
		for _, tool := range result.Tools {
			if canonicalMCPTools[tool.Name] {
				tools = append(tools, tool)
			}
		}
		result.Tools = tools
	})
	return hooks
}

func registerCompatibilityAliases(srv *mcpsrv.MCPServer) {
	aliases := map[string]string{"note_create_or_update": "note_write", "note_replace": "note_search_replace"}
	for old, name := range aliases {
		original := srv.GetTool(old)
		tool := original.Tool
		tool.Name = name
		srv.AddTool(tool, original.Handler)
	}
	for name, original := range srv.ListTools() {
		if canonicalMCPTools[name] {
			continue
		}
		tool := original.Tool
		metadata := make(map[string]any)
		if tool.Meta != nil {
			for k, v := range tool.Meta.AdditionalFields {
				metadata[k] = v
			}
		}
		metadata["deprecated"] = true
		replacement := legacyReplacement(name)
		metadata["seeAlso"] = replacement
		tool.Meta = &mcp.Meta{AdditionalFields: metadata}
		tool.Description = fmt.Sprintf("Deprecated; use %s. %s", replacement, tool.Description)
		srv.AddTool(tool, original.Handler)
	}
}

func legacyReplacement(name string) string {
	switch name {
	case "note_list":
		return "note_search"
	case "file_list", "file_get_info":
		return "file_search"
	case "note_get_backlinks", "note_get_outlinks":
		return "note_links"
	case "note_create_or_update", "note_append", "note_prepend", "note_patch_frontmatter":
		return "note_write"
	case "note_replace":
		return "note_search_replace"
	case "vault_list", "vault_get", "vault_create_or_update", "vault_delete":
		return "vault_manage"
	default:
		return "trash_action"
	}
}

type legacyPagination struct {
	Page       int  `json:"page"`
	PageSize   int  `json:"pageSize"`
	Total      int  `json:"total"`
	TotalPages int  `json:"totalPages"`
	HasMore    bool `json:"hasMore"`
}

func legacyPager(args map[string]interface{}) (*pkgapp.Pager, error) {
	values := map[string]int{"page": 1, "pageSize": 100}
	for key, fallback := range values {
		raw, ok := args[key]
		if !ok {
			values[key] = fallback
			continue
		}
		value, ok := raw.(float64)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) || value < 1 || value != math.Trunc(value) || value > 2147483647 {
			return nil, fmt.Errorf("%s must be a positive integer", key)
		}
		values[key] = int(value)
	}
	return &pkgapp.Pager{Page: values["page"], PageSize: pkgapp.GetPageSize(values["pageSize"])}, nil
}

func legacyPageOutput(pager *pkgapp.Pager, total int) legacyPagination {
	pages := total / pager.PageSize
	if total%pager.PageSize != 0 {
		pages++
	}
	return legacyPagination{Page: pager.Page, PageSize: pager.PageSize, Total: total, TotalPages: pages, HasMore: pager.Page < pages}
}
