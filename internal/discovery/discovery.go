package discovery

import (
	"context"
	"fmt"
	"sync"

	"github.com/shayonj/loraplex/internal/config"
)

type OnChangeFunc func(peers []string)

type Discovery interface {
	Start(ctx context.Context) error
	Stop()
	Peers() []string
	OnChange(fn OnChangeFunc)
}

type Base struct {
	mu        sync.RWMutex
	peers     []string
	callbacks []OnChangeFunc
}

func (b *Base) Peers() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, len(b.peers))
	copy(out, b.peers)
	return out
}

func (b *Base) OnChange(fn OnChangeFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.callbacks = append(b.callbacks, fn)
}

func (b *Base) SetPeers(peers []string) {
	b.mu.Lock()
	b.peers = peers
	cbs := make([]OnChangeFunc, len(b.callbacks))
	copy(cbs, b.callbacks)
	b.mu.Unlock()

	for _, cb := range cbs {
		cb(peers)
	}
}

func New(cfg config.DiscoveryConfig, selfAddr string) (Discovery, error) {
	switch cfg.Mode {
	case "static":
		return NewStatic(cfg.Static.Peers), nil
	case "file":
		return NewFile(cfg.File, selfAddr)
	case "k8s":
		return NewK8s(cfg.K8s)
	default:
		return nil, fmt.Errorf("unknown discovery mode: %s", cfg.Mode)
	}
}
