package mcp_router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// Exercise real tool definitions over HTTP: inspecting the in-memory tool list
// alone misses SDK serialization errors that otherwise produce HTTP 200 + no body.
func TestStreamableHTTPHandshakeThroughGin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := NewMCPServer(&app.App{Infra: &app.Infra{}, Services: &app.Services{}}, nil)
	// Also validate hidden compatibility aliases, which remain callable.
	for name, tool := range srv.ListTools() {
		_, err := json.Marshal(tool.Tool)
		require.NoError(t, err, name)
	}
	handler := &MCPHandler{streamableServer: server.NewStreamableHTTPServer(srv)}
	router := gin.New()
	router.Any("/api/mcp", handler.HandleStreamableHTTP)
	session := ""
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/api/mcp", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-03-26")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if bytes.Contains([]byte(body), []byte("notifications/initialized")) {
			require.Equal(t, http.StatusAccepted, rec.Code)
			require.Empty(t, rec.Body.String())
			continue
		}
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		var response struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		require.Equal(t, "2.0", response.JSONRPC)
		if response.ID == 1 {
			session = rec.Header().Get("Mcp-Session-Id")
			require.NotEmpty(t, session)
			continue
		}
		require.Equal(t, 2, response.ID)
		var result mcp.ListToolsResult
		require.NoError(t, json.Unmarshal(response.Result, &result))
		require.Len(t, result.Tools, 14)
		names := map[string]bool{}
		for _, tool := range result.Tools {
			require.True(t, canonicalMCPTools[tool.Name], tool.Name)
			names[tool.Name] = true
			var schema map[string]any
			raw, err := json.Marshal(tool)
			require.NoError(t, err)
			var encoded map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &encoded))
			require.NoError(t, json.Unmarshal(encoded["inputSchema"], &schema))
			require.Equal(t, "object", schema["type"], tool.Name)
			if tool.Name == "note_search" || tool.Name == "file_search" || tool.Name == "vault_search" || tool.Name == "note_history_search" {
				require.Contains(t, schema["properties"], "query", tool.Name)
				require.Equal(t, false, schema["additionalProperties"], tool.Name)
			}
		}
		for name := range canonicalMCPTools {
			require.True(t, names[name], name)
		}
	}
}
