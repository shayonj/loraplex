package routing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDecideHandleLocallyWhenForwarded(t *testing.T) {
	ring := NewRing(100, "self:8080")
	ring.Update([]string{"self:8080", "peer1:8080"})
	f := NewForwarder(ring, 5*time.Second, 0.8)

	d := f.Decide("some-key", true)
	if !d.HandleLocally {
		t.Fatal("expected HandleLocally when request is already forwarded")
	}
	if d.ForwardTo != nil {
		t.Fatal("expected nil ForwardTo when handling locally")
	}
}

func TestDecideForwardToOwner(t *testing.T) {
	ring := NewRing(100, "self:8080")
	ring.Update([]string{"self:8080", "peer1:8080"})
	f := NewForwarder(ring, 5*time.Second, 0.8)

	var d *RouteDecision
	for i := 0; i < 10000; i++ {
		d = f.Decide(fmt.Sprintf("key-%d", i), false)
		if d.ForwardTo != nil {
			break
		}
	}

	if d == nil || d.ForwardTo == nil {
		t.Fatal("expected at least one key to forward to a remote peer")
	}
	if d.HandleLocally {
		t.Fatal("expected HandleLocally=false when forwarding")
	}
	if d.Overflow {
		t.Fatal("expected Overflow=false for low-load peer")
	}
}

func TestDecideOverflowWhenLoadHigh(t *testing.T) {
	ring := NewRing(100, "self:8080")
	ring.Update([]string{"self:8080", "peer1:8080"})
	ring.UpdatePeerLoad("peer1:8080", 0.9)
	f := NewForwarder(ring, 5*time.Second, 0.8)

	var d *RouteDecision
	for i := 0; i < 10000; i++ {
		d = f.Decide(fmt.Sprintf("key-%d", i), false)
		if d.Overflow {
			break
		}
	}

	if d == nil || !d.Overflow {
		t.Fatal("expected overflow decision for overloaded peer")
	}
	if !d.HandleLocally {
		t.Fatal("expected HandleLocally=true on overflow")
	}
}

func TestDecideHandleLocallyWhenOwnerIsSelf(t *testing.T) {
	ring := NewRing(100, "self:8080")
	ring.Update([]string{"self:8080"})
	f := NewForwarder(ring, 5*time.Second, 0.8)

	d := f.Decide("any-key", false)
	if !d.HandleLocally {
		t.Fatal("expected HandleLocally when owner is self")
	}
}

func TestForwardSendsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderForwarded) != "true" {
			t.Error("expected X-Loraplex-Forwarded header")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	ring := NewRing(100, "self:8080")
	ring.Update([]string{"self:8080", addr})
	f := NewForwarder(ring, 5*time.Second, 0.8)

	resp, err := f.Forward(context.Background(), &Peer{Addr: addr}, "POST", "/v1/completions", []byte(`{"model":"x"}`), http.Header{})
	if err != nil {
		t.Fatalf("forward error: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("expected body 'ok', got %q", string(body))
	}
}

func TestForwardWithFallbackOnPrimaryFailure(t *testing.T) {
	fallbackHit := false
	fallbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fallback-ok"))
	}))
	defer fallbackSrv.Close()

	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	primarySrv.Close()

	primaryAddr := strings.TrimPrefix(primarySrv.URL, "http://")
	fallbackAddr := strings.TrimPrefix(fallbackSrv.URL, "http://")

	ring := NewRing(100, "other:9999")
	ring.Update([]string{primaryAddr, fallbackAddr})
	f := NewForwarder(ring, 2*time.Second, 0.8)

	var hashKey string
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		if ring.Owner(key).Addr == primaryAddr {
			hashKey = key
			break
		}
	}
	if hashKey == "" {
		t.Fatal("could not find a key owned by the primary peer")
	}

	resp, err := f.ForwardWithFallback(context.Background(), hashKey, "POST", "/v1/completions", []byte(`{}`), http.Header{})
	if err != nil {
		t.Fatalf("fallback should succeed: %v", err)
	}
	defer resp.Body.Close()

	if !fallbackHit {
		t.Fatal("expected fallback server to be called")
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "fallback-ok" {
		t.Fatalf("expected body 'fallback-ok', got %q", string(body))
	}
}

func TestForwardWithFallbackSkipsSelf(t *testing.T) {
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	primarySrv.Close()
	primaryAddr := strings.TrimPrefix(primarySrv.URL, "http://")

	ring := NewRing(100, "self:8080")
	ring.Update([]string{primaryAddr, "self:8080"})
	f := NewForwarder(ring, 2*time.Second, 0.8)

	var hashKey string
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		owner := ring.Owner(key)
		if owner.Addr == primaryAddr {
			fb := ring.Fallback(key, primaryAddr)
			if fb != nil && fb.Addr == "self:8080" {
				hashKey = key
				break
			}
		}
	}
	if hashKey == "" {
		t.Fatal("could not find a key where primary is remote and fallback is self")
	}

	_, err := f.ForwardWithFallback(context.Background(), hashKey, "POST", "/v1/completions", []byte(`{}`), http.Header{})
	if err == nil {
		t.Fatal("expected error when fallback is self")
	}
	if !strings.Contains(err.Error(), "no remote fallback") {
		t.Fatalf("expected 'no remote fallback' in error, got: %v", err)
	}
}

func TestForwardWithFallbackNoPeers(t *testing.T) {
	ring := NewRing(100, "self:8080")
	f := NewForwarder(ring, 2*time.Second, 0.8)

	_, err := f.ForwardWithFallback(context.Background(), "any-key", "POST", "/v1/completions", []byte(`{}`), http.Header{})
	if err == nil {
		t.Fatal("expected error on empty ring")
	}
	if !strings.Contains(err.Error(), "no peer available") {
		t.Fatalf("expected 'no peer available' in error, got: %v", err)
	}
}
