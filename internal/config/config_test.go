package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMinimalConfig(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
storage:
  dir: "/tmp/loraplex"
`), 0644)

	cfg, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Listen != ":8080" {
		t.Fatalf("expected default listen :8080, got %s", cfg.Listen)
	}
	if cfg.Routing.VnodesPerPeer != 150 {
		t.Fatalf("expected default vnodes_per_peer 150, got %d", cfg.Routing.VnodesPerPeer)
	}
	if cfg.Routing.OverflowThreshold != 0.8 {
		t.Fatalf("expected default overflow_threshold 0.8, got %f", cfg.Routing.OverflowThreshold)
	}
}

func TestLoadAppliesAllDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
storage:
  dir: "/tmp/loraplex"
`), 0644)

	cfg, err := Load(cfgFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.VLLMUrl != "http://localhost:8000" {
		t.Fatalf("expected default VLLMUrl, got %s", cfg.VLLMUrl)
	}
	if cfg.Storage.MaxSize != "100GB" {
		t.Fatalf("expected default MaxSize 100GB, got %s", cfg.Storage.MaxSize)
	}
	if cfg.Discovery.Mode != "static" {
		t.Fatalf("expected default discovery mode static, got %s", cfg.Discovery.Mode)
	}
	if cfg.VLLM.MaxLoras != 200 {
		t.Fatalf("expected default MaxLoras 200, got %d", cfg.VLLM.MaxLoras)
	}
	if cfg.VLLM.HealthCheckInterval != "10s" {
		t.Fatalf("expected default HealthCheckInterval 10s, got %s", cfg.VLLM.HealthCheckInterval)
	}
	if cfg.Routing.HashOn != "model" {
		t.Fatalf("expected default HashOn model, got %s", cfg.Routing.HashOn)
	}
}

func TestValidationBadDiscoveryMode(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
storage:
  dir: "/tmp/loraplex"
discovery:
  mode: "banana"
`), 0644)

	_, err := Load(cfgFile)
	if err == nil {
		t.Fatal("expected validation error for unknown discovery mode")
	}
}

func TestValidationBadOverflowThreshold(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
storage:
  dir: "/tmp/loraplex"
routing:
  overflow_threshold: 1.5
`), 0644)

	_, err := Load(cfgFile)
	if err == nil {
		t.Fatal("expected validation error for overflow_threshold > 1")
	}
}

func TestLoadNonExistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`{{{invalid`), 0644)

	_, err := Load(cfgFile)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidationHashOn(t *testing.T) {
	valid := []string{"model", "tenant", "tenant/model", "header:X-Region", "header:X-Customer-ID/model"}
	for _, h := range valid {
		t.Run("valid_"+h, func(t *testing.T) {
			if err := validateHashOn(h); err != nil {
				t.Fatalf("expected %q to be valid, got: %v", h, err)
			}
		})
	}

	invalid := []string{"", "banana", "header:", "header:/model"}
	for _, h := range invalid {
		t.Run("invalid_"+h, func(t *testing.T) {
			if err := validateHashOn(h); err == nil {
				t.Fatalf("expected %q to be invalid", h)
			}
		})
	}
}

func TestValidationBadHashOn(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
storage:
  dir: "/tmp/loraplex"
routing:
  hash_on: "banana"
`), 0644)

	_, err := Load(cfgFile)
	if err == nil {
		t.Fatal("expected validation error for invalid hash_on")
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
		err   bool
	}{
		{"megabytes", "10MB", 10 * (1 << 20), false},
		{"gigabytes", "10GB", 10 * (1 << 30), false},
		{"terabytes", "1TB", 1 << 40, false},
		{"single char", "x", 0, true},
		{"unknown unit", "10XB", 0, true},
		{"empty string", "", 0, true},
		{"non-numeric prefix", "abcMB", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSize(tt.input)
			if tt.err && err == nil {
				t.Fatal("expected error")
			}
			if !tt.err && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
