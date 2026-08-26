package httpapi

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	engine, err := service.NewEngine(store.NewMemoryStore())
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	return NewServer(engine)
}

func TestNewErrorSortsAndDeduplicatesReasons(t *testing.T) {
	e := NewError("code", "msg", "b", "a", "b", "c")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(e.Reasons, want) {
		t.Fatalf("got %v want %v", e.Reasons, want)
	}
}

func TestHealthEndpointListsComponents(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Body.String() == "" {
		t.Fatal("empty body")
	}
}

func TestFrontendServesEmbeddedPage(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatal("missing content type")
	}
}
