package tester

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"proxy-hub/internal/idgen"
	"proxy-hub/internal/logbuf"
	"proxy-hub/internal/proxy"
	"proxy-hub/internal/store"
)

type JobKind string

const (
	JobLatency JobKind = "latency"
	JobEcho    JobKind = "echo"
)

type Job struct {
	ID         string    `json:"id"`
	Kind       JobKind   `json:"kind"`
	GroupID    string    `json:"group_id"`
	Total      int       `json:"total"`
	Done       int       `json:"done"`
	Running    bool      `json:"running"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type JobEvent struct {
	Type    string             `json:"type"`
	Job     Job                `json:"job"`
	ProxyID string             `json:"proxy_id,omitempty"`
	Result  *proxy.ProxyResult `json:"result,omitempty"`
}

type Manager struct {
	store  store.Store
	tester *Tester
	logger *logbuf.Logger

	mu   sync.Mutex
	jobs map[string]*jobState
}

type jobState struct {
	job         Job
	subscribers map[chan JobEvent]struct{}
}

func NewManager(store store.Store, tester *Tester, logger *logbuf.Logger) *Manager {
	return &Manager{
		store:  store,
		tester: tester,
		logger: logger,
		jobs:   make(map[string]*jobState),
	}
}

func (m *Manager) Create(kind JobKind, groupID string, proxyIDs []string) (Job, error) {
	if kind != JobLatency && kind != JobEcho {
		return Job{}, errors.New("invalid job kind")
	}
	groups, err := m.store.Groups()
	if err != nil {
		return Job{}, err
	}
	var group *proxy.Group
	for i := range groups {
		if groups[i].ID == groupID {
			group = &groups[i]
			break
		}
	}
	if group == nil {
		return Job{}, errors.New("group not found")
	}
	selected := selectProxies(group.Proxies, proxyIDs)
	if len(selected) == 0 {
		return Job{}, errors.New("no proxies selected")
	}
	id, err := idgen.New()
	if err != nil {
		return Job{}, err
	}
	job := Job{
		ID:        id,
		Kind:      kind,
		GroupID:   groupID,
		Total:     len(selected),
		Running:   true,
		StartedAt: time.Now(),
	}
	state := &jobState{
		job:         job,
		subscribers: make(map[chan JobEvent]struct{}),
	}
	m.mu.Lock()
	m.jobs[id] = state
	m.mu.Unlock()
	go m.run(id, selected)
	return job, nil
}

func (m *Manager) Subscribe(jobID string) (chan JobEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.jobs[jobID]
	if state == nil {
		return nil, errors.New("job not found")
	}
	ch := make(chan JobEvent, 32)
	state.subscribers[ch] = struct{}{}
	ch <- JobEvent{Type: "snapshot", Job: state.job}
	if !state.job.Running {
		ch <- JobEvent{Type: "complete", Job: state.job}
	}
	return ch, nil
}

func (m *Manager) Unsubscribe(jobID string, ch chan JobEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.jobs[jobID]; state != nil {
		delete(state.subscribers, ch)
	}
	close(ch)
}

func (m *Manager) run(jobID string, proxies []proxy.Proxy) {
	settings, err := m.store.Settings()
	if err != nil {
		m.finish(jobID, nil, err)
		return
	}
	concurrency := settings.Concurrency
	if concurrency > len(proxies) {
		concurrency = len(proxies)
	}
	tasks := make(chan proxy.Proxy)
	updates := make(map[string]proxy.ProxyResult)
	var updatesMu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range tasks {
				result := proxy.ProxyResult{ProxyID: p.ID}
				if m.kind(jobID) == JobLatency {
					latency := m.tester.Latency(context.Background(), p, settings)
					result.Latency = &latency
					m.logLatency(p, latency)
				} else {
					echo := m.tester.Echo(context.Background(), p, settings)
					result.Echo = &echo
					m.logEcho(p, echo)
				}
				updatesMu.Lock()
				updates[p.ID] = result
				updatesMu.Unlock()
				m.progress(jobID, p.ID, result)
			}
		}()
	}
	for _, p := range proxies {
		tasks <- p
	}
	close(tasks)
	wg.Wait()
	m.finish(jobID, updates, nil)
}

func (m *Manager) kind(jobID string) JobKind {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.jobs[jobID]; state != nil {
		return state.job.Kind
	}
	return JobLatency
}

func (m *Manager) progress(jobID, proxyID string, result proxy.ProxyResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.jobs[jobID]
	if state == nil {
		return
	}
	state.job.Done++
	event := JobEvent{
		Type:    "result",
		Job:     state.job,
		ProxyID: proxyID,
		Result:  &result,
	}
	m.broadcastLocked(state, event)
}

func (m *Manager) finish(jobID string, updates map[string]proxy.ProxyResult, runErr error) {
	errText := ""
	if runErr == nil && len(updates) > 0 {
		if err := m.store.MergeResults(updates); err != nil {
			errText = err.Error()
		}
	} else if runErr != nil {
		errText = runErr.Error()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.jobs[jobID]
	if state == nil {
		return
	}
	state.job.Running = false
	state.job.FinishedAt = time.Now()
	state.job.Error = errText
	m.broadcastLocked(state, JobEvent{Type: "complete", Job: state.job})
}

func (m *Manager) broadcastLocked(state *jobState, event JobEvent) {
	for ch := range state.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (m *Manager) logLatency(p proxy.Proxy, result proxy.LatencyResult) {
	fields := map[string]string{
		"scheme":  p.Scheme,
		"host":    p.Host,
		"port":    strconv.Itoa(p.Port),
		"ok":      strconv.FormatBool(result.OK),
		"latency": strconv.Itoa(result.LatencyMs),
	}
	if result.DNSMode != "" {
		fields["dns_mode"] = result.DNSMode
	}
	if result.OK {
		m.logger.Info("latency test completed", fields)
		return
	}
	fields["error"] = result.Error
	m.logger.Warn("latency test failed", fields)
}

func (m *Manager) logEcho(p proxy.Proxy, result proxy.EchoResult) {
	fields := map[string]string{
		"scheme":       p.Scheme,
		"host":         p.Host,
		"port":         strconv.Itoa(p.Port),
		"ok":           strconv.FormatBool(result.OK),
		"source":       result.Source,
		"ip":           result.EchoIP,
		"country_code": result.CountryCode,
		"asn":          strconv.Itoa(result.ASN),
		"asn_org":      result.ASNOrganization,
		"org":          result.Organization,
	}
	if result.OK {
		m.logger.Info("IP echo completed", fields)
		return
	}
	fields["error"] = result.Error
	m.logger.Warn("IP echo failed", fields)
}

func selectProxies(proxies []proxy.Proxy, ids []string) []proxy.Proxy {
	if len(ids) == 0 {
		return append([]proxy.Proxy(nil), proxies...)
	}
	allowed := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	selected := make([]proxy.Proxy, 0, len(ids))
	for _, p := range proxies {
		if _, ok := allowed[p.ID]; ok {
			selected = append(selected, p)
		}
	}
	return selected
}
