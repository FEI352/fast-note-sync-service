package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCorsMCPBrowserHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Cors([]string{"https://notes.example"}, ""))
	r.POST("/api/mcp", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodOptions, "/api/mcp", nil)
	req.Header.Set("Origin", "https://notes.example")
	req.Header.Set("Access-Control-Request-Headers", "authorization,mcp-session-id,mcp-protocol-version")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", w.Code)
	}
	for _, header := range []string{"mcp-session-id", "mcp-protocol-version"} {
		if !strings.Contains(strings.ToLower(w.Header().Get("Access-Control-Allow-Headers")), header) {
			t.Fatalf("preflight does not permit %s", header)
		}
		if !strings.Contains(strings.ToLower(w.Header().Get("Access-Control-Expose-Headers")), header) {
			t.Fatalf("response does not expose %s", header)
		}
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "https://notes.example" {
		t.Fatal("allowed origin changed")
	}
	req = httptest.NewRequest(http.MethodOptions, "/api/mcp", nil)
	req.Header.Set("Origin", "https://other.example")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unconfigured origin allowed")
	}
}
