package mcp_router

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestConsolidatedDiscoveryKeepsLegacyCalls(t *testing.T) {
	srv := mcpsrv.NewMCPServer("test", "1", mcpsrv.WithHooks(consolidatedDiscoveryHooks()))
	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("called"), nil
	}
	for name := range canonicalMCPTools {
		srv.AddTool(mcp.NewTool(name), handler)
	}
	for _, name := range []string{"note_create_or_update", "note_replace", "note_list", "file_list"} {
		srv.AddTool(mcp.NewTool(name), handler)
	}
	registerCompatibilityAliases(srv)
	response := srv.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)).(mcp.JSONRPCResponse)
	tools := response.Result.(mcp.ListToolsResult).Tools
	require.Len(t, tools, 14)
	for _, tool := range tools {
		require.True(t, canonicalMCPTools[tool.Name])
	}
	for _, name := range []string{"note_list", "file_list", "note_create_or_update", "note_replace", "note_write", "note_search_replace"} {
		message, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": name, "arguments": map[string]any{}}})
		result := srv.HandleMessage(context.Background(), message)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.Contains(t, string(encoded), "called", name)
	}
	require.Equal(t, true, srv.GetTool("note_list").Tool.Meta.AdditionalFields["deprecated"])
}

func TestLegacyPagination(t *testing.T) {
	pager, err := legacyPager(map[string]interface{}{"page": float64(2), "pageSize": float64(100)})
	require.NoError(t, err)
	out := legacyPageOutput(pager, 251)
	require.Equal(t, 3, out.TotalPages)
	require.Equal(t, 251, out.Total)
	require.True(t, out.HasMore)
	pager, err = legacyPager(map[string]interface{}{"page": float64(3), "pageSize": float64(100)})
	require.NoError(t, err)
	require.False(t, legacyPageOutput(pager, 251).HasMore)
	for _, bad := range []any{float64(0), float64(1.5), "2", float64(-1)} {
		_, err = legacyPager(map[string]interface{}{"page": bad})
		require.Error(t, err)
	}
}
