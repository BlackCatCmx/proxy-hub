package store

import (
	"testing"
	"time"

	"proxy-hub/internal/proxy"
)

func TestUpdateGroupsAndClearResults(t *testing.T) {
	s, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONStore() error = %v", err)
	}
	group := proxy.Group{
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
	if err := s.SaveGroups([]proxy.Group{group}); err != nil {
		t.Fatalf("SaveGroups() error = %v", err)
	}
	if err := s.MergeResults(map[string]proxy.ProxyResult{
		"proxy-1": {
			ProxyID: "proxy-1",
			Latency: &proxy.LatencyResult{
				OK:        true,
				LatencyMs: 10,
				TestURL:   "https://cp.cloudflare.com/generate_204",
				TestedAt:  time.Now(),
			},
		},
	}); err != nil {
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
