package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shayonj/loraplex/internal/config"
)

func TestStaticPeers(t *testing.T) {
	peers := []string{"10.0.0.1:9090", "10.0.0.2:9090"}
	s := NewStatic(peers)

	var got []string
	s.OnChange(func(p []string) { got = p })

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if len(got) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(got))
	}
	if got[0] != "10.0.0.1:9090" || got[1] != "10.0.0.2:9090" {
		t.Fatalf("unexpected peers: %v", got)
	}

	p := s.Peers()
	if len(p) != 2 {
		t.Fatalf("Peers() returned %d, want 2", len(p))
	}
}

func TestStaticPeersCopied(t *testing.T) {
	peers := []string{"a:1", "b:2"}
	s := NewStatic(peers)
	s.Start(context.Background())
	defer s.Stop()

	got := s.Peers()
	got[0] = "mutated"
	if s.Peers()[0] == "mutated" {
		t.Fatal("Peers() returned a reference instead of a copy")
	}
}

func TestBaseSetPeersCallbacks(t *testing.T) {
	var b Base
	calls := 0
	b.OnChange(func(peers []string) { calls++ })
	b.OnChange(func(peers []string) { calls++ })
	b.SetPeers([]string{"a"})

	if calls != 2 {
		t.Fatalf("expected 2 callbacks, got %d", calls)
	}
}

func TestFileDiscovery(t *testing.T) {
	dir := t.TempDir()

	cfg := config.FileDiscovery{
		Dir:               dir,
		HeartbeatInterval: "100ms",
		StaleThreshold:    "500ms",
	}

	f, err := NewFile(cfg, "10.0.0.1:9090")
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan []string, 10)
	f.OnChange(func(peers []string) { ch <- peers })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer f.Stop()

	select {
	case got := <-ch:
		if len(got) != 1 || got[0] != "10.0.0.1:9090" {
			t.Fatalf("expected self peer, got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for peers")
	}

	hbFile := filepath.Join(dir, "10.0.0.1:9090")
	if _, err := os.Stat(hbFile); err != nil {
		t.Fatalf("heartbeat file not created: %v", err)
	}
}

func TestFileDiscoveryUsesSelfAddr(t *testing.T) {
	dir := t.TempDir()

	cfg := config.FileDiscovery{
		Dir:               dir,
		HeartbeatInterval: "100ms",
		StaleThreshold:    "500ms",
	}

	f, err := NewFile(cfg, "custom-addr:9090")
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan []string, 10)
	f.OnChange(func(peers []string) { ch <- peers })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f.Start(ctx)
	defer f.Stop()

	select {
	case got := <-ch:
		if len(got) != 1 || got[0] != "custom-addr:9090" {
			t.Fatalf("expected custom-addr:9090, got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for peers")
	}
}

func TestFileDiscoveryStalePeerRemoved(t *testing.T) {
	dir := t.TempDir()

	staleFile := filepath.Join(dir, "stale-peer:9090")
	os.WriteFile(staleFile, []byte("stale-peer:9090\n0"), 0644)

	old := time.Now().Add(-2 * time.Second)
	os.Chtimes(staleFile, old, old)

	cfg := config.FileDiscovery{
		Dir:               dir,
		HeartbeatInterval: "100ms",
		StaleThreshold:    "500ms",
	}

	f, err := NewFile(cfg, "self:9090")
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan []string, 10)
	f.OnChange(func(peers []string) { ch <- peers })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Start(ctx)
	defer f.Stop()

	select {
	case got := <-ch:
		for _, p := range got {
			if p == "stale-peer:9090" {
				t.Fatal("stale peer should have been removed")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for peers")
	}
}

func TestDoubleStopFile(t *testing.T) {
	dir := t.TempDir()
	cfg := config.FileDiscovery{
		Dir:               dir,
		HeartbeatInterval: "1s",
		StaleThreshold:    "5s",
	}
	f, err := NewFile(cfg, "self:9090")
	if err != nil {
		t.Fatal(err)
	}
	f.Start(context.Background())
	f.Stop()
	f.Stop()
}

func TestDoubleStopK8s(t *testing.T) {
	k := &K8s{
		stopCh: make(chan struct{}),
	}
	k.Stop()
	k.Stop()
}
