package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
)

type MockOrigin struct {
	Server     *httptest.Server
	fetchCount atomic.Int64
	mu         sync.Mutex
	fetched    map[string]int
}

func NewMockOrigin() *MockOrigin {
	m := &MockOrigin{fetched: make(map[string]int)}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	return m
}

func (m *MockOrigin) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}

	adapterID := parts[0]
	filename := parts[1]

	m.fetchCount.Add(1)

	if filename == "adapter_config.json" && r.Method == http.MethodGet {
		m.mu.Lock()
		m.fetched[adapterID]++
		m.mu.Unlock()
	}

	switch filename {
	case "adapter_config.json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"etag-`+adapterID+`"`)
		if r.Method == http.MethodHead {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"peft_type":               "LORA",
			"base_model_name_or_path": "meta-llama/Llama-2-7b-hf",
			"r":                       16,
			"lora_alpha":              32,
		})
	case "adapter_model.safetensors":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(make([]byte, 1024))
	default:
		http.NotFound(w, r)
	}
}

func (m *MockOrigin) FetchCount() int64 {
	return m.fetchCount.Load()
}

func (m *MockOrigin) AdapterFetchCount(adapterID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fetched[adapterID]
}

func (m *MockOrigin) ResetCounts() {
	m.fetchCount.Store(0)
	m.mu.Lock()
	m.fetched = make(map[string]int)
	m.mu.Unlock()
}

func (m *MockOrigin) Close() {
	m.Server.Close()
}
