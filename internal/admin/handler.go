package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/shayonj/loraplex/internal/cache"
	"github.com/shayonj/loraplex/internal/routing"
)

type Handler struct {
	cacheMgr *cache.Manager
	ring     *routing.Ring
	fwd      *routing.Forwarder
}

func NewHandler(cacheMgr *cache.Manager, ring *routing.Ring, fwd *routing.Forwarder) *Handler {
	return &Handler{
		cacheMgr: cacheMgr,
		ring:     ring,
		fwd:      fwd,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET" && r.URL.Path == "/admin/cache/stats":
		h.cacheStats(w, r)
	case r.Method == "GET" && r.URL.Path == "/admin/peers":
		h.peers(w, r)
	case r.Method == "POST" && r.URL.Path == "/admin/cache/evict":
		h.evict(w, r)
	case r.Method == "POST" && r.URL.Path == "/admin/cache/warmup":
		h.warmup(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) cacheStats(w http.ResponseWriter, _ *http.Request) {
	s := h.cacheMgr.Store()

	stats := map[string]any{
		"items":    s.LRU().Len(),
		"size":     s.LRU().Size(),
		"max_size": s.LRU().MaxSize(),
		"dir":      s.BasePath,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (h *Handler) peers(w http.ResponseWriter, _ *http.Request) {
	peers := h.ring.Peers()
	result := make([]map[string]any, 0, len(peers))
	for _, p := range peers {
		result = append(result, map[string]any{
			"addr": p.Addr,
			"load": p.Load,
			"self": h.ring.IsSelf(p),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"peers": result,
		"self":  h.ring.Self(),
		"count": len(result),
	})
}

type evictRequest struct {
	Adapter string `json:"adapter"`
}

func (h *Handler) evict(w http.ResponseWriter, r *http.Request) {
	var req evictRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Adapter == "" {
		http.Error(w, "adapter field required", http.StatusBadRequest)
		return
	}

	if err := h.cacheMgr.EvictAdapter(req.Adapter); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isForwarded := r.Header.Get(routing.HeaderForwarded) == "true"
	if !isForwarded {
		peers := h.ring.Peers()
		for _, p := range peers {
			if h.ring.IsSelf(p) {
				continue
			}
			body, _ := json.Marshal(req)
			go func(peer *routing.Peer) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				h.fwd.Forward(ctx, peer, "POST", "/admin/cache/evict", body, http.Header{
					routing.HeaderForwarded: []string{"true"},
				})
			}(p)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "evicted", "adapter": req.Adapter})
}

type warmupRequest struct {
	Adapters []string `json:"adapters"`
}

func (h *Handler) warmup(w http.ResponseWriter, r *http.Request) {
	var req warmupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	results := make([]map[string]string, 0, len(req.Adapters))
	for _, adapter := range req.Adapters {
		_, err := h.cacheMgr.EnsureAdapter(r.Context(), adapter)
		status := "ok"
		if err != nil {
			status = fmt.Sprintf("error: %v", err)
		}
		results = append(results, map[string]string{"adapter": adapter, "status": status})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": results})
}
