package mcp_router

import (
	"context"
	"math"
	"testing"

	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func consolidatedTestRequest(name string, args map[string]interface{}) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Name, req.Params.Arguments = name, args
	return req
}

func TestConsolidatedToolsDispatch(t *testing.T) {
	srv := mcpsrv.NewMCPServer("test", "1")
	var calls []string
	var received map[string]interface{}
	names := []string{"note_get_backlinks", "note_get_outlinks", "note_delete", "note_restore", "note_recycle_clear", "note_rename", "file_delete", "file_restore", "file_recycle_clear", "file_rename", "folder_delete", "vault_list", "vault_get", "vault_create_or_update", "vault_delete"}
	for _, name := range names {
		srv.AddTool(mcp.NewTool(name), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls = append(calls, req.Params.Name)
			received = getArgs(req)
			return mcp.NewToolResultStructured(map[string]interface{}{"source": req.Params.Name}, "ok"), nil
		})
	}
	registerConsolidatedTools(srv, &app.App{Infra: &app.Infra{}, Services: &app.Services{}})
	for _, name := range names {
		require.NotNil(t, srv.GetTool(name), "legacy tool retained")
	}
	for _, kind := range []string{"note", "file", "folder"} {
		for _, action := range []string{"delete", "restore", "purge", "rename"} {
			if kind == "folder" && action != "delete" {
				continue
			}
			args := map[string]interface{}{"type": kind, "action": action, "path": "a", "oldPath": "a", "newPath": "b", "vault": "v"}
			calls = nil
			result, err := srv.GetTool("trash_action").Handler(context.Background(), consolidatedTestRequest("trash_action", args))
			require.NoError(t, err)
			require.False(t, result.IsError)
			suffix := action
			if action == "purge" {
				suffix = "recycle_clear"
			}
			require.Equal(t, []string{kind + "_" + suffix}, calls)
			require.Equal(t, args, received)
		}
	}
	for action, target := range map[string]string{"list": "vault_list", "get": "vault_get", "create": "vault_create_or_update", "rename": "vault_create_or_update", "delete": "vault_delete"} {
		args := map[string]interface{}{"action": action, "name": "New"}
		if action != "create" {
			args["id"] = float64(7)
		}
		calls = nil
		result, err := srv.GetTool("vault_manage").Handler(context.Background(), consolidatedTestRequest("vault_manage", args))
		require.NoError(t, err)
		require.False(t, result.IsError)
		require.Equal(t, []string{target}, calls)
		if action == "create" || action == "rename" {
			require.Equal(t, "New", received["vault"])
		}
		require.NotContains(t, args, "vault", "request arguments remain unmodified")
	}
	for _, direction := range []string{"", "both", "in", "out", "backlinks", "outlinks"} {
		args := map[string]interface{}{"path": "a"}
		if direction != "" {
			args["direction"] = direction
		}
		calls = nil
		result, err := srv.GetTool("note_links").Handler(context.Background(), consolidatedTestRequest("note_links", args))
		require.NoError(t, err)
		require.False(t, result.IsError)
		combined := result.StructuredContent.(mcpCombinedNoteLinksOutput)
		switch direction {
		case "", "both":
			require.Equal(t, []string{"note_get_backlinks", "note_get_outlinks"}, calls)
			require.NotNil(t, combined.Backlinks)
			require.NotNil(t, combined.Outlinks)
		case "in", "backlinks":
			require.Equal(t, []string{"note_get_backlinks"}, calls)
			require.Nil(t, combined.Outlinks)
		default:
			require.Equal(t, []string{"note_get_outlinks"}, calls)
			require.Nil(t, combined.Backlinks)
		}
	}
}

func TestConsolidatedActionValidation(t *testing.T) {
	for _, kind := range []string{"note", "file", "folder"} {
		for _, action := range []string{"delete", "restore", "purge", "rename"} {
			args := map[string]interface{}{"type": kind, "action": action}
			_, err := consolidatedTrashTarget(args)
			require.Error(t, err)
			if kind == "folder" && action != "delete" {
				require.Contains(t, err.Error(), "unsupported")
			}
		}
	}
	for _, args := range []map[string]interface{}{
		{}, {"type": "other", "action": "delete", "path": "a"}, {"type": "note", "action": "other", "path": "a"},
		{"type": "file", "action": "rename", "oldPath": "a"}, {"type": "file", "action": "rename", "newPath": "b"},
		{"type": "note", "action": "purge", "path": "  "}, {"type": "folder", "action": "delete", "path": "/"},
	} {
		_, err := consolidatedTrashTarget(args)
		require.Error(t, err)
	}
	for _, args := range []map[string]interface{}{
		{}, {"action": "other"}, {"action": "get"}, {"action": "delete", "id": 0}, {"action": "rename", "id": 1},
		{"action": "create"}, {"action": "create", "name": "x", "id": 1}, {"action": "create", "name": "x", "vault": "y"},
		{"action": "get", "id": 1.5}, {"action": "get", "id": math.Inf(1)}, {"action": "get", "id": math.NaN()},
		{"action": "get", "id": 9223372036854775808.0}, {"action": "get", "id": "1"},
		{"action": "create", "name": []string{"x"}, "vault": []string{"x"}},
		{"action": "create", "name": "x", "vault": []string{"x"}},
	} {
		_, _, err := consolidatedVaultTarget(args)
		require.Error(t, err)
	}
	for _, id := range []interface{}{int(1), int64(1), float64(1)} {
		_, _, err := consolidatedVaultTarget(map[string]interface{}{"action": "get", "id": id})
		require.NoError(t, err)
	}
	_, args, err := consolidatedVaultTarget(map[string]interface{}{"action": "create", "vault": "v", "id": 0})
	require.NoError(t, err)
	require.Equal(t, "v", args["vault"])
}

func TestConsolidatedToolsPreserveAuthorization(t *testing.T) {
	srv := mcpsrv.NewMCPServer("test", "1")
	container := &app.App{Infra: &app.Infra{}, Services: &app.Services{}}
	registerNoteTools(srv, container, nil)
	registerFileTools(srv, container, nil)
	registerFolderTools(srv, container, nil)
	registerVaultTools(srv, container)
	registerConsolidatedTools(srv, container)
	denied := context.WithValue(context.Background(), "scope", "f:none")
	for _, tool := range []string{"note_links", "trash_action", "vault_manage"} {
		args := map[string]interface{}{"path": "a", "type": "note", "action": "delete", "id": 1}
		if tool == "vault_manage" {
			args["action"] = "get"
		}
		result, err := srv.GetTool(tool).Handler(denied, consolidatedTestRequest(tool, args))
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Contains(t, result.Content[0].(mcp.TextContent).Text, "permission denied")
	}
	restricted := context.WithValue(context.Background(), "vaults", "Allowed")
	for _, kind := range []string{"note", "file", "folder"} {
		result, err := srv.GetTool("trash_action").Handler(restricted, consolidatedTestRequest("trash_action", map[string]interface{}{"type": kind, "action": "delete", "vault": "Denied", "path": "a"}))
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Contains(t, result.Content[0].(mcp.TextContent).Text, "vault access restricted")
	}
	result, err := srv.GetTool("vault_manage").Handler(restricted, consolidatedTestRequest("vault_manage", map[string]interface{}{"action": "create", "name": "New"}))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, "cannot create")
}

func TestConsolidatedMetadata(t *testing.T) {
	srv := mcpsrv.NewMCPServer("test", "1")
	registerConsolidatedTools(srv, &app.App{Infra: &app.Infra{}, Services: &app.Services{}})
	for name, scopes := range map[string][]string{"note_links": {"notes:read"}, "trash_action": {"notes:write", "files:write"}, "vault_manage": {"vaults:read", "notes:write"}} {
		tool := srv.GetTool(name).Tool
		require.Equal(t, name == "note_links", *tool.Annotations.ReadOnlyHint)
		require.Equal(t, name != "note_links", *tool.Annotations.DestructiveHint)
		require.Equal(t, scopes, tool.Meta.AdditionalFields["securitySchemes"].([]map[string]interface{})[0]["scopes"])
	}
}
