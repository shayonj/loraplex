package cache

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Store struct {
	BasePath string
	lru      *LRU
}

func NewStore(basePath string, maxSize int64) *Store {
	return &Store{
		BasePath: basePath,
		lru:      NewLRU(maxSize),
	}
}

func (s *Store) AdapterPath(adapterID string) string {
	return filepath.Join(s.BasePath, adapterID)
}

func (s *Store) ValidateAdapterID(adapterID string) error {
	resolved := filepath.Clean(filepath.Join(s.BasePath, adapterID))
	base := filepath.Clean(s.BasePath) + string(filepath.Separator)
	if !strings.HasPrefix(resolved, base) && resolved != filepath.Clean(s.BasePath) {
		return fmt.Errorf("invalid adapter ID: path traversal detected")
	}
	return nil
}

func (s *Store) Has(adapterID string) bool {
	p := s.AdapterPath(adapterID)
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(p, "adapter_config.json"))
	return err == nil
}

func (s *Store) Touch(adapterID string) error {
	size, err := s.dirSize(s.AdapterPath(adapterID))
	if err != nil {
		return err
	}
	s.lru.Touch(adapterID, size)
	return nil
}

func (s *Store) MakeRoom(requiredBytes int64) ([]string, error) {
	evicted := s.lru.Evict(requiredBytes)
	for _, id := range evicted {
		if err := os.RemoveAll(s.AdapterPath(id)); err != nil {
			return evicted, fmt.Errorf("remove evicted adapter %s: %w", id, err)
		}
	}
	return evicted, nil
}

func (s *Store) Remove(adapterID string) error {
	s.lru.Remove(adapterID)
	return os.RemoveAll(s.AdapterPath(adapterID))
}

func (s *Store) LRU() *LRU {
	return s.lru
}

func (s *Store) Rebuild() error {
	if err := os.MkdirAll(s.BasePath, 0755); err != nil {
		return err
	}

	type adapterInfo struct {
		id   string
		size int64
		mod  int64
	}
	var adapters []adapterInfo

	filepath.WalkDir(s.BasePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == "adapter_config.json" && !d.IsDir() {
			adapterDir := filepath.Dir(path)
			rel, relErr := filepath.Rel(s.BasePath, adapterDir)
			if relErr != nil || strings.HasPrefix(rel, ".tmp") {
				return nil
			}
			size, sizeErr := s.dirSize(adapterDir)
			if sizeErr != nil {
				return nil
			}
			info, _ := d.Info()
			var mod int64
			if info != nil {
				mod = info.ModTime().UnixNano()
			}
			adapters = append(adapters, adapterInfo{id: rel, size: size, mod: mod})
			return filepath.SkipDir
		}
		return nil
	})

	sort.Slice(adapters, func(i, j int) bool {
		return adapters[i].mod < adapters[j].mod
	})

	for _, a := range adapters {
		s.lru.Touch(a.id, a.size)
	}
	return nil
}

func (s *Store) dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
