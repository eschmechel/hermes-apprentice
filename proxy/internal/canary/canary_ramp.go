package canary

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
)

type RampState string

const (
	StateWarming RampState = "warming"
	StateLive    RampState = "live"
	StateBroken  RampState = "broken"
)

type PatternRamp struct {
	PatternID    string    `json:"pattern_id"`
	State        RampState `json:"state"`
	Pct          int       `json:"pct"`
	RequestCount int       `json:"request_count"`
	AgreementSum float64   `json:"agreement_sum"`
}

type Config struct {
	StartPct        int
	StepPct         int
	StepRequests    int
	AgreementThresh float64
}

func DefaultConfig() Config {
	return Config{
		StartPct:        5,
		StepPct:         10,
		StepRequests:    50,
		AgreementThresh: 0.8,
	}
}

type Manager struct {
	mu     sync.RWMutex
	config Config
	ramps  map[string]*PatternRamp
	path   string
	randFn func() float64
}

func New(path string, config Config) (*Manager, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("canary: mkdir %s: %w", filepath.Dir(path), err)
	}
	if config.StartPct <= 0 {
		config.StartPct = DefaultConfig().StartPct
	}
	if config.StepPct <= 0 {
		config.StepPct = DefaultConfig().StepPct
	}
	if config.StepRequests <= 0 {
		config.StepRequests = DefaultConfig().StepRequests
	}
	if config.AgreementThresh <= 0 {
		config.AgreementThresh = DefaultConfig().AgreementThresh
	}
	m := &Manager{
		config: config,
		ramps:  make(map[string]*PatternRamp),
		path:   path,
		randFn: rand.Float64,
	}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		var list []PatternRamp
		if decErr := json.NewDecoder(f).Decode(&list); decErr == nil {
			for i := range list {
				m.ramps[list[i].PatternID] = &list[i]
			}
		}
	}
	return m, nil
}

func (m *Manager) Register(patternID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.ramps[patternID]; exists {
		return nil
	}
	m.ramps[patternID] = &PatternRamp{
		PatternID: patternID,
		State:     StateWarming,
		Pct:       m.config.StartPct,
	}
	return m.persist()
}

func (m *Manager) Decision(patternID string) (routeSpecialist bool, managed bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.ramps[patternID]
	if !ok {
		return true, false
	}
	switch r.State {
	case StateLive:
		return true, true
	case StateBroken:
		return false, true
	case StateWarming:
		return m.randFn() < float64(r.Pct)/100.0, true
	default:
		return true, true
	}
}

func (m *Manager) RecordAgreement(patternID string, score float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.ramps[patternID]
	if !ok || r.State != StateWarming {
		return false
	}
	r.RequestCount++
	r.AgreementSum += score
	return r.RequestCount >= m.config.StepRequests
}

func (m *Manager) Advance(patternID string, score float64) (RampState, bool, error) {
	ready := m.RecordAgreement(patternID, score)
	if !ready {
		r, _ := m.State(patternID)
		return r.State, false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.ramps[patternID]
	if !ok || r.State != StateWarming {
		return StateWarming, false, nil
	}
	avgAgreement := r.AgreementSum / float64(r.RequestCount)
	r.RequestCount = 0
	r.AgreementSum = 0
	if avgAgreement < m.config.AgreementThresh {
		r.State = StateBroken
		r.Pct = 0
		_ = m.persist()
		return StateBroken, true, nil
	}
	newPct := r.Pct + m.config.StepPct
	if newPct >= 100 {
		r.State = StateLive
		r.Pct = 100
		_ = m.persist()
		return StateLive, true, nil
	}
	r.Pct = newPct
	_ = m.persist()
	return StateWarming, true, nil
}

func (m *Manager) State(patternID string) (PatternRamp, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.ramps[patternID]
	if !ok {
		return PatternRamp{}, false
	}
	return *r, true
}

func (m *Manager) List() []PatternRamp {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PatternRamp, 0, len(m.ramps))
	for _, r := range m.ramps {
		out = append(out, *r)
	}
	return out
}

func (m *Manager) SetState(patternID string, state RampState, pct int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.ramps[patternID]
	if !ok {
		return fmt.Errorf("canary: pattern %q not found", patternID)
	}
	r.State = state
	r.Pct = pct
	if state == StateBroken {
		r.RequestCount = 0
		r.AgreementSum = 0
	}
	return m.persist()
}

func (m *Manager) SendAlert(patternID string) string {
	r, ok := m.State(patternID)
	if !ok || r.State != StateBroken {
		return ""
	}
	return fmt.Sprintf("⚠️ Canary: pattern %q demoted to broken. Agreement fell below threshold. No specialist responses served until resolved.", patternID)
}

func (m *Manager) persist() error {
	list := make([]PatternRamp, 0, len(m.ramps))
	for _, r := range m.ramps {
		list = append(list, *r)
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".canary-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(list); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	tmp.Close()
	return os.Rename(tmpName, m.path)
}
