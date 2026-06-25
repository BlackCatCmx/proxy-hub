package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"proxy-hub/internal/auth"
	"proxy-hub/internal/logbuf"
	"proxy-hub/internal/prober"
	"proxy-hub/internal/proxy"
	"proxy-hub/internal/store"
	"proxy-hub/internal/tester"
	"proxy-hub/web"
)

func TestApplyProxyOrder(t *testing.T) {
	existing := []proxy.Proxy{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	tests := []struct {
		name    string
		order   []string
		want    []string
		wantErr bool
	}{
		{name: "reverse", order: []string{"c", "b", "a"}, want: []string{"c", "b", "a"}},
		{name: "identity", order: []string{"a", "b", "c"}, want: []string{"a", "b", "c"}},
		{name: "short", order: []string{"a", "b"}, wantErr: true},
		{name: "long", order: []string{"a", "b", "c", "d"}, wantErr: true},
		{name: "unknown id", order: []string{"a", "b", "x"}, wantErr: true},
		{name: "duplicate", order: []string{"a", "b", "b"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyProxyOrder(existing, tt.order)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("applyProxyOrder() expected error, got %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("applyProxyOrder() error = %v", err)
			}
			gotIDs := make([]string, len(got))
			for i, p := range got {
				gotIDs[i] = p.ID
			}
			if len(gotIDs) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(gotIDs), len(tt.want))
			}
			for i := range tt.want {
				if gotIDs[i] != tt.want[i] {
					t.Fatalf("order = %v, want %v", gotIDs, tt.want)
				}
			}
		})
	}
}

// PLACEHOLDER_ROUTE_TEST

const testAdminKey = "test-admin-key"

func newTestServer(t *testing.T) (*Server, store.Store) {
	t.Helper()
	dir := t.TempDir()
	jsonStore, err := store.NewJSONStore(dir)
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}
	authService, err := auth.NewService(testAdminKey, dir, false)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	logger := logbuf.New(10)
	proxyTester := tester.New(prober.NewDefaultRegistry())
	jobs := tester.NewManager(jsonStore, proxyTester, logger)
	srv := NewServer(jsonStore, authService, auth.NewLoginLimiter(), proxyTester, jobs, logger, web.FS, dir, false)
	return srv, jsonStore
}

// authedRequest builds a request carrying a valid session cookie.
func authedRequest(t *testing.T, srv *Server, method, target string, body any) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	if err := srv.auth.SetLoginCookie(rec, req); err != nil {
		t.Fatalf("SetLoginCookie: %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

func seedGroup(t *testing.T, st store.Store, raws ...string) proxy.Group {
	t.Helper()
	group := proxy.Group{ID: "g1", Name: "g1"}
	for i, raw := range raws {
		p, err := proxy.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		p.ID = string(rune('a' + i))
		group.Proxies = append(group.Proxies, p)
	}
	if err := st.SaveGroups([]proxy.Group{group}); err != nil {
		t.Fatalf("SaveGroups: %v", err)
	}
	return group
}

func proxyIDs(group proxy.Group) []string {
	ids := make([]string, len(group.Proxies))
	for i, p := range group.Proxies {
		ids[i] = p.ID
	}
	return ids
}

// PLACEHOLDER_ROUTE_CASES

func TestReorderProxiesRoute(t *testing.T) {
	srv, st := newTestServer(t)
	seedGroup(t, st, "1.0.0.1:1080", "1.0.0.2:1080", "1.0.0.3:1080")
	handler := srv.Handler()

	req := authedRequest(t, srv, http.MethodPost, "/api/groups/g1/proxies/reorder", map[string]any{
		"order": []string{"c", "a", "b"},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated proxy.Group
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := proxyIDs(updated); !equalStrings(got, []string{"c", "a", "b"}) {
		t.Fatalf("response order = %v, want [c a b]", got)
	}
	groups, err := st.Groups()
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if got := proxyIDs(groups[0]); !equalStrings(got, []string{"c", "a", "b"}) {
		t.Fatalf("persisted order = %v, want [c a b]", got)
	}
}

func TestReorderProxiesNotPermutation(t *testing.T) {
	srv, st := newTestServer(t)
	seedGroup(t, st, "1.0.0.1:1080", "1.0.0.2:1080")
	handler := srv.Handler()

	req := authedRequest(t, srv, http.MethodPost, "/api/groups/g1/proxies/reorder", map[string]any{
		"order": []string{"a", "x"},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestReorderProxiesGroupNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	handler := srv.Handler()

	req := authedRequest(t, srv, http.MethodPost, "/api/groups/missing/proxies/reorder", map[string]any{
		"order": []string{},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}


