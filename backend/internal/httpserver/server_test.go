package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"netassets/internal/auth"
	"netassets/internal/config"
)

func TestLiveHealth(t *testing.T) {
	handler := New(config.Config{Environment: "test", StaticDir: t.TempDir()}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestReadinessWithoutDatabase(t *testing.T) {
	handler := New(config.Config{Environment: "test", StaticDir: t.TempDir()}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestSystemInfo(t *testing.T) {
	handler := New(config.Config{Environment: "test", StaticDir: t.TempDir()}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["name"] != "NetAssets" {
		t.Fatalf("name = %v, want NetAssets", body["name"])
	}
}

func TestStaticFrontendKeepsAssetsBusinessRouteAvailable(t *testing.T) {
	staticDir := t.TempDir()
	indexBody := "<!doctype html><html><body>netassets-spa-shell</body></html>"
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte(indexBody), 0o600); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	assetDir := filepath.Join(staticDir, "_assets")
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		t.Fatalf("create asset directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "app.js"), []byte("window.netassetsLoaded = true"), 0o600); err != nil {
		t.Fatalf("write static asset: %v", err)
	}

	handler := New(config.Config{Environment: "test", StaticDir: staticDir}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, path := range []string{"/assets", "/assets/", "/systems", "/unknown-client-route"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; location = %q", response.Code, http.StatusOK, response.Header().Get("Location"))
			}
			if location := response.Header().Get("Location"); location != "" {
				t.Fatalf("unexpected redirect to %q", location)
			}
			if !strings.Contains(response.Body.String(), "netassets-spa-shell") {
				t.Fatalf("response does not contain SPA shell: %q", response.Body.String())
			}
		})
	}

	request := httptest.NewRequest(http.MethodGet, "/_assets/app.js", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("static asset status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "netassetsLoaded") {
		t.Fatalf("unexpected static asset response: %q", response.Body.String())
	}
}

func TestAllowsPasswordChangeRequest(t *testing.T) {
	tests := []struct {
		method  string
		path    string
		allowed bool
	}{
		{http.MethodGet, "/api/v1/auth/me", true},
		{http.MethodGet, "/api/v1/auth/profile", true},
		{http.MethodPut, "/api/v1/auth/profile/password", true},
		{http.MethodPost, "/api/v1/auth/logout", true},
		{http.MethodPut, "/api/v1/auth/profile", false},
		{http.MethodGet, "/api/v1/auth/sessions", false},
		{http.MethodDelete, "/api/v1/auth/sessions/others", false},
		{http.MethodGet, "/api/v1/dashboard", false},
		{http.MethodGet, "/api/v1/users", false},
	}
	for _, test := range tests {
		if actual := allowsPasswordChangeRequest(test.method, test.path); actual != test.allowed {
			t.Errorf("allowsPasswordChangeRequest(%q, %q) = %v, want %v", test.method, test.path, actual, test.allowed)
		}
	}
}

func TestSessionAuthenticationMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		authType   string
		wantStatus int
	}{
		{name: "browser session", authType: auth.AuthenticationSession, wantStatus: http.StatusNoContent},
		{name: "api token", authType: auth.AuthenticationAPIToken, wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/", func(c *gin.Context) {
				c.Set(principalContextKey, auth.Principal{UserID: "user", AuthenticationType: test.authType})
				c.Next()
			}, sessionAuthenticationMiddleware(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
