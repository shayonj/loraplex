package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type mockOrigin struct {
	name       string
	adapters   map[string]bool
	fetchCount int
	mu         sync.Mutex
	etag       string
}

func newMockOrigin(name string, adapters ...string) *mockOrigin {
	m := &mockOrigin{name: name, adapters: make(map[string]bool), etag: "\"v1\""}
	for _, a := range adapters {
		m.adapters[a] = true
	}
	return m
}

func (m *mockOrigin) Name() string { return m.name }

func (m *mockOrigin) Fetch(_ context.Context, adapterID string, destDir string) (*FetchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.adapters[adapterID] {
		return nil, fmt.Errorf("not found: %s", adapterID)
	}
	m.fetchCount++

	os.WriteFile(filepath.Join(destDir, "adapter_config.json"), []byte(`{"peft_type":"LORA"}`), 0644)
	os.WriteFile(filepath.Join(destDir, "adapter_model.safetensors"), make([]byte, 100), 0644)

	return &FetchResult{Origin: m.name, ETag: m.etag}, nil
}

func (m *mockOrigin) Head(_ context.Context, adapterID string) (*FetchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.adapters[adapterID] {
		return nil, fmt.Errorf("not found: %s", adapterID)
	}
	return &FetchResult{Origin: m.name, ETag: m.etag}, nil
}

func (m *mockOrigin) FetchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fetchCount
}

func setupManager(t *testing.T, maxSize int64, origins ...OriginFetcher) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	store := NewStore(dir, maxSize)
	mgr := NewManager(store, origins, time.Hour)
	mgr.Rebuild()
	return mgr, dir
}

func TestManagerEnsureFromOrigin(t *testing.T) {
	origin := newMockOrigin("test", "adapter1")
	mgr, dir := setupManager(t, 1<<20, origin)

	result, err := mgr.EnsureAdapter(context.Background(), "adapter1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Cached {
		t.Fatal("first fetch should not be cached")
	}

	configPath := filepath.Join(dir, "adapter1", "adapter_config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("adapter_config.json should exist in store: %v", err)
	}
}

func TestManagerEnsureCacheHit(t *testing.T) {
	origin := newMockOrigin("test", "adapter1")
	mgr, _ := setupManager(t, 1<<20, origin)

	mgr.EnsureAdapter(context.Background(), "adapter1")
	result, err := mgr.EnsureAdapter(context.Background(), "adapter1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cached {
		t.Fatal("second call should be cached")
	}
	if origin.FetchCount() != 1 {
		t.Fatalf("expected 1 fetch, got %d", origin.FetchCount())
	}
}

func TestManagerEnsureNotFound(t *testing.T) {
	origin := newMockOrigin("test")
	mgr, _ := setupManager(t, 1<<20, origin)

	_, err := mgr.EnsureAdapter(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing adapter")
	}
}

func TestManagerEvictAdapter(t *testing.T) {
	origin := newMockOrigin("test", "adapter1")
	mgr, dir := setupManager(t, 1<<20, origin)

	mgr.EnsureAdapter(context.Background(), "adapter1")
	mgr.EvictAdapter("adapter1")

	if _, err := os.Stat(filepath.Join(dir, "adapter1")); !os.IsNotExist(err) {
		t.Fatal("adapter should be removed after eviction")
	}
}

func TestManagerSingleflight(t *testing.T) {
	origin := newMockOrigin("test", "adapter1")
	mgr, _ := setupManager(t, 1<<20, origin)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.EnsureAdapter(context.Background(), "adapter1")
		}()
	}
	wg.Wait()

	if origin.FetchCount() != 1 {
		t.Fatalf("expected 1 fetch via singleflight, got %d", origin.FetchCount())
	}
}

func TestManagerMultipleOriginFallthrough(t *testing.T) {
	empty := newMockOrigin("empty")
	has := newMockOrigin("has", "adapter1")
	mgr, _ := setupManager(t, 1<<20, empty, has)

	result, err := mgr.EnsureAdapter(context.Background(), "adapter1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Cached {
		t.Fatal("first fetch should not be cached")
	}
}
