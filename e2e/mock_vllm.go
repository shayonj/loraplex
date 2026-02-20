package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
)

type MockVLLM struct {
	Server *httptest.Server
	mu     sync.Mutex
	models map[string]int
}

func NewMockVLLM() *MockVLLM {
	m := &MockVLLM{models: make(map[string]int)}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", m.chatCompletions)
	mux.HandleFunc("/v1/models", m.listModels)
	mux.HandleFunc("/health", m.healthCheck)
	m.Server = httptest.NewServer(mux)
	return m
}

func (m *MockVLLM) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var req map[string]json.RawMessage
	json.NewDecoder(r.Body).Decode(&req)
	if raw, ok := req["model"]; ok {
		var model string
		json.Unmarshal(raw, &model)
		m.mu.Lock()
		m.models[model]++
		m.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   "base-model",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]string{
				"role":    "assistant",
				"content": "Hello from mock vLLM!",
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     10,
			"completion_tokens": 5,
			"total_tokens":      15,
		},
	})
}

func (m *MockVLLM) listModels(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data": []map[string]any{{
			"id":       "base-model",
			"object":   "model",
			"owned_by": "test",
		}},
	})
}

func (m *MockVLLM) healthCheck(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (m *MockVLLM) ModelsSeen() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.models))
	for k, v := range m.models {
		out[k] = v
	}
	return out
}

func (m *MockVLLM) Close() {
	m.Server.Close()
}
