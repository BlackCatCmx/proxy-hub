package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"proxy-hub/internal/config"
	"proxy-hub/internal/proxy"
)

type JSONStore struct {
	mu       sync.RWMutex
	dataDir  string
	groups   []proxy.Group
	results  map[string]proxy.ProxyResult
	settings config.Settings
}

func NewJSONStore(dataDir string) (*JSONStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	s := &JSONStore{
		dataDir: dataDir,
		results: make(map[string]proxy.ProxyResult),
	}
	normalizedProxyIDs, err := s.loadGroups()
	if err != nil {
		return nil, err
	}
	if err := s.loadResults(); err != nil {
		return nil, err
	}
	if err := s.clearLoadedResults(normalizedProxyIDs); err != nil {
		return nil, err
	}
	if err := s.loadSettings(); err != nil {
		return nil, err
	}
	if err := s.pruneLoadedResults(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *JSONStore) Groups() ([]proxy.Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneGroups(s.groups), nil
}

func (s *JSONStore) UpdateGroups(update func([]proxy.Group) ([]proxy.Group, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	nextGroups, err := update(cloneGroups(s.groups))
	if err != nil {
		return err
	}
	return s.saveGroupsLocked(nextGroups)
}

func (s *JSONStore) UpdateGroupsAndClearResults(update func([]proxy.Group) ([]proxy.Group, []string, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	nextGroups, clearIDs, err := update(cloneGroups(s.groups))
	if err != nil {
		return err
	}
	nextResults := cloneResults(s.results)
	for _, id := range clearIDs {
		delete(nextResults, id)
	}
	nextResults = keepExistingResults(nextResults, nextGroups)
	return s.saveGroupsAndResultsLocked(nextGroups, nextResults)
}

func (s *JSONStore) SaveGroups(groups []proxy.Group) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveGroupsLocked(groups)
}

func (s *JSONStore) saveGroupsLocked(groups []proxy.Group) error {
	nextGroups := cloneGroups(groups)
	nextResults := keepExistingResults(s.results, nextGroups)
	return s.saveGroupsAndResultsLocked(nextGroups, nextResults)
}

func (s *JSONStore) saveGroupsAndResultsLocked(groups []proxy.Group, results map[string]proxy.ProxyResult) error {
	nextGroups := cloneGroups(groups)
	nextResults := cloneResults(results)
	if !sameResultKeys(nextResults, s.results) {
		// Results are written first so a mid-write failure does not leave stale results for edited proxies.
		if err := writeJSON(s.path("results.json"), nextResults); err != nil {
			return err
		}
	}
	if err := writeJSON(s.path("groups.json"), nextGroups); err != nil {
		return err
	}
	s.groups = nextGroups
	s.results = nextResults
	return nil
}

func (s *JSONStore) Results() (map[string]proxy.ProxyResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneResults(s.results), nil
}

func (s *JSONStore) MergeResults(updates map[string]proxy.ProxyResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneResults(s.results)
	for id, update := range updates {
		current := next[id]
		current.ProxyID = id
		if update.Latency != nil {
			current.Latency = update.Latency
		}
		if update.Echo != nil {
			current.Echo = update.Echo
		}
		next[id] = current
	}
	next = keepExistingResults(next, s.groups)
	if err := writeJSON(s.path("results.json"), next); err != nil {
		return err
	}
	s.results = next
	return nil
}

func (s *JSONStore) ClearResults(proxyIDs ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneResults(s.results)
	changed := false
	for _, id := range proxyIDs {
		if _, ok := next[id]; ok {
			delete(next, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := writeJSON(s.path("results.json"), next); err != nil {
		return err
	}
	s.results = next
	return nil
}

func (s *JSONStore) Settings() (config.Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings, nil
}

func (s *JSONStore) SaveSettings(settings config.Settings) error {
	settings.FillDefaults()
	if err := settings.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSON(s.path("settings.json"), settings); err != nil {
		return err
	}
	s.settings = settings
	return nil
}

func (s *JSONStore) loadGroups() ([]string, error) {
	path := s.path("groups.json")
	if !exists(path) {
		s.groups = []proxy.Group{}
		return nil, writeJSON(path, s.groups)
	}
	if err := readJSON(path, &s.groups); err != nil {
		return nil, err
	}
	groups, changedIDs := normalizeLoadedGroups(s.groups)
	s.groups = groups
	if len(changedIDs) > 0 {
		return changedIDs, writeJSON(path, s.groups)
	}
	return nil, nil
}

func (s *JSONStore) loadResults() error {
	path := s.path("results.json")
	if !exists(path) {
		s.results = map[string]proxy.ProxyResult{}
		return writeJSON(path, s.results)
	}
	return readJSON(path, &s.results)
}

func (s *JSONStore) clearLoadedResults(proxyIDs []string) error {
	if len(proxyIDs) == 0 {
		return nil
	}
	changed := false
	for _, id := range proxyIDs {
		if _, ok := s.results[id]; ok {
			delete(s.results, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeJSON(s.path("results.json"), s.results)
}

func (s *JSONStore) loadSettings() error {
	path := s.path("settings.json")
	if !exists(path) {
		s.settings = config.DefaultSettings()
		return writeJSON(path, s.settings)
	}
	if err := readJSON(path, &s.settings); err != nil {
		return err
	}
	s.settings.FillDefaults()
	return s.settings.Validate()
}

func (s *JSONStore) pruneLoadedResults() error {
	pruned := keepExistingResults(s.results, s.groups)
	if sameResultKeys(pruned, s.results) {
		return nil
	}
	// Startup pruning rewrites results.json when it contains proxy IDs no longer present in groups.json.
	if err := writeJSON(s.path("results.json"), pruned); err != nil {
		return err
	}
	s.results = pruned
	return nil
}

func (s *JSONStore) path(name string) string {
	return filepath.Join(s.dataDir, name)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteJSON(path, data)
}

func atomicWriteJSON(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func keepExistingResults(results map[string]proxy.ProxyResult, groups []proxy.Group) map[string]proxy.ProxyResult {
	ids := make(map[string]struct{})
	for _, group := range groups {
		for _, p := range group.Proxies {
			ids[p.ID] = struct{}{}
		}
	}
	next := make(map[string]proxy.ProxyResult)
	for id, result := range results {
		if _, ok := ids[id]; ok {
			next[id] = result
		}
	}
	return next
}

func cloneGroups(groups []proxy.Group) []proxy.Group {
	out := make([]proxy.Group, len(groups))
	for i, group := range groups {
		out[i] = group
		out[i].Proxies = append([]proxy.Proxy(nil), group.Proxies...)
	}
	return out
}

func normalizeLoadedGroups(groups []proxy.Group) ([]proxy.Group, []string) {
	next := cloneGroups(groups)
	var changedIDs []string
	for gi := range next {
		for pi := range next[gi].Proxies {
			p := &next[gi].Proxies[pi]
			scheme := proxy.CanonicalScheme(p.Scheme)
			if parsed, err := proxy.Parse(p.Raw); err == nil && sameProxyEndpoint(*p, parsed) {
				scheme = parsed.Scheme
			}
			if p.Scheme != scheme {
				p.Scheme = scheme
				changedIDs = append(changedIDs, p.ID)
			}
		}
	}
	return next, changedIDs
}

func sameProxyEndpoint(a, b proxy.Proxy) bool {
	return a.Host == b.Host && a.Port == b.Port && a.User == b.User && a.Pass == b.Pass
}

func cloneResults(results map[string]proxy.ProxyResult) map[string]proxy.ProxyResult {
	out := make(map[string]proxy.ProxyResult, len(results))
	for id, result := range results {
		out[id] = result
	}
	return out
}

func sameResultKeys(a, b map[string]proxy.ProxyResult) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if _, ok := b[id]; !ok {
			return false
		}
	}
	return true
}
