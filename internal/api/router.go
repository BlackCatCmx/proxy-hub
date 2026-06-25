package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"proxy-hub/internal/auth"
	"proxy-hub/internal/config"
	"proxy-hub/internal/idgen"
	"proxy-hub/internal/logbuf"
	"proxy-hub/internal/proxy"
	"proxy-hub/internal/store"
	"proxy-hub/internal/tester"
	"proxy-hub/internal/version"
)

const (
	maxJSONBodyBytes = 1 << 20
	maxBulkBodyBytes = 5 << 20
)

var (
	errGroupNotFound   = errors.New("group not found")
	errProxyNotFound   = errors.New("proxy not found")
	errDuplicateProxy  = errors.New("proxy already exists in this group")
	errReorderMismatch = errors.New("order must be a permutation of existing proxy ids")
)

type Server struct {
	store             store.Store
	auth              *auth.Service
	limiter           *auth.LoginLimiter
	tester            *tester.Tester
	jobs              *tester.Manager
	logger            *logbuf.Logger
	static            fs.FS
	dataDir           string
	trustProxyHeaders bool
}

func NewServer(store store.Store, authService *auth.Service, limiter *auth.LoginLimiter, singleTester *tester.Tester, jobs *tester.Manager, logger *logbuf.Logger, static fs.FS, dataDir string, trustProxyHeaders bool) *Server {
	return &Server{
		store:             store,
		auth:              authService,
		limiter:           limiter,
		tester:            singleTester,
		jobs:              jobs,
		logger:            logger,
		static:            static,
		dataDir:           dataDir,
		trustProxyHeaders: trustProxyHeaders,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.Handle("GET /api/me", s.protected(s.me))

	mux.Handle("GET /api/groups", s.protected(s.listGroups))
	mux.Handle("POST /api/groups", s.protected(s.createGroup))
	mux.Handle("PATCH /api/groups/{id}", s.protected(s.renameGroup))
	mux.Handle("DELETE /api/groups/{id}", s.protected(s.deleteGroup))
	mux.Handle("POST /api/groups/{id}/proxies", s.protected(s.addProxy))
	mux.Handle("POST /api/groups/{id}/proxies/bulk", s.protected(s.bulkProxies))
	mux.Handle("POST /api/groups/{id}/proxies/reorder", s.protected(s.reorderProxies))
	mux.Handle("PATCH /api/groups/{id}/proxies/{pid}", s.protected(s.updateProxy))
	mux.Handle("DELETE /api/groups/{id}/proxies/{pid}", s.protected(s.deleteProxy))
	mux.Handle("GET /api/groups/{id}/export", s.protected(s.exportGroup))

	mux.Handle("POST /api/test/jobs", s.protected(s.createJob))
	mux.Handle("GET /api/test/jobs/{id}/stream", s.protected(s.streamJob))
	mux.Handle("POST /api/test/one", s.protected(s.testOne))
	mux.Handle("GET /api/results", s.protected(s.results))

	mux.Handle("GET /api/settings", s.protected(s.getSettings))
	mux.Handle("PUT /api/settings", s.protected(s.saveSettings))
	mux.Handle("GET /api/logs", s.protected(s.logs))
	mux.Handle("GET /api/logs/stream", s.protected(s.streamLogs))

	mux.HandleFunc("GET /", s.staticFile)
	return mux
}

func (s *Server) protected(h http.HandlerFunc) http.Handler {
	return s.auth.Middleware(h)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "version": version.Version})
}

func (s *Server) staticFile(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if path == "index.html" && !s.auth.Authenticated(r) {
		w.Header().Set("Location", "./login.html")
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.FileServer(http.FS(s.static)).ServeHTTP(w, r)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ip := auth.ClientIP(r, s.trustProxyHeaders)
	if !s.limiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, errors.New("too many login attempts"))
		return
	}
	if !s.auth.CheckKey(body.Key) {
		s.limiter.Fail(ip)
		writeError(w, http.StatusUnauthorized, errors.New("invalid admin key"))
		return
	}
	s.limiter.Success(ip)
	if err := s.auth.SetLoginCookie(w, r); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info("login succeeded", map[string]string{"ip": ip})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.auth.ClearLoginCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listGroups(w http.ResponseWriter, _ *http.Request) {
	groups, err := s.store.Groups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	id, err := idgen.New()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	created := proxy.Group{ID: id, Name: name, Created: time.Now(), Proxies: []proxy.Proxy{}}
	if err := s.store.UpdateGroups(func(groups []proxy.Group) ([]proxy.Group, error) {
		return append(groups, created), nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) renameGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	var updated proxy.Group
	if err := s.store.UpdateGroups(func(groups []proxy.Group) ([]proxy.Group, error) {
		idx := groupIndex(groups, r.PathValue("id"))
		if idx < 0 {
			return nil, errGroupNotFound
		}
		groups[idx].Name = name
		updated = groups[idx]
		return groups, nil
	}); err != nil {
		writeStoreMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.UpdateGroups(func(groups []proxy.Group) ([]proxy.Group, error) {
		idx := groupIndex(groups, r.PathValue("id"))
		if idx < 0 {
			return nil, errGroupNotFound
		}
		return append(groups[:idx], groups[idx+1:]...), nil
	}); err != nil {
		writeStoreMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) addProxy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Raw string `json:"raw"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, err := proxy.Parse(body.Raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := assignID(&p); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.store.UpdateGroups(func(groups []proxy.Group) ([]proxy.Group, error) {
		idx := groupIndex(groups, r.PathValue("id"))
		if idx < 0 {
			return nil, errGroupNotFound
		}
		for _, existing := range groups[idx].Proxies {
			if existing.DedupKey() == p.DedupKey() {
				return nil, errDuplicateProxy
			}
		}
		groups[idx].Proxies = append(groups[idx].Proxies, p)
		return groups, nil
	}); err != nil {
		writeStoreMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) bulkProxies(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
		Mode string `json:"mode"`
	}
	if err := readJSONLimited(w, r, &body, maxBulkBodyBytes); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Mode != "replace" && body.Mode != "append" {
		writeError(w, http.StatusBadRequest, errors.New("mode must be replace or append"))
		return
	}
	parsed, parseErrors := proxy.ParseLines(body.Text)
	unique := make([]proxy.Proxy, 0, len(parsed))
	seen := make(map[string]struct{})
	for _, p := range parsed {
		if _, ok := seen[p.DedupKey()]; ok {
			continue
		}
		seen[p.DedupKey()] = struct{}{}
		unique = append(unique, p)
	}
	imported := 0
	skipped := 0
	var updated proxy.Group
	if err := s.store.UpdateGroups(func(groups []proxy.Group) ([]proxy.Group, error) {
		idx := groupIndex(groups, r.PathValue("id"))
		if idx < 0 {
			return nil, errGroupNotFound
		}
		if body.Mode == "replace" {
			oldIDs := make(map[string]string, len(groups[idx].Proxies))
			for _, p := range groups[idx].Proxies {
				oldIDs[p.DedupKey()] = p.ID
			}
			next := make([]proxy.Proxy, 0, len(unique))
			for _, p := range unique {
				if oldID := oldIDs[p.DedupKey()]; oldID != "" {
					p.ID = oldID
				} else if err := assignID(&p); err != nil {
					return nil, err
				}
				next = append(next, p)
			}
			groups[idx].Proxies = next
			imported = len(next)
		} else {
			existing := make(map[string]struct{}, len(groups[idx].Proxies))
			for _, p := range groups[idx].Proxies {
				existing[p.DedupKey()] = struct{}{}
			}
			for _, p := range unique {
				if _, ok := existing[p.DedupKey()]; ok {
					skipped++
					continue
				}
				if err := assignID(&p); err != nil {
					return nil, err
				}
				existing[p.DedupKey()] = struct{}{}
				groups[idx].Proxies = append(groups[idx].Proxies, p)
				imported++
			}
		}
		updated = groups[idx]
		return groups, nil
	}); err != nil {
		writeStoreMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group":              updated,
		"imported":           imported,
		"skipped_duplicates": skipped,
		"errors":             parseErrors,
	})
}

func applyProxyOrder(existing []proxy.Proxy, order []string) ([]proxy.Proxy, error) {
	if len(order) != len(existing) {
		return nil, errReorderMismatch
	}
	byID := make(map[string]proxy.Proxy, len(existing))
	for _, p := range existing {
		byID[p.ID] = p
	}
	next := make([]proxy.Proxy, 0, len(order))
	seen := make(map[string]struct{}, len(order))
	for _, id := range order {
		p, ok := byID[id]
		if !ok {
			return nil, errReorderMismatch
		}
		if _, dup := seen[id]; dup {
			return nil, errReorderMismatch
		}
		seen[id] = struct{}{}
		next = append(next, p)
	}
	return next, nil
}

func (s *Server) reorderProxies(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Order []string `json:"order"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var updated proxy.Group
	if err := s.store.UpdateGroups(func(groups []proxy.Group) ([]proxy.Group, error) {
		idx := groupIndex(groups, r.PathValue("id"))
		if idx < 0 {
			return nil, errGroupNotFound
		}
		next, err := applyProxyOrder(groups[idx].Proxies, body.Order)
		if err != nil {
			return nil, err
		}
		groups[idx].Proxies = next
		updated = groups[idx]
		return groups, nil
	}); err != nil {
		writeStoreMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) updateProxy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label *string `json:"label"`
		Raw   *string `json:"raw"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var parsedRaw proxy.Proxy
	if body.Raw != nil {
		var err error
		parsedRaw, err = proxy.Parse(*body.Raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	var current proxy.Proxy
	if err := s.store.UpdateGroupsAndClearResults(func(groups []proxy.Group) ([]proxy.Group, []string, error) {
		gi, pi := proxyIndex(groups, r.PathValue("id"), r.PathValue("pid"))
		if gi < 0 {
			return nil, nil, errGroupNotFound
		}
		if pi < 0 {
			return nil, nil, errProxyNotFound
		}
		current = groups[gi].Proxies[pi]
		var clearIDs []string
		if body.Raw != nil && strings.TrimSpace(*body.Raw) != current.Raw {
			parsedRaw.ID = current.ID
			parsedRaw.Label = current.Label
			current = parsedRaw
			clearIDs = append(clearIDs, current.ID)
		}
		if body.Label != nil {
			current.Label = strings.TrimSpace(*body.Label)
		}
		groups[gi].Proxies[pi] = current
		return groups, clearIDs, nil
	}); err != nil {
		writeStoreMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, current)
}

func (s *Server) deleteProxy(w http.ResponseWriter, r *http.Request) {
	if err := s.store.UpdateGroups(func(groups []proxy.Group) ([]proxy.Group, error) {
		gi, pi := proxyIndex(groups, r.PathValue("id"), r.PathValue("pid"))
		if gi < 0 {
			return nil, errGroupNotFound
		}
		if pi < 0 {
			return nil, errProxyNotFound
		}
		groups[gi].Proxies = append(groups[gi].Proxies[:pi], groups[gi].Proxies[pi+1:]...)
		return groups, nil
	}); err != nil {
		writeStoreMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) exportGroup(w http.ResponseWriter, r *http.Request) {
	groups, idx, err := s.groupByID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	var lines []string
	for _, p := range groups[idx].Proxies {
		lines = append(lines, p.Raw)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+safeFilename(groups[idx].Name)+".txt\"")
	_, _ = w.Write([]byte(strings.Join(lines, "\n")))
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind     tester.JobKind `json:"kind"`
		GroupID  string         `json:"group_id"`
		ProxyIDs []string       `json:"proxy_ids"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	job, err := s.jobs.Create(body.Kind, body.GroupID, body.ProxyIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"job_id": job.ID, "job": job})
}

func (s *Server) streamJob(w http.ResponseWriter, r *http.Request) {
	ch, err := s.jobs.Subscribe(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer s.jobs.Unsubscribe(r.PathValue("id"), ch)
	streamSSE(w, r, ch)
}

func (s *Server) testOne(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind tester.JobKind `json:"kind"`
		Raw  string         `json:"raw"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p, err := proxy.Parse(body.Raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings, err := s.store.Settings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	switch body.Kind {
	case tester.JobLatency:
		result := s.tester.Latency(r.Context(), p, settings)
		writeJSON(w, http.StatusOK, proxy.ProxyResult{ProxyID: "instant", Latency: &result})
	case tester.JobEcho:
		result := s.tester.Echo(r.Context(), p, settings)
		writeJSON(w, http.StatusOK, proxy.ProxyResult{ProxyID: "instant", Echo: &result})
	default:
		writeError(w, http.StatusBadRequest, errors.New("invalid test kind"))
	}
}

func (s *Server) results(w http.ResponseWriter, r *http.Request) {
	results, err := s.store.Results()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	groupID := r.URL.Query().Get("group_id")
	if groupID == "" {
		writeJSON(w, http.StatusOK, results)
		return
	}
	groups, idx, err := s.groupByID(groupID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	filtered := make(map[string]proxy.ProxyResult)
	for _, p := range groups[idx].Proxies {
		if result, ok := results[p.ID]; ok {
			filtered[p.ID] = result
		}
	}
	writeJSON(w, http.StatusOK, filtered)
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.store.Settings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var settings config.Settings
	if err := readJSON(w, r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings.FillDefaults()
	if err := settings.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveSettings(settings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.configureLogger(settings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		limit = parsed
	}
	writeJSON(w, http.StatusOK, s.logger.Recent(limit))
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	ch := s.logger.Subscribe()
	defer s.logger.Unsubscribe(ch)
	streamSSE(w, r, ch)
}

func (s *Server) configureLogger(settings config.Settings) error {
	return s.logger.ConfigureFile(filepath.Join(s.dataDir, "logs", "app.log"), settings.LogToFile, int64(settings.LogMaxMB)*1024*1024)
}

func (s *Server) groupByID(id string) ([]proxy.Group, int, error) {
	groups, err := s.store.Groups()
	if err != nil {
		return nil, -1, err
	}
	idx := groupIndex(groups, id)
	if idx < 0 {
		return nil, -1, errGroupNotFound
	}
	return groups, idx, nil
}

func groupIndex(groups []proxy.Group, groupID string) int {
	for i := range groups {
		if groups[i].ID == groupID {
			return i
		}
	}
	return -1
}

func proxyIndex(groups []proxy.Group, groupID, proxyID string) (int, int) {
	gi := groupIndex(groups, groupID)
	if gi < 0 {
		return -1, -1
	}
	for i := range groups[gi].Proxies {
		if groups[gi].Proxies[i].ID == proxyID {
			return gi, i
		}
	}
	return gi, -1
}

func assignID(p *proxy.Proxy) error {
	id, err := idgen.New()
	if err != nil {
		return err
	}
	p.ID = id
	return nil
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return readJSONLimited(w, r, target, maxJSONBodyBytes)
}

func readJSONLimited(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeStoreMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errGroupNotFound), errors.Is(err, errProxyNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, errDuplicateProxy):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, errReorderMismatch):
		writeError(w, http.StatusBadRequest, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

func streamSSE[T any](w http.ResponseWriter, r *http.Request, ch <-chan T) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming is not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
				flusher.Flush()
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "proxies"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(name)
}
