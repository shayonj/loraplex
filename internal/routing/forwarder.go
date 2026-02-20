package routing

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const HeaderForwarded = "X-Loraplex-Forwarded"

type Forwarder struct {
	client    *http.Client
	ring      *Ring
	threshold float64
}

func NewForwarder(ring *Ring, timeout time.Duration, overflowThreshold float64) *Forwarder {
	return &Forwarder{
		client: &http.Client{
			Timeout: timeout,
		},
		ring:      ring,
		threshold: overflowThreshold,
	}
}

type RouteDecision struct {
	HandleLocally bool
	ForwardTo     *Peer
	Overflow      bool
}

// Decide determines whether a request should be handled locally or forwarded.
func (f *Forwarder) Decide(hashKey string, isForwarded bool) *RouteDecision {
	if isForwarded {
		return &RouteDecision{HandleLocally: true}
	}

	owner := f.ring.Owner(hashKey)
	if owner == nil || f.ring.IsSelf(owner) {
		return &RouteDecision{HandleLocally: true}
	}

	if owner.Load >= f.threshold {
		return &RouteDecision{HandleLocally: true, Overflow: true}
	}

	return &RouteDecision{ForwardTo: owner}
}

// Forward sends a request to the target peer's loraplex.
func (f *Forwarder) Forward(ctx context.Context, peer *Peer, method, path string, body []byte, headers http.Header) (*http.Response, error) {
	url := fmt.Sprintf("http://%s%s", peer.Addr, path)
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set(HeaderForwarded, "true")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("forward to %s: %w", peer.Addr, err)
	}
	return resp, nil
}

// ForwardWithFallback tries the primary peer, then walks the ring on failure.
func (f *Forwarder) ForwardWithFallback(ctx context.Context, hashKey string, method, path string, body []byte, headers http.Header) (*http.Response, error) {
	owner := f.ring.Owner(hashKey)
	if owner == nil {
		return nil, fmt.Errorf("no peer available")
	}

	resp, err := f.Forward(ctx, owner, method, path, body, headers)
	if err == nil {
		return resp, nil
	}

	fallback := f.ring.Fallback(hashKey, owner.Addr)
	if fallback == nil || f.ring.IsSelf(fallback) {
		return nil, fmt.Errorf("owner %s failed, no remote fallback: %w", owner.Addr, err)
	}

	return f.Forward(ctx, fallback, method, path, body, headers)
}

// CopyResponse writes the forwarded response back to the client.
func CopyResponse(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
