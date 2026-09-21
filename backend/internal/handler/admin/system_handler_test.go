//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type systemHandlerUpdateServiceStub struct {
	currentVersion string
	warnings       []string
}

func (s *systemHandlerUpdateServiceStub) CurrentVersion() string {
	return s.currentVersion
}

func (s *systemHandlerUpdateServiceStub) SecurityWarnings() []string {
	return s.warnings
}

func newSystemHandlerTestRouter(t *testing.T, updateSvc *systemHandlerUpdateServiceStub, repo *memoryIdempotencyRepoStub) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() {
		service.SetDefaultIdempotencyCoordinator(nil)
	})

	lockSvc := service.NewSystemOperationLockService(repo, service.IdempotencyConfig{
		ProcessingTimeout:  time.Second,
		SystemOperationTTL: time.Minute,
	})
	handler := NewSystemHandler(updateSvc, lockSvc)

	router := gin.New()
	router.GET("/api/v1/admin/system/version", handler.GetVersion)
	router.POST("/api/v1/admin/system/restart", handler.RestartService)
	return router
}

func requireSystemLockStatus(t *testing.T, repo *memoryIdempotencyRepoStub, wantStatus string) {
	t.Helper()
	repo.mu.Lock()
	defer repo.mu.Unlock()

	for _, record := range repo.data {
		if record.Status == wantStatus {
			return
		}
	}
	t.Fatalf("system lock status %q not found in records: %#v", wantStatus, repo.data)
}

func TestSystemHandlerGetVersionReturnsVersionAndNoWarningsWhenSecure(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{currentVersion: "0.1.147"}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/version", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Code int `json:"code"`
		Data struct {
			Version          string   `json:"version"`
			SecurityWarnings []string `json:"security_warnings"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Equal(t, "0.1.147", body.Data.Version)
	require.Empty(t, body.Data.SecurityWarnings)
}

func TestSystemHandlerGetVersionSurfacesSecurityWarnings(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		currentVersion: "0.1.147",
		warnings:       []string{"security.url_allowlist.enabled is false: ..."},
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/version", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data struct {
			SecurityWarnings []string `json:"security_warnings"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data.SecurityWarnings, 1)
}

// RestartService is intentionally not exercised end-to-end here: on Linux,
// sysutil.RestartServiceAsync() calls os.Exit(0) ~600ms after responding
// (systemd then restarts the process via Restart=always) — invoking it from
// a test would kill the test binary itself.
