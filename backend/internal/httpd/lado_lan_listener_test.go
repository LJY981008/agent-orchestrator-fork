package httpd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// TestLANManagerBlocksLadoRoutes keeps LADO's routes on the loopback listener
// only (docs/LJY-PRD/TDD.md §13), even for an authenticated LAN client that
// spoofs a loopback Host.
func TestLANManagerBlocksLadoRoutes(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default(), nil)
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	for _, path := range []string{"/api/v1/lado", "/api/v1/lado/projects/mer/settings"} {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
			req.Host = "127.0.0.1"
			req.Header.Set("Authorization", "Bearer secret12")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("LAN %s %s = %d, want 404", method, path, resp.StatusCode)
			}
		}
	}
}
