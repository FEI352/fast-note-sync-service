package mcp_router

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

// registerConsolidatedTools must run after the legacy tools have been registered.
// Capturing their handlers preserves authorization, vault restrictions and sync events.
func registerConsolidatedTools(srv *mcpsrv.MCPServer, appContainer *app.App) {
	cfg := appContainer.Config()
	handlers := make(map[string]mcpsrv.ToolHandlerFunc)
	for _, name := range []string{"note_get_backlinks", "note_get_outlinks", "note_delete", "note_restore", "note_recycle_clear", "note_rename", "file_delete", "file_restore", "file_recycle_clear", "file_rename", "folder_delete", "vault_list", "vault_get", "vault_create_or_update", "vault_delete"} {
		if tool := srv.GetTool(name); tool != nil {
			handlers[name] = tool.Handler
		}
	}
	invoke := func(ctx context.Context, req mcp.CallToolRequest, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
		handler := handlers[name]
		if handler == nil {
			return mcp.NewToolResultError("tool unavailable: " + name), nil
		}
		req.Params.Name, req.Params.Arguments = name, args
		return handler(ctx, req)
	}

	links := mcp.NewTool("note_links",
		mcp.WithDescription("Get incoming backlinks and outgoing links for a note. By default returns both directions."),
		mcp.WithString("vault", mcp.Description("Vault name; omitted or 'default' uses the client default vault.")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Note path.")),
		mcp.WithString("direction", mcp.Enum("in", "out", "both", "backlinks", "outlinks"), mcp.Description("Link direction, default both. in/backlinks selects incoming; out/outlinks selects outgoing.")),
		mcp.WithOutputSchema[mcpCombinedNoteLinksOutput](),
	)
	srv.AddTool(readOnlyMCPTool(links, cfg, "notes:read"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := getArgs(req)
		if err := consolidatedRequiredStrings(args, "path"); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		direction := "both"
		if value, exists := args["direction"]; exists {
			var ok bool
			direction, ok = value.(string)
			if !ok {
				return mcp.NewToolResultError("direction must be a string"), nil
			}
		}
		output := mcpCombinedNoteLinksOutput{}
		var targets []string
		switch direction {
		case "both":
			targets = []string{"note_get_backlinks", "note_get_outlinks"}
		case "in", "backlinks":
			targets = []string{"note_get_backlinks"}
		case "out", "outlinks":
			targets = []string{"note_get_outlinks"}
		default:
			return mcp.NewToolResultError("direction must be in, out, both, backlinks or outlinks"), nil
		}
		for _, target := range targets {
			result, err := invoke(ctx, req, target, args)
			if err != nil || result == nil || result.IsError {
				return result, err
			}
			if target == "note_get_backlinks" {
				output.Backlinks = result.StructuredContent
			} else {
				output.Outlinks = result.StructuredContent
			}
		}
		return mcp.NewToolResultStructured(output, "Note links retrieved."), nil
	})

	trash := mcp.NewTool("trash_action",
		mcp.WithDescription("Delete, restore, permanently purge or rename a note/file. Folder supports recursive delete only; restore, purge and rename are unsupported."),
		mcp.WithString("type", mcp.Required(), mcp.Enum("note", "file", "folder"), mcp.Description("Item type.")),
		mcp.WithString("action", mcp.Required(), mcp.Enum("delete", "restore", "purge", "rename"), mcp.Description("Operation. purge permanently deletes one recycle-bin item.")),
		mcp.WithString("vault", mcp.Description("Vault name; omitted or 'default' uses the client default vault.")),
		mcp.WithString("path", mcp.Description("Required for delete, restore and purge. Folder delete uses an exact non-root folder path.")),
		mcp.WithString("oldPath", mcp.Description("Required for rename: existing path.")),
		mcp.WithString("newPath", mcp.Description("Required for rename: destination path.")),
	)
	srv.AddTool(writeMCPTool(trash, cfg, true, "notes:write", "files:write"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := getArgs(req)
		target, err := consolidatedTrashTarget(args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return invoke(ctx, req, target, args)
	})

	vault := mcp.NewTool("vault_manage",
		mcp.WithDescription("List, get, create, rename or delete vaults. Restricted tokens cannot create vaults; access to existing vaults follows the token allowlist."),
		mcp.WithString("action", mcp.Required(), mcp.Enum("list", "get", "create", "rename", "delete"), mcp.Description("Vault operation.")),
		mcp.WithNumber("id", mcp.Description("Positive integer vault ID required for get, rename and delete. Omit or use 0 for create.")),
		mcp.WithString("name", mcp.Description("Vault name required for create and rename.")),
		mcp.WithString("vault", mcp.Description("Alias for name; if both supplied they must match.")),
	)
	srv.AddTool(writeMCPTool(vault, cfg, true, "vaults:read", "notes:write"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		target, args, err := consolidatedVaultTarget(getArgs(req))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return invoke(ctx, req, target, args)
	})
}

type mcpCombinedNoteLinksOutput struct {
	Backlinks any `json:"backlinks,omitempty"`
	Outlinks  any `json:"outlinks,omitempty"`
}

func consolidatedRequiredStrings(args map[string]interface{}, keys ...string) error {
	for _, key := range keys {
		value, ok := args[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must be a non-empty string", key)
		}
	}
	return nil
}

func consolidatedTrashTarget(args map[string]interface{}) (string, error) {
	if err := consolidatedRequiredStrings(args, "type", "action"); err != nil {
		return "", err
	}
	kind, action := args["type"].(string), args["action"].(string)
	if kind != "note" && kind != "file" && kind != "folder" {
		return "", fmt.Errorf("type must be note, file or folder")
	}
	if action != "delete" && action != "restore" && action != "purge" && action != "rename" {
		return "", fmt.Errorf("action must be delete, restore, purge or rename")
	}
	if kind == "folder" && action != "delete" {
		return "", fmt.Errorf("folder action %s is unsupported; only delete is supported", action)
	}
	if action == "rename" {
		if err := consolidatedRequiredStrings(args, "oldPath", "newPath"); err != nil {
			return "", err
		}
	} else {
		if err := consolidatedRequiredStrings(args, "path"); err != nil {
			return "", err
		}
		if kind == "folder" && strings.Trim(args["path"].(string), "/") == "" {
			return "", fmt.Errorf("root folder cannot be deleted")
		}
	}
	if action == "purge" {
		action = "recycle_clear"
	}
	return kind + "_" + action, nil
}

func consolidatedVaultTarget(input map[string]interface{}) (string, map[string]interface{}, error) {
	args := make(map[string]interface{}, len(input)+1)
	for key, value := range input {
		args[key] = value
	}
	if err := consolidatedRequiredStrings(args, "action"); err != nil {
		return "", nil, err
	}
	action := args["action"].(string)
	switch action {
	case "list":
		return "vault_list", args, nil
	case "get", "rename", "delete":
		if !consolidatedValidVaultID(args["id"], false) {
			return "", nil, fmt.Errorf("id must be a positive integer for %s", action)
		}
	case "create":
		if id, exists := args["id"]; exists && !consolidatedValidVaultID(id, true) {
			return "", nil, fmt.Errorf("id must be omitted or 0 for create")
		}
	default:
		return "", nil, fmt.Errorf("action must be list, get, create, rename or delete")
	}
	if action == "create" || action == "rename" {
		if name, exists := args["name"]; exists {
			if err := consolidatedRequiredStrings(args, "name"); err != nil {
				return "", nil, err
			}
			if _, exists := args["vault"]; exists {
				if err := consolidatedRequiredStrings(args, "vault"); err != nil {
					return "", nil, err
				}
			}
			if alias, exists := args["vault"]; exists && alias.(string) != name.(string) {
				return "", nil, fmt.Errorf("name and vault must match")
			}
			args["vault"] = name
		}
		if err := consolidatedRequiredStrings(args, "vault"); err != nil {
			return "", nil, fmt.Errorf("name must be a non-empty string for %s", action)
		}
		return "vault_create_or_update", args, nil
	}
	return "vault_" + action, args, nil
}

func consolidatedValidVaultID(value interface{}, zeroOnly bool) bool {
	var id int64
	switch number := value.(type) {
	case int:
		id = int64(number)
	case int64:
		id = number
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number < 0 || number >= 9223372036854775808.0 {
			return false
		}
		id = int64(number)
	default:
		return false
	}
	if zeroOnly {
		return id == 0
	}
	return id > 0
}
