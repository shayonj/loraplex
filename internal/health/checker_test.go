package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shayonj/loraplex/internal/routing"
)

func TestLoadCalculation(t *testing.T) {
	ring := routing.NewRing(10, "self:9090")
	c := NewChecker(ring, "self:9090", "", 10*time.Second)

	if c.Load(200) != 0.0 {
		t.Fatalf("expected load 0.0, got %f", c.Load(200))
	}

	for range 100 {
		c.IncrPending()
	}
	got := c.Load(200)
	if got < 0.49 || got > 0.51 {
		t.Fatalf("expected load ~0.5, got %f", got)
	}

	for range 200 {
		c.IncrPending()
	}
	got = c.Load(200)
	if got != 1.0 {
		t.Fatalf("expected load capped at 1.0, got %f", got)
	}
}

func TestLoadZeroMax(t *testing.T) {
	ring := routing.NewRing(10, "self:9090")
	c := NewChecker(ring, "self:9090", "", 10*time.Second)
	c.IncrPending()

	got := c.Load(0)
	if got < 0.004 || got > 0.006 {
		t.Fatalf("expected load ~0.005 (1/200 fallback), got %f", got)
	}
}

func TestPendingCounters(t *testing.T) {
	ring := routing.NewRing(10, "self:9090")
	c := NewChecker(ring, "self:9090", "", 10*time.Second)

	c.IncrPending()
	c.IncrPending()
	c.IncrPending()
	if c.Pending() != 3 {
		t.Fatalf("expected 3 pending, got %d", c.Pending())
	}

	c.DecrPending()
	if c.Pending() != 2 {
		t.Fatalf("expected 2 pending, got %d", c.Pending())
	}
}

func TestHealthEndpointOK(t *testing.T) {
	ring := routing.NewRing(10, "self:9090")
	c := NewChecker(ring, "self:9090", "", 10*time.Second)

	c.IncrPending()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	c.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %s", resp.Status)
	}
	if resp.Pending != 1 {
		t.Fatalf("expected 1 pending, got %d", resp.Pending)
	}
	if !resp.VLLM {
		t.Fatal("expected vllm_healthy true by default")
	}
	if resp.Load < 0.004 || resp.Load > 0.006 {
		t.Fatalf("expected load ~0.005, got %f", resp.Load)
	}
}

func TestHealthEndpointDegraded(t *testing.T) {
	ring := routing.NewRing(10, "self:9090")
	c := NewChecker(ring, "self:9090", "", 10*time.Second)
	c.vllmHealthy.Store(false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	c.ServeHTTP(rec, req)

	var resp HealthResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Status != "degraded" {
		t.Fatalf("expected degraded, got %s", resp.Status)
	}
	if resp.VLLM {
		t.Fatal("expected vllm_healthy false")
	}
}

func TestCheckVLLM(t *testing.T) {
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer vllm.Close()

	ring := routing.NewRing(10, "self:9090")
	c := NewChecker(ring, "self:9090", vllm.URL, 10*time.Second)
	c.vllmHealthy.Store(false)

	c.checkPeers(t.Context())

	if !c.vllmHealthy.Load() {
		t.Fatal("expected vllm healthy after successful check")
	}
}

func TestCheckVLLMDown(t *testing.T) {
	ring := routing.NewRing(10, "self:9090")
	c := NewChecker(ring, "self:9090", "http://127.0.0.1:1", 10*time.Second)

	c.checkPeers(t.Context())

	if c.vllmHealthy.Load() {
		t.Fatal("expected vllm unhealthy after failed check")
	}
}

func TestCheckPeerUpdatesLoad(t *testing.T) {
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(HealthResponse{
			Status:  "ok",
			Load:    0.75,
			Pending: 150,
			VLLM:    true,
		})
	}))
	defer peerSrv.Close()

	ring := routing.NewRing(10, "self:9090")
	ring.Update([]string{"self:9090", peerSrv.Listener.Addr().String()})

	c := NewChecker(ring, "self:9090", "", 10*time.Second)

	peerAddr := peerSrv.Listener.Addr().String()
	peer := &routing.Peer{Addr: peerAddr}
	c.checkPeer(t.Context(), peer)

	peers := ring.Peers()
	for _, p := range peers {
		if p.Addr == peerAddr {
			if p.Load < 0.74 || p.Load > 0.76 {
				t.Fatalf("expected peer load ~0.75, got %f", p.Load)
			}
			return
		}
	}
	t.Fatal("peer not found in ring")
}
