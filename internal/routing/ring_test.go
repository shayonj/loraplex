package routing

import (
	"fmt"
	"testing"
)

func TestOwnerConsistency(t *testing.T) {
	r := NewRing(100, "self:8080")
	r.Update([]string{"peer1:8080", "peer2:8080", "peer3:8080"})

	keys := []string{"model-a", "model-b", "model-c", "user/adapter-1"}
	for _, key := range keys {
		first := r.Owner(key)
		for i := 0; i < 50; i++ {
			got := r.Owner(key)
			if got.Addr != first.Addr {
				t.Fatalf("Owner(%q) inconsistent: got %s, want %s", key, got.Addr, first.Addr)
			}
		}
	}
}

func TestOwnerEmptyRing(t *testing.T) {
	r := NewRing(100, "self:8080")
	if r.Owner("anything") != nil {
		t.Fatal("expected nil owner on empty ring")
	}
}

func TestFallbackReturnsDifferentPeer(t *testing.T) {
	r := NewRing(100, "self:8080")
	r.Update([]string{"peer1:8080", "peer2:8080", "peer3:8080"})

	key := "test-key"
	owner := r.Owner(key)
	fallback := r.Fallback(key, owner.Addr)

	if fallback == nil {
		t.Fatal("expected non-nil fallback")
	}
	if fallback.Addr == owner.Addr {
		t.Fatalf("fallback should differ from owner: both are %s", owner.Addr)
	}
}

func TestFallbackSinglePeerReturnsNil(t *testing.T) {
	r := NewRing(100, "self:8080")
	r.Update([]string{"peer1:8080"})

	fb := r.Fallback("key", "peer1:8080")
	if fb != nil {
		t.Fatal("expected nil fallback when only peer is skipped")
	}
}

func TestFallbackEmptyRing(t *testing.T) {
	r := NewRing(100, "self:8080")
	fb := r.Fallback("key", "peer1:8080")
	if fb != nil {
		t.Fatal("expected nil fallback on empty ring")
	}
}

func TestUpdateReplacesPeers(t *testing.T) {
	r := NewRing(100, "self:8080")
	r.Update([]string{"peer1:8080", "peer2:8080"})
	if r.PeerCount() != 2 {
		t.Fatalf("expected 2 peers, got %d", r.PeerCount())
	}

	r.Update([]string{"peer3:8080", "peer4:8080", "peer5:8080"})
	if r.PeerCount() != 3 {
		t.Fatalf("expected 3 peers after update, got %d", r.PeerCount())
	}

	found := map[string]bool{}
	for i := 0; i < 10000; i++ {
		owner := r.Owner(fmt.Sprintf("key-%d", i))
		found[owner.Addr] = true
	}
	for _, addr := range []string{"peer3:8080", "peer4:8080", "peer5:8080"} {
		if !found[addr] {
			t.Fatalf("new peer %s never appeared as owner", addr)
		}
	}
	for _, addr := range []string{"peer1:8080", "peer2:8080"} {
		if found[addr] {
			t.Fatalf("old peer %s still appears as owner after update", addr)
		}
	}
}

func TestIsSelf(t *testing.T) {
	r := NewRing(100, "self:8080")

	tests := []struct {
		name string
		peer *Peer
		want bool
	}{
		{"nil peer", nil, true},
		{"self address", &Peer{Addr: "self:8080"}, true},
		{"different peer", &Peer{Addr: "other:8080"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.IsSelf(tt.peer); got != tt.want {
				t.Fatalf("IsSelf() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdatePeerLoad(t *testing.T) {
	r := NewRing(100, "self:8080")
	r.Update([]string{"peer1:8080", "peer2:8080"})

	r.UpdatePeerLoad("peer1:8080", 0.75)

	for _, p := range r.Peers() {
		if p.Addr == "peer1:8080" && p.Load != 0.75 {
			t.Fatalf("expected load 0.75, got %f", p.Load)
		}
	}
}

func TestUpdatePeerLoadNonExistent(t *testing.T) {
	r := NewRing(100, "self:8080")
	r.Update([]string{"peer1:8080"})
	r.UpdatePeerLoad("nonexistent:8080", 0.5)
}

func TestVnodeDistribution(t *testing.T) {
	r := NewRing(150, "self:8080")
	peers := []string{"peer1:8080", "peer2:8080", "peer3:8080"}
	r.Update(peers)

	counts := map[string]int{}
	total := 10000
	for i := 0; i < total; i++ {
		owner := r.Owner(fmt.Sprintf("model-%d", i))
		counts[owner.Addr]++
	}

	for _, addr := range peers {
		pct := float64(counts[addr]) / float64(total)
		if pct < 0.15 || pct > 0.55 {
			t.Fatalf("peer %s got %.1f%% of keys; distribution too skewed", addr, pct*100)
		}
	}
}
