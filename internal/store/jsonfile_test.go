package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"proxy-hub/internal/proxy"
)

func TestUpdateGroupsAndClearResults(t *testing.T) {
	s, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONStore() error = %v", err)
	}
	if err := s.SaveGroups([]proxy.Group{testGroup()}); err != nil {
		t.Fatalf("SaveGroups() error = %v", err)
	}
	if err := s.MergeResults(testResults("proxy-1")); err != nil {
		t.Fatalf("MergeResults() error = %v", err)
	}
	if err := s.UpdateGroupsAndClearResults(func(groups []proxy.Group) ([]proxy.Group, []string, error) {
		groups[0].Proxies[0].Raw = "socks5://127.0.0.1:1081"
		groups[0].Proxies[0].Port = 1081
		return groups, []string{"proxy-1"}, nil
	}); err != nil {
		t.Fatalf("UpdateGroupsAndClearResults() error = %v", err)
	}
	results, err := s.Results()
	if err != nil {
		t.Fatalf("Results() error = %v", err)
	}
	if _, ok := results["proxy-1"]; ok {
		t.Fatal("result for proxy-1 was not cleared")
	}
	groups, err := s.Groups()
	if err != nil {
		t.Fatalf("Groups() error = %v", err)
	}
	if groups[0].Proxies[0].ID != "proxy-1" || groups[0].Proxies[0].Port != 1081 {
		t.Fatalf("proxy was not updated with ID preserved: %#v", groups[0].Proxies[0])
	}
}

func TestSaveGroupsPreservesEmptyProxyList(t *testing.T) {
	dataDir := t.TempDir()
	s, err := NewJSONStore(dataDir)
	if err != nil {
		t.Fatalf("NewJSONStore() error = %v", err)
	}
	group := proxy.Group{
		ID:      "empty-group",
		Name:    "empty",
		Created: time.Now(),
		Proxies: []proxy.Proxy{},
	}
	if err := s.SaveGroups([]proxy.Group{group}); err != nil {
		t.Fatalf("SaveGroups() error = %v", err)
	}

	groups, err := s.Groups()
	if err != nil {
		t.Fatalf("Groups() error = %v", err)
	}
	if groups[0].Proxies == nil {
		t.Fatal("Groups() returned nil proxies for an empty group")
	}

	var persisted []proxy.Group
	if err := readJSON(filepath.Join(dataDir, "groups.json"), &persisted); err != nil {
		t.Fatalf("readJSON(groups.json) error = %v", err)
	}
	if persisted[0].Proxies == nil {
		t.Fatal("groups.json stored null instead of an empty proxy list")
	}
}

func TestUpdateGroupsAndClearResultsKeepsResultsWithoutClearIDs(t *testing.T) {
	s, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONStore() error = %v", err)
	}
	if err := s.SaveGroups([]proxy.Group{testGroup()}); err != nil {
		t.Fatalf("SaveGroups() error = %v", err)
	}
	if err := s.MergeResults(testResults("proxy-1")); err != nil {
		t.Fatalf("MergeResults() error = %v", err)
	}
	if err := s.UpdateGroupsAndClearResults(func(groups []proxy.Group) ([]proxy.Group, []string, error) {
		groups[0].Proxies[0].Label = "new label"
		return groups, nil, nil
	}); err != nil {
		t.Fatalf("UpdateGroupsAndClearResults() error = %v", err)
	}
	results, err := s.Results()
	if err != nil {
		t.Fatalf("Results() error = %v", err)
	}
	if _, ok := results["proxy-1"]; !ok {
		t.Fatal("result for proxy-1 was unexpectedly cleared")
	}
}

func TestUpdateGroupsAndClearResultsIgnoresMissingClearIDs(t *testing.T) {
	s, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONStore() error = %v", err)
	}
	if err := s.SaveGroups([]proxy.Group{testGroup()}); err != nil {
		t.Fatalf("SaveGroups() error = %v", err)
	}
	if err := s.MergeResults(testResults("proxy-1")); err != nil {
		t.Fatalf("MergeResults() error = %v", err)
	}
	if err := s.UpdateGroupsAndClearResults(func(groups []proxy.Group) ([]proxy.Group, []string, error) {
		return groups, []string{"missing-proxy"}, nil
	}); err != nil {
		t.Fatalf("UpdateGroupsAndClearResults() error = %v", err)
	}
	results, err := s.Results()
	if err != nil {
		t.Fatalf("Results() error = %v", err)
	}
	if _, ok := results["proxy-1"]; !ok {
		t.Fatal("result for proxy-1 was incorrectly deleted")
	}
}

func TestUpdateGroupsAndClearResultsRollbackOnError(t *testing.T) {
	s, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONStore() error = %v", err)
	}
	if err := s.SaveGroups([]proxy.Group{testGroup()}); err != nil {
		t.Fatalf("SaveGroups() error = %v", err)
	}
	if err := s.MergeResults(testResults("proxy-1")); err != nil {
		t.Fatalf("MergeResults() error = %v", err)
	}
	wantErr := errors.New("stop")
	err = s.UpdateGroupsAndClearResults(func(groups []proxy.Group) ([]proxy.Group, []string, error) {
		groups[0].Proxies[0].Port = 1081
		return groups, []string{"proxy-1"}, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("UpdateGroupsAndClearResults() error = %v, want %v", err, wantErr)
	}
	groups, err := s.Groups()
	if err != nil {
		t.Fatalf("Groups() error = %v", err)
	}
	if groups[0].Proxies[0].Port != 1080 {
		t.Fatalf("proxy port changed despite rollback: %d", groups[0].Proxies[0].Port)
	}
	results, err := s.Results()
	if err != nil {
		t.Fatalf("Results() error = %v", err)
	}
	if _, ok := results["proxy-1"]; !ok {
		t.Fatal("result was cleared despite rollback")
	}
}

func TestNewJSONStorePrunesOrphanResults(t *testing.T) {
	dataDir := t.TempDir()
	s, err := NewJSONStore(dataDir)
	if err != nil {
		t.Fatalf("NewJSONStore() error = %v", err)
	}
	if err := s.SaveGroups([]proxy.Group{testGroup()}); err != nil {
		t.Fatalf("SaveGroups() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "results.json"), []byte(`{
  "proxy-1": {"proxy_id":"proxy-1"},
  "orphan": {"proxy_id":"orphan"}
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(results.json) error = %v", err)
	}
	reopened, err := NewJSONStore(dataDir)
	if err != nil {
		t.Fatalf("NewJSONStore(reopen) error = %v", err)
	}
	results, err := reopened.Results()
	if err != nil {
		t.Fatalf("Results() error = %v", err)
	}
	if _, ok := results["orphan"]; ok {
		t.Fatal("orphan result was not pruned")
	}
	if _, ok := results["proxy-1"]; !ok {
		t.Fatal("valid result was pruned")
	}
}

func TestNewJSONStoreRestoresSchemeFromRaw(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "groups.json"), []byte(`[
  {
    "id": "group-1",
    "name": "group",
    "created": "2026-06-30T00:00:00Z",
    "proxies": [
      {
        "id": "proxy-1",
        "scheme": "socks5",
        "host": "example.com",
        "port": 1080,
        "raw": "socks5h://example.com:1080"
      }
    ]
  }
]
`), 0o644); err != nil {
		t.Fatalf("WriteFile(groups.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "results.json"), []byte(`{
  "proxy-1": {"proxy_id":"proxy-1"}
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(results.json) error = %v", err)
	}

	s, err := NewJSONStore(dataDir)
	if err != nil {
		t.Fatalf("NewJSONStore() error = %v", err)
	}
	groups, err := s.Groups()
	if err != nil {
		t.Fatalf("Groups() error = %v", err)
	}
	if got := groups[0].Proxies[0].Scheme; got != "socks5h" {
		t.Fatalf("loaded scheme = %q, want socks5h", got)
	}
	results, err := s.Results()
	if err != nil {
		t.Fatalf("Results() error = %v", err)
	}
	if _, ok := results["proxy-1"]; ok {
		t.Fatal("result for normalized proxy was not cleared")
	}
}

func testGroup() proxy.Group {
	return proxy.Group{
		ID:      "group-1",
		Name:    "group",
		Created: time.Now(),
		Proxies: []proxy.Proxy{{
			ID:     "proxy-1",
			Scheme: "socks5",
			Host:   "127.0.0.1",
			Port:   1080,
			Raw:    "socks5://127.0.0.1:1080",
		}},
	}
}

func testResults(id string) map[string]proxy.ProxyResult {
	return map[string]proxy.ProxyResult{
		id: {
			ProxyID: id,
			Latency: &proxy.LatencyResult{
				OK:        true,
				LatencyMs: 10,
				TestURL:   "https://cp.cloudflare.com/generate_204",
				TestedAt:  time.Now(),
			},
		},
	}
}
