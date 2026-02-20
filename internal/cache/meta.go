package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const MetaFileName = ".loraplex_meta"

type AdapterMeta struct {
	Origin     string    `json:"origin"`
	ETag       string    `json:"etag,omitempty"`
	Revision   string    `json:"revision,omitempty"`
	FetchedAt  time.Time `json:"fetched_at"`
	TTLSeconds int       `json:"ttl_seconds"`
}

func (m *AdapterMeta) IsExpired() bool {
	if m.TTLSeconds <= 0 {
		return false
	}
	return time.Since(m.FetchedAt) > time.Duration(m.TTLSeconds)*time.Second
}

func (m *AdapterMeta) ContentID() string {
	if m.ETag != "" {
		return m.ETag
	}
	return m.Revision
}

func ReadMeta(adapterDir string) (*AdapterMeta, error) {
	data, err := os.ReadFile(filepath.Join(adapterDir, MetaFileName))
	if err != nil {
		return nil, err
	}
	meta := &AdapterMeta{}
	if err := json.Unmarshal(data, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func WriteMeta(adapterDir string, meta *AdapterMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(adapterDir, MetaFileName), data, 0644)
}
