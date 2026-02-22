package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shayonj/loraplex/internal/admin"
	"github.com/shayonj/loraplex/internal/cache"
	"github.com/shayonj/loraplex/internal/health"
	"github.com/shayonj/loraplex/internal/origin"
	"github.com/shayonj/loraplex/internal/proxy"
	"github.com/shayonj/loraplex/internal/routing"
)

type testEnv struct {
	vllm     *MockVLLM
	origin   *MockOrigin
	server   *httptest.Server
	cacheMgr *cache.Manager
	dir      string
}

func setup(t *testing.T, maxSize int64) *testEnv {
	t.Helper()

	vllm := NewMockVLLM()
	orig := NewMockOrigin()

	dir := t.TempDir()
	store := cache.NewStore(dir, maxSize)

	httpOrig := origin.NewHTTP(orig.Server.URL)
	origins := []cache.OriginFetcher{httpOrig}

	mgr := cache.NewManager(store, origins, 0)
	if err := mgr.Rebuild(); err != nil {
		t.Fatal(err)
	}

	selfAddr := "127.0.0.1:0"
	ring := routing.NewRing(100, selfAddr)
	ring.Update([]string{selfAddr})

	fwd := routing.NewForwarder(ring, 10*time.Second, 0.9)

	p := proxy.New(proxy.ProxyConfig{
		VLLMUrl:      vllm.Server.URL,
		CacheManager: mgr,
		Ring:         ring,
		Forwarder:    fwd,
		HashOn:       "model",
	})

	checker := health.NewChecker(ring, selfAddr, vllm.Server.URL, 30*time.Second)
	adminH := admin.NewHandler(mgr, ring, fwd)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", checker.ServeHTTP)
	mux.HandleFunc("/admin/", adminH.ServeHTTP)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		checker.IncrPending()
		defer checker.DecrPending()
		p.ServeHTTP(w, r)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(func() {
		ts.Close()
		vllm.Close()
		orig.Close()
	})

	return &testEnv{
		vllm:     vllm,
		origin:   orig,
		server:   ts,
		cacheMgr: mgr,
		dir:      dir,
	}
}

func postJSON(url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return http.Post(url, "application/json", bytes.NewReader(data))
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid JSON response: %s", data)
	}
	return result
}

func chatRequest(serverURL, model string) (*http.Response, error) {
	return postJSON(serverURL+"/v1/chat/completions", map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	})
}

func TestProxyRequestFlow(t *testing.T) {
	env := setup(t, 10<<20)

	t.Run("routes through storage to vLLM", func(t *testing.T) {
		resp, err := chatRequest(env.server.URL, "test-adapter")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		result := readJSON(t, resp)
		if result["object"] != "chat.completion" {
			t.Errorf("expected chat.completion object, got %v", result["object"])
		}
		models := env.vllm.ModelsSeen()
		if models["test-adapter"] < 1 {
			t.Errorf("vLLM did not see model test-adapter, seen: %v", models)
		}
	})

	t.Run("adapter files in storage dir", func(t *testing.T) {
		configPath := filepath.Join(env.dir, "test-adapter", "adapter_config.json")
		if _, err := os.Stat(configPath); err != nil {
			t.Fatalf("adapter_config.json not in storage: %v", err)
		}

		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg["peft_type"] != "LORA" {
			t.Errorf("expected peft_type LORA, got %v", cfg["peft_type"])
		}

		sfPath := filepath.Join(env.dir, "test-adapter", "adapter_model.safetensors")
		if _, err := os.Stat(sfPath); err != nil {
			t.Errorf("adapter_model.safetensors not in storage: %v", err)
		}
	})

	t.Run("second request hits cache", func(t *testing.T) {
		before := env.origin.FetchCount()

		resp, err := chatRequest(env.server.URL, "test-adapter")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		after := env.origin.FetchCount()
		if after != before {
			t.Errorf("expected no origin fetch on cache hit, got %d new requests", after-before)
		}
	})
}

func TestCacheEviction(t *testing.T) {
	env := setup(t, 2000)

	resp, err := chatRequest(env.server.URL, "adapter-a")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !env.cacheMgr.Store().Has("adapter-a") {
		t.Fatal("adapter-a not in storage after first request")
	}

	resp, err = chatRequest(env.server.URL, "adapter-b")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !env.cacheMgr.Store().Has("adapter-b") {
		t.Error("adapter-b not in storage after second request")
	}
	if env.cacheMgr.Store().Has("adapter-a") {
		t.Error("adapter-a should have been evicted")
	}
}

func TestAdminEvict(t *testing.T) {
	env := setup(t, 10<<20)

	resp, err := chatRequest(env.server.URL, "evict-me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !env.cacheMgr.Store().Has("evict-me") {
		t.Fatal("adapter not in storage before eviction")
	}

	resp, err = postJSON(env.server.URL+"/admin/cache/evict", map[string]string{
		"adapter": "evict-me",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	result := readJSON(t, resp)

	if result["status"] != "evicted" {
		t.Errorf("expected status evicted, got %v", result["status"])
	}
	if env.cacheMgr.Store().Has("evict-me") {
		t.Error("adapter still in storage after eviction")
	}
}

func TestHealthEndpoint(t *testing.T) {
	env := setup(t, 10<<20)

	resp, err := http.Get(env.server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}
	result := readJSON(t, resp)

	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %v", result["status"])
	}
	if _, ok := result["load"]; !ok {
		t.Error("missing load field")
	}
	if _, ok := result["pending"]; !ok {
		t.Error("missing pending field")
	}
	if _, ok := result["vllm_healthy"]; !ok {
		t.Error("missing vllm_healthy field")
	}
}

func TestCacheStats(t *testing.T) {
	env := setup(t, 10<<20)

	resp, err := chatRequest(env.server.URL, "stats-adapter")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp, err = http.Get(env.server.URL + "/admin/cache/stats")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	result := readJSON(t, resp)

	if items, _ := result["items"].(float64); items < 1 {
		t.Errorf("expected at least 1 item, got %v", result["items"])
	}
	if size, _ := result["size"].(float64); size <= 0 {
		t.Errorf("expected positive size, got %v", result["size"])
	}
}

func TestBaseModelPassthrough(t *testing.T) {
	vllm := NewMockVLLM()
	orig := NewMockOrigin()

	dir := t.TempDir()
	mgr := cache.NewManager(
		cache.NewStore(dir, 10<<20),
		[]cache.OriginFetcher{origin.NewHTTP(orig.Server.URL)},
		0,
	)
	if err := mgr.Rebuild(); err != nil {
		t.Fatal(err)
	}

	selfAddr := "127.0.0.1:0"
	ring := routing.NewRing(100, selfAddr)
	ring.Update([]string{selfAddr})

	p := proxy.New(proxy.ProxyConfig{
		VLLMUrl:      vllm.Server.URL,
		BaseModel:    "meta-llama/Llama-3.1-8B-Instruct",
		CacheManager: mgr,
		Ring:         ring,
		Forwarder:    routing.NewForwarder(ring, 10*time.Second, 0.9),
		HashOn:       "model",
	})
	ts := httptest.NewServer(p)
	t.Cleanup(func() {
		ts.Close()
		vllm.Close()
		orig.Close()
	})

	resp, err := chatRequest(ts.URL, "meta-llama/Llama-3.1-8B-Instruct")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	if orig.FetchCount() != 0 {
		t.Errorf("base model request should not fetch from origin, got %d fetches", orig.FetchCount())
	}
	if mgr.Store().Has("meta-llama/Llama-3.1-8B-Instruct") {
		t.Error("base model should not be stored as an adapter")
	}
}

type nodeEnv struct {
	server   *httptest.Server
	addr     string
	ring     *routing.Ring
	cacheMgr *cache.Manager
	dir      string
}

func setupMultiNode(t *testing.T, overflowThreshold float64) (a, b *nodeEnv, cleanup func()) {
	t.Helper()
	vllm := NewMockVLLM()
	orig := NewMockOrigin()

	muxA := http.NewServeMux()
	muxB := http.NewServeMux()
	srvA := httptest.NewUnstartedServer(muxA)
	srvB := httptest.NewUnstartedServer(muxB)
	srvA.Start()
	srvB.Start()

	addrA := strings.TrimPrefix(srvA.URL, "http://")
	addrB := strings.TrimPrefix(srvB.URL, "http://")

	makeNode := func(selfAddr string) *nodeEnv {
		dir := t.TempDir()
		mgr := cache.NewManager(
			cache.NewStore(dir, 10<<20),
			[]cache.OriginFetcher{origin.NewHTTP(orig.Server.URL)},
			0,
		)
		mgr.Rebuild()
		ring := routing.NewRing(100, selfAddr)
		ring.Update([]string{addrA, addrB})
		return &nodeEnv{ring: ring, cacheMgr: mgr, dir: dir, addr: selfAddr}
	}

	nA := makeNode(addrA)
	nB := makeNode(addrB)

	proxyA := proxy.New(proxy.ProxyConfig{
		VLLMUrl:      vllm.Server.URL,
		CacheManager: nA.cacheMgr,
		Ring:         nA.ring,
		Forwarder:    routing.NewForwarder(nA.ring, 5*time.Second, overflowThreshold),
		HashOn:       "model",
	})
	proxyB := proxy.New(proxy.ProxyConfig{
		VLLMUrl:      vllm.Server.URL,
		CacheManager: nB.cacheMgr,
		Ring:         nB.ring,
		Forwarder:    routing.NewForwarder(nB.ring, 5*time.Second, overflowThreshold),
		HashOn:       "model",
	})

	muxA.HandleFunc("/", proxyA.ServeHTTP)
	muxB.HandleFunc("/", proxyB.ServeHTTP)

	nA.server = srvA
	nB.server = srvB

	return nA, nB, func() {
		srvA.Close()
		srvB.Close()
		vllm.Close()
		orig.Close()
	}
}

func findModelOwnedBy(ring *routing.Ring, targetAddr string) string {
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("adapter-%d", i)
		if ring.Owner(key).Addr == targetAddr {
			return key
		}
	}
	return ""
}

func TestMultiNodeForward(t *testing.T) {
	nodeA, nodeB, cleanup := setupMultiNode(t, 0.8)
	defer cleanup()

	model := findModelOwnedBy(nodeA.ring, nodeB.addr)
	if model == "" {
		t.Fatal("could not find adapter owned by node B")
	}

	resp, err := chatRequest(nodeA.server.URL, model)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	if !nodeB.cacheMgr.Store().Has(model) {
		t.Error("expected adapter in node B's storage after forwarding")
	}
	if nodeA.cacheMgr.Store().Has(model) {
		t.Error("adapter should not be in node A's storage (was forwarded to B)")
	}
}

func TestForwardToDeadPeerFallsBackLocal(t *testing.T) {
	nodeA, nodeB, cleanup := setupMultiNode(t, 0.8)
	defer cleanup()

	model := findModelOwnedBy(nodeA.ring, nodeB.addr)
	if model == "" {
		t.Fatal("could not find adapter owned by node B")
	}

	nodeB.server.Close()

	resp, err := chatRequest(nodeA.server.URL, model)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200 after fallback to local, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	if !nodeA.cacheMgr.Store().Has(model) {
		t.Error("expected adapter in node A's storage after local fallback")
	}
}

func TestOverflowHandledLocally(t *testing.T) {
	nodeA, nodeB, cleanup := setupMultiNode(t, 0.8)
	defer cleanup()

	nodeA.ring.UpdatePeerLoad(nodeB.addr, 0.9)

	model := findModelOwnedBy(nodeA.ring, nodeB.addr)
	if model == "" {
		t.Fatal("could not find adapter owned by node B")
	}

	resp, err := chatRequest(nodeA.server.URL, model)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	if !nodeA.cacheMgr.Store().Has(model) {
		t.Error("expected adapter in node A's storage (overflow handled locally)")
	}
	if nodeB.cacheMgr.Store().Has(model) {
		t.Error("adapter should not be in node B's storage (overflow bypassed it)")
	}
}

func TestBaseModelRoutedByHeader(t *testing.T) {
	vllmA := NewMockVLLM()
	vllmB := NewMockVLLM()
	orig := NewMockOrigin()

	muxA := http.NewServeMux()
	muxB := http.NewServeMux()
	srvA := httptest.NewUnstartedServer(muxA)
	srvB := httptest.NewUnstartedServer(muxB)
	srvA.Start()
	srvB.Start()

	addrA := strings.TrimPrefix(srvA.URL, "http://")
	addrB := strings.TrimPrefix(srvB.URL, "http://")

	makeNode := func(selfAddr string) *nodeEnv {
		dir := t.TempDir()
		mgr := cache.NewManager(
			cache.NewStore(dir, 10<<20),
			[]cache.OriginFetcher{origin.NewHTTP(orig.Server.URL)},
			0,
		)
		mgr.Rebuild()
		ring := routing.NewRing(100, selfAddr)
		ring.Update([]string{addrA, addrB})
		return &nodeEnv{ring: ring, cacheMgr: mgr, dir: dir, addr: selfAddr}
	}

	nA := makeNode(addrA)
	nB := makeNode(addrB)

	proxyA := proxy.New(proxy.ProxyConfig{
		VLLMUrl:      vllmA.Server.URL,
		BaseModel:    "base-model",
		CacheManager: nA.cacheMgr,
		Ring:         nA.ring,
		Forwarder:    routing.NewForwarder(nA.ring, 5*time.Second, 0.8),
		HashOn:       "header:X-Session-ID",
	})
	proxyB := proxy.New(proxy.ProxyConfig{
		VLLMUrl:      vllmB.Server.URL,
		BaseModel:    "base-model",
		CacheManager: nB.cacheMgr,
		Ring:         nB.ring,
		Forwarder:    routing.NewForwarder(nB.ring, 5*time.Second, 0.8),
		HashOn:       "header:X-Session-ID",
	})

	muxA.HandleFunc("/", proxyA.ServeHTTP)
	muxB.HandleFunc("/", proxyB.ServeHTTP)
	nA.server = srvA
	nB.server = srvB

	defer func() {
		srvA.Close()
		srvB.Close()
		vllmA.Close()
		vllmB.Close()
		orig.Close()
	}()

	sessionID := ""
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("session-%d", i)
		if nA.ring.Owner(key).Addr == addrB {
			sessionID = key
			break
		}
	}
	if sessionID == "" {
		t.Fatal("could not find session ID that hashes to node B")
	}

	body, _ := json.Marshal(map[string]any{
		"model":    "base-model",
		"messages": []map[string]string{{"role": "user", "content": "Hello"}},
	})
	req, _ := http.NewRequest("POST", srvA.URL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Session-ID", sessionID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	bSeen := vllmB.ModelsSeen()
	aSeen := vllmA.ModelsSeen()
	if bSeen["base-model"] != 1 {
		t.Errorf("expected node B's vLLM to see 1 request, got %d", bSeen["base-model"])
	}
	if aSeen["base-model"] != 0 {
		t.Errorf("expected node A's vLLM to see 0 requests (forwarded to B), got %d", aSeen["base-model"])
	}

	if orig.FetchCount() != 0 {
		t.Errorf("base model request should not fetch from origin, got %d", orig.FetchCount())
	}
}
