package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/shayonj/loraplex/internal/cache"
	"github.com/shayonj/loraplex/internal/metrics"
	"github.com/shayonj/loraplex/internal/routing"
)

type Proxy struct {
	vllmURL      string
	baseModel    string
	cacheMgr     *cache.Manager
	ring         *routing.Ring
	forwarder    *routing.Forwarder
	hashOn       string
	fallbackBase bool
	client       *http.Client
}

type ProxyConfig struct {
	VLLMUrl        string
	BaseModel      string
	CacheManager   *cache.Manager
	Ring           *routing.Ring
	Forwarder      *routing.Forwarder
	HashOn         string
	FallbackToBase bool
}

func New(cfg ProxyConfig) *Proxy {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 300 * time.Second,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
	}
	return &Proxy{
		vllmURL:      cfg.VLLMUrl,
		baseModel:    cfg.BaseModel,
		cacheMgr:     cfg.CacheManager,
		ring:         cfg.Ring,
		forwarder:    cfg.Forwarder,
		hashOn:       cfg.HashOn,
		fallbackBase: cfg.FallbackToBase,
		client:       &http.Client{Transport: transport},
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	info, err := ExtractRequestInfo(r, p.hashOn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	isBaseModel := info.Model == "" || info.Model == p.baseModel

	if isBaseModel && (p.hashOn == "model" || info.HashKey == "") {
		p.proxyToVLLM(w, r, info.Body)
		metrics.RecordRequest(info.Tenant, "passthrough", time.Since(start))
		return
	}

	isForwarded := r.Header.Get(routing.HeaderForwarded) == "true"
	decision := p.forwarder.Decide(info.HashKey, isForwarded)

	if !decision.HandleLocally {
		fwdStart := time.Now()
		resp, err := p.forwarder.ForwardWithFallback(r.Context(), info.HashKey, r.Method, r.URL.Path, info.Body, r.Header)
		if err != nil {
			slog.Warn("all peers failed, handling locally", "err", err)
			decision.HandleLocally = true
		} else {
			metrics.RecordForwardDuration(time.Since(fwdStart))
			metrics.RecordForward(decision.ForwardTo.Addr)
			routing.CopyResponse(w, resp)
			metrics.RecordRequest(info.Tenant, "forwarded", time.Since(start))
			return
		}
	}

	if decision.Overflow {
		metrics.RecordOverflow()
	}

	if !isBaseModel {
		result, err := p.cacheMgr.EnsureAdapter(r.Context(), info.Model)
		if err != nil {
			slog.Error("ensure adapter failed", "adapter", info.Model, "err", err)
			if p.fallbackBase {
				p.proxyToVLLM(w, r, info.Body)
				metrics.RecordRequest(info.Tenant, "fallback-base", time.Since(start))
				return
			}
			http.Error(w, fmt.Sprintf("adapter not available: %v", err), http.StatusServiceUnavailable)
			return
		}

		source := "origin"
		if result.Cached {
			source = "local"
		}

		p.proxyToVLLM(w, r, info.Body)
		metrics.RecordRequest(info.Tenant, source, time.Since(start))
		return
	}

	p.proxyToVLLM(w, r, info.Body)
	metrics.RecordRequest(info.Tenant, "routed", time.Since(start))
}

func (p *Proxy) proxyToVLLM(w http.ResponseWriter, r *http.Request, body []byte) {
	ctx := r.Context()
	url := p.vllmURL + r.URL.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, url, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for k, vals := range r.Header {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	req.Header.Del(routing.HeaderForwarded)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		http.Error(w, fmt.Sprintf("vllm proxy error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		flusher, ok := w.(http.Flusher)
		if ok {
			buf := make([]byte, 4096)
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
					flusher.Flush()
				}
				if readErr != nil {
					break
				}
			}
			return
		}
	}
	io.Copy(w, resp.Body)
}
