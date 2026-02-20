package cache

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shayonj/loraplex/internal/metrics"
	"golang.org/x/sync/singleflight"
)

type FetchResult struct {
	Origin   string
	ETag     string
	Revision string
}

type OriginFetcher interface {
	Name() string
	Fetch(ctx context.Context, adapterID string, destDir string) (*FetchResult, error)
	Head(ctx context.Context, adapterID string) (*FetchResult, error)
}

type EnsureResult struct {
	Path   string
	Cached bool
}

type Manager struct {
	store   *Store
	origins []OriginFetcher
	ttl     time.Duration

	group singleflight.Group

	revalMu      sync.Mutex
	revalidating map[string]bool
}

func NewManager(store *Store, origins []OriginFetcher, ttl time.Duration) *Manager {
	return &Manager{
		store:        store,
		origins:      origins,
		ttl:          ttl,
		revalidating: make(map[string]bool),
	}
}

func (m *Manager) Rebuild() error {
	if err := m.store.Rebuild(); err != nil {
		return fmt.Errorf("rebuild storage: %w", err)
	}
	return nil
}

func (m *Manager) EnsureAdapter(ctx context.Context, adapterID string) (*EnsureResult, error) {
	if err := m.store.ValidateAdapterID(adapterID); err != nil {
		return nil, err
	}
	v, err, _ := m.group.Do(adapterID, func() (any, error) {
		return m.ensureInternal(ctx, adapterID)
	})
	if err != nil {
		return nil, err
	}
	return v.(*EnsureResult), nil
}

func (m *Manager) ensureInternal(ctx context.Context, adapterID string) (*EnsureResult, error) {
	if m.store.Has(adapterID) {
		if err := m.store.Touch(adapterID); err != nil {
			return nil, err
		}
		m.maybeRevalidate(adapterID)
		metrics.RecordCacheHit("local")
		return &EnsureResult{
			Path:   m.store.AdapterPath(adapterID),
			Cached: true,
		}, nil
	}

	if err := m.fetchFromOrigin(ctx, adapterID); err != nil {
		return nil, err
	}

	metrics.RecordCacheHit("origin")
	return &EnsureResult{
		Path:   m.store.AdapterPath(adapterID),
		Cached: false,
	}, nil
}

func (m *Manager) fetchFromOrigin(ctx context.Context, adapterID string) error {
	safeID := strings.ReplaceAll(adapterID, "/", "__")
	tmpDir := filepath.Join(m.store.BasePath, ".tmp-"+safeID)
	os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("create tmp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	var fr *FetchResult
	for _, origin := range m.origins {
		result, err := origin.Fetch(ctx, adapterID, tmpDir)
		if err != nil {
			slog.Warn("origin fetch failed", "origin", origin.Name(), "adapter", adapterID, "err", err)
			continue
		}
		fr = result
		break
	}
	if fr == nil {
		return fmt.Errorf("adapter %s not found in any origin", adapterID)
	}

	meta := &AdapterMeta{
		Origin:     fr.Origin,
		ETag:       fr.ETag,
		Revision:   fr.Revision,
		FetchedAt:  time.Now(),
		TTLSeconds: int(m.ttl.Seconds()),
	}
	if err := WriteMeta(tmpDir, meta); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}

	size, err := m.store.dirSize(tmpDir)
	if err != nil {
		return err
	}
	if evicted, err := m.store.MakeRoom(size); err != nil {
		return err
	} else if len(evicted) > 0 {
		metrics.RecordEviction("local", len(evicted))
	}

	dst := m.store.AdapterPath(adapterID)
	os.RemoveAll(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("create adapter parent dir: %w", err)
	}
	if err := os.Rename(tmpDir, dst); err != nil {
		return fmt.Errorf("move adapter to store: %w", err)
	}
	if err := m.store.Touch(adapterID); err != nil {
		return err
	}
	return nil
}

func (m *Manager) maybeRevalidate(adapterID string) {
	if m.ttl <= 0 || len(m.origins) == 0 {
		return
	}

	adapterPath := m.store.AdapterPath(adapterID)
	meta, err := ReadMeta(adapterPath)
	if err != nil || !meta.IsExpired() {
		return
	}

	m.revalMu.Lock()
	if m.revalidating[adapterID] {
		m.revalMu.Unlock()
		return
	}
	m.revalidating[adapterID] = true
	m.revalMu.Unlock()

	go func() {
		defer func() {
			m.revalMu.Lock()
			delete(m.revalidating, adapterID)
			m.revalMu.Unlock()
		}()
		m.revalidateAdapter(adapterID, meta)
	}()
}

func (m *Manager) revalidateAdapter(adapterID string, oldMeta *AdapterMeta) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, origin := range m.origins {
		headResult, err := origin.Head(ctx, adapterID)
		if err != nil {
			continue
		}

		newContentID := headResult.ETag
		if newContentID == "" {
			newContentID = headResult.Revision
		}
		oldContentID := oldMeta.ContentID()

		if newContentID != "" && oldContentID != "" && newContentID == oldContentID {
			refreshMeta := *oldMeta
			refreshMeta.FetchedAt = time.Now()
			WriteMeta(m.store.AdapterPath(adapterID), &refreshMeta)
			slog.Debug("revalidation: adapter unchanged", "adapter", adapterID)
			return
		}

		slog.Info("revalidation: adapter changed, refetching", "adapter", adapterID)
		m.store.Remove(adapterID)
		if err := m.fetchFromOrigin(ctx, adapterID); err != nil {
			slog.Error("revalidation: refetch failed", "adapter", adapterID, "err", err)
		}
		return
	}
}

func (m *Manager) EvictAdapter(adapterID string) error {
	if err := m.store.ValidateAdapterID(adapterID); err != nil {
		return err
	}
	return m.store.Remove(adapterID)
}

func (m *Manager) Store() *Store { return m.store }
