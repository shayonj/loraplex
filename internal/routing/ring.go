package routing

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"sync"
)

type Peer struct {
	Addr string
	Load float64
}

type vnode struct {
	hash uint32
	peer string
}

type Ring struct {
	mu            sync.RWMutex
	vnodes        []vnode
	peers         map[string]*Peer
	vnodesPerPeer int
	self          string
}

func NewRing(vnodesPerPeer int, selfAddr string) *Ring {
	return &Ring{
		peers:         make(map[string]*Peer),
		vnodesPerPeer: vnodesPerPeer,
		self:          selfAddr,
	}
}

func (r *Ring) Update(addrs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.peers = make(map[string]*Peer, len(addrs))
	r.vnodes = make([]vnode, 0, len(addrs)*r.vnodesPerPeer)

	for _, addr := range addrs {
		r.peers[addr] = &Peer{Addr: addr}
		for i := range r.vnodesPerPeer {
			h := hashKey(addr, i)
			r.vnodes = append(r.vnodes, vnode{hash: h, peer: addr})
		}
	}

	sort.Slice(r.vnodes, func(i, j int) bool {
		return r.vnodes[i].hash < r.vnodes[j].hash
	})
}

// Owner returns the peer responsible for the given key.
func (r *Ring) Owner(key string) *Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.vnodes) == 0 {
		return nil
	}

	h := hashString(key)
	idx := sort.Search(len(r.vnodes), func(i int) bool {
		return r.vnodes[i].hash >= h
	})
	if idx == len(r.vnodes) {
		idx = 0
	}
	addr := r.vnodes[idx].peer
	return r.peers[addr]
}

// Fallback returns the next peer on the ring after the given skip peer.
func (r *Ring) Fallback(key string, skip string) *Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.vnodes) == 0 {
		return nil
	}

	h := hashString(key)
	idx := sort.Search(len(r.vnodes), func(i int) bool {
		return r.vnodes[i].hash >= h
	})

	seen := 0
	for seen < len(r.vnodes) {
		i := (idx + seen) % len(r.vnodes)
		addr := r.vnodes[i].peer
		if addr != skip {
			return r.peers[addr]
		}
		seen++
	}
	return nil
}

func (r *Ring) IsSelf(peer *Peer) bool {
	if peer == nil {
		return true
	}
	return peer.Addr == r.self
}

func (r *Ring) Self() string {
	return r.self
}

func (r *Ring) PeerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.peers)
}

func (r *Ring) Peers() []*Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, p)
	}
	return out
}

func (r *Ring) UpdatePeerLoad(addr string, load float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.peers[addr]; ok {
		p.Load = load
	}
}

func hashKey(addr string, idx int) uint32 {
	h := sha256.New()
	h.Write([]byte(addr))
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(idx))
	h.Write(b)
	sum := h.Sum(nil)
	return binary.BigEndian.Uint32(sum[:4])
}

func hashString(s string) uint32 {
	h := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint32(h[:4])
}
