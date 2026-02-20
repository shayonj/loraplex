package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func createFakeAdapter(t *testing.T, base, adapterID string, size int) {
	t.Helper()
	dir := filepath.Join(base, adapterID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	configData := []byte(`{"peft_type": "LORA"}`)
	if err := os.WriteFile(filepath.Join(dir, "adapter_config.json"), configData, 0644); err != nil {
		t.Fatal(err)
	}
	modelData := make([]byte, size)
	if err := os.WriteFile(filepath.Join(dir, "adapter_model.safetensors"), modelData, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestStoreHas(t *testing.T) {
	base := t.TempDir()
	s := NewStore(base, 1<<30)

	if s.Has("nonexistent") {
		t.Fatal("expected Has to return false for missing adapter")
	}

	createFakeAdapter(t, base, "adapter1", 100)
	if !s.Has("adapter1") {
		t.Fatal("expected Has to return true for existing adapter")
	}
}

func TestStoreHasRequiresConfig(t *testing.T) {
	base := t.TempDir()
	s := NewStore(base, 1<<30)

	dir := filepath.Join(base, "incomplete")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "adapter_model.safetensors"), []byte("data"), 0644)

	if s.Has("incomplete") {
		t.Fatal("expected Has to return false without adapter_config.json")
	}
}

func TestStoreTouchAndLRU(t *testing.T) {
	base := t.TempDir()
	s := NewStore(base, 1<<30)
	createFakeAdapter(t, base, "a", 100)

	if err := s.Touch("a"); err != nil {
		t.Fatal(err)
	}
	if s.LRU().Len() != 1 {
		t.Fatalf("expected 1 item in LRU, got %d", s.LRU().Len())
	}
}

func TestStoreMakeRoom(t *testing.T) {
	base := t.TempDir()
	s := NewStore(base, 500)
	createFakeAdapter(t, base, "a", 200)
	createFakeAdapter(t, base, "b", 200)
	s.Touch("a")
	s.Touch("b")

	evicted, err := s.MakeRoom(200)
	if err != nil {
		t.Fatal(err)
	}
	if len(evicted) == 0 {
		t.Fatal("expected at least one eviction")
	}
	if s.Has(evicted[0]) {
		t.Fatal("evicted adapter should be removed from disk")
	}
}

func TestStoreRemove(t *testing.T) {
	base := t.TempDir()
	s := NewStore(base, 1<<30)
	createFakeAdapter(t, base, "removeme", 100)
	s.Touch("removeme")

	if err := s.Remove("removeme"); err != nil {
		t.Fatal(err)
	}
	if s.Has("removeme") {
		t.Fatal("adapter should be removed")
	}
	if s.LRU().Contains("removeme") {
		t.Fatal("adapter should be removed from LRU")
	}
}

func TestStoreRebuild(t *testing.T) {
	base := t.TempDir()
	s := NewStore(base, 1<<30)
	createFakeAdapter(t, base, "x", 50)
	createFakeAdapter(t, base, "y", 100)

	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if s.LRU().Len() != 2 {
		t.Fatalf("expected 2 items after rebuild, got %d", s.LRU().Len())
	}
}

func TestStoreRebuildCreatesDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "nonexistent")
	s := NewStore(base, 1<<30)

	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(base)
	if err != nil {
		t.Fatal("rebuild should create base directory")
	}
	if !info.IsDir() {
		t.Fatal("base should be a directory")
	}
}

func TestStoreHasNestedAdapter(t *testing.T) {
	base := t.TempDir()
	s := NewStore(base, 1<<30)

	createFakeAdapter(t, base, "org/my-adapter", 100)
	if !s.Has("org/my-adapter") {
		t.Fatal("expected Has to return true for nested adapter")
	}
}

func TestStoreRebuildNestedAdapters(t *testing.T) {
	base := t.TempDir()
	s := NewStore(base, 1<<30)
	createFakeAdapter(t, base, "org1/adapter-a", 50)
	createFakeAdapter(t, base, "org2/adapter-b", 100)
	createFakeAdapter(t, base, "flat-adapter", 75)

	if err := s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if s.LRU().Len() != 3 {
		t.Fatalf("expected 3 items after rebuild with nested adapters, got %d", s.LRU().Len())
	}
	if !s.LRU().Contains("org1/adapter-a") {
		t.Fatal("expected org1/adapter-a in LRU")
	}
	if !s.LRU().Contains("org2/adapter-b") {
		t.Fatal("expected org2/adapter-b in LRU")
	}
}

func TestStoreAdapterPath(t *testing.T) {
	s := NewStore("/tmp/test", 1<<30)
	p := s.AdapterPath("org/model")
	if p != "/tmp/test/org/model" {
		t.Fatalf("unexpected path: %s", p)
	}
}

func TestValidateAdapterIDBlocks(t *testing.T) {
	s := NewStore("/tmp/cache", 1<<30)

	bad := []string{
		"../../etc/passwd",
		"../escape",
		"foo/../../etc/shadow",
	}
	for _, id := range bad {
		if err := s.ValidateAdapterID(id); err == nil {
			t.Fatalf("expected error for adapter ID %q", id)
		}
	}
}

func TestValidateAdapterIDAllows(t *testing.T) {
	s := NewStore("/tmp/cache", 1<<30)

	good := []string{
		"my-adapter",
		"org/adapter-name",
		"org/sub/adapter",
	}
	for _, id := range good {
		if err := s.ValidateAdapterID(id); err != nil {
			t.Fatalf("unexpected error for adapter ID %q: %v", id, err)
		}
	}
}
