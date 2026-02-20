package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/shayonj/loraplex/internal/metrics"
	"github.com/shayonj/loraplex/internal/routing"
)

type Checker struct {
	ring     *routing.Ring
	selfAddr string
	vllmURL  string
	interval time.Duration
	client   *http.Client
	stopCh   chan struct{}

	vllmHealthy  atomic.Bool
	pendingCount atomic.Int64
}

func NewChecker(ring *routing.Ring, selfAddr, vllmURL string, interval time.Duration) *Checker {
	c := &Checker{
		ring:     ring,
		selfAddr: selfAddr,
		vllmURL:  vllmURL,
		interval: interval,
		client:   &http.Client{Timeout: 3 * time.Second},
		stopCh:   make(chan struct{}),
	}
	c.vllmHealthy.Store(true)
	return c
}

func (c *Checker) Start(ctx context.Context) {
	go c.loop(ctx)
}

func (c *Checker) Stop() {
	close(c.stopCh)
}

func (c *Checker) loop(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.checkPeers(ctx)
		}
	}
}

func (c *Checker) checkPeers(ctx context.Context) {
	c.checkVLLM(ctx)

	peers := c.ring.Peers()
	for _, peer := range peers {
		if peer.Addr == c.selfAddr {
			continue
		}
		go c.checkPeer(ctx, peer)
	}
}

func (c *Checker) checkVLLM(ctx context.Context) {
	if c.vllmURL == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.vllmURL+"/health", nil)
	if err != nil {
		return
	}
	resp, err := c.client.Do(req)
	healthy := err == nil && resp != nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		resp.Body.Close()
	}
	c.vllmHealthy.Store(healthy)
	if healthy {
		metrics.VLLMHealthy.Set(1)
	} else {
		metrics.VLLMHealthy.Set(0)
	}
}

func (c *Checker) checkPeer(ctx context.Context, peer *routing.Peer) {
	url := fmt.Sprintf("http://%s/healthz", peer.Addr)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return
	}

	resp, err := c.client.Do(req)
	if err != nil {
		slog.Debug("peer health check failed", "peer", peer.Addr, "err", err)
		c.ring.UpdatePeerLoad(peer.Addr, 1.0)
		return
	}
	defer resp.Body.Close()

	var status HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return
	}
	c.ring.UpdatePeerLoad(peer.Addr, status.Load)
}

func (c *Checker) IncrPending()  { c.pendingCount.Add(1) }
func (c *Checker) DecrPending()  { c.pendingCount.Add(-1) }
func (c *Checker) Pending() int64 { return c.pendingCount.Load() }

// Load returns a normalized load score between 0.0 and 1.0.
func (c *Checker) Load(maxConcurrent int) float64 {
	if maxConcurrent <= 0 {
		maxConcurrent = 200
	}
	load := float64(c.pendingCount.Load()) / float64(maxConcurrent)
	if load > 1.0 {
		load = 1.0
	}
	return load
}

type HealthResponse struct {
	Status  string  `json:"status"`
	Load    float64 `json:"load"`
	Pending int64   `json:"pending"`
	VLLM    bool    `json:"vllm_healthy"`
}

func (c *Checker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:  "ok",
		Load:    c.Load(200),
		Pending: c.pendingCount.Load(),
		VLLM:    c.vllmHealthy.Load(),
	}
	if !resp.VLLM {
		resp.Status = "degraded"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
