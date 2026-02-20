package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen       string             `yaml:"listen"`
	SelfAddr     string             `yaml:"self_addr"`
	VLLMUrl      string             `yaml:"vllm_url"`
	Storage      StorageConfig      `yaml:"storage"`
	Origins      []OriginConfig     `yaml:"origins"`
	Invalidation InvalidationConfig `yaml:"invalidation"`
	Discovery    DiscoveryConfig    `yaml:"discovery"`
	Routing      RoutingConfig      `yaml:"routing"`
	VLLM         VLLMConfig         `yaml:"vllm"`
}

type StorageConfig struct {
	Dir     string `yaml:"dir"`
	MaxSize string `yaml:"max_size"`
}

type OriginConfig struct {
	Type    string `yaml:"type"`
	Bucket  string `yaml:"bucket,omitempty"`
	Region  string `yaml:"region,omitempty"`
	Prefix  string `yaml:"prefix,omitempty"`
	Token   string `yaml:"token,omitempty"`
	BaseURL string `yaml:"base_url,omitempty"`
}

type InvalidationConfig struct {
	TTL string `yaml:"ttl"`
}

type DiscoveryConfig struct {
	Mode   string          `yaml:"mode"`
	K8s    K8sDiscovery    `yaml:"k8s"`
	File   FileDiscovery   `yaml:"file"`
	Static StaticDiscovery `yaml:"static"`
}

type K8sDiscovery struct {
	Service   string `yaml:"service"`
	Namespace string `yaml:"namespace"`
}

type FileDiscovery struct {
	Dir               string `yaml:"dir"`
	HeartbeatInterval string `yaml:"heartbeat_interval"`
	StaleThreshold    string `yaml:"stale_threshold"`
}

type StaticDiscovery struct {
	Peers []string `yaml:"peers"`
}

type RoutingConfig struct {
	HashOn              string  `yaml:"hash_on"`
	VnodesPerPeer       int     `yaml:"vnodes_per_peer"`
	ForwardTimeout      string  `yaml:"forward_timeout"`
	OverflowThreshold   float64 `yaml:"overflow_threshold"`
	FallbackToBaseModel bool    `yaml:"fallback_to_base_model"`
}

type VLLMConfig struct {
	ModelName           string `yaml:"model_name"`
	MaxLoras            int    `yaml:"max_loras"`
	MaxLoraRank         int    `yaml:"max_lora_rank"`
	HealthCheckInterval string `yaml:"health_check_interval"`
}

func Default() *Config {
	cfg := &Config{}
	applyDefaults(cfg)
	return cfg
}

func Validate(cfg *Config) error {
	return validate(cfg)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	data = []byte(os.ExpandEnv(string(data)))

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.VLLMUrl == "" {
		cfg.VLLMUrl = "http://localhost:8000"
	}
	if cfg.Storage.Dir == "" {
		cfg.Storage.Dir = "/mnt/nvme/loraplex"
	}
	if cfg.Storage.MaxSize == "" {
		cfg.Storage.MaxSize = "100GB"
	}
	if cfg.Invalidation.TTL == "" {
		cfg.Invalidation.TTL = "1h"
	}
	if cfg.Discovery.Mode == "" {
		cfg.Discovery.Mode = "static"
	}
	if cfg.Discovery.Static.Peers == nil {
		cfg.Discovery.Static.Peers = []string{"localhost:8080"}
	}
	if cfg.Discovery.File.HeartbeatInterval == "" {
		cfg.Discovery.File.HeartbeatInterval = "5s"
	}
	if cfg.Discovery.File.StaleThreshold == "" {
		cfg.Discovery.File.StaleThreshold = "15s"
	}
	if cfg.Routing.HashOn == "" {
		cfg.Routing.HashOn = "model"
	}
	if cfg.Routing.VnodesPerPeer == 0 {
		cfg.Routing.VnodesPerPeer = 150
	}
	if cfg.Routing.ForwardTimeout == "" {
		cfg.Routing.ForwardTimeout = "5s"
	}
	if cfg.Routing.OverflowThreshold == 0 {
		cfg.Routing.OverflowThreshold = 0.8
	}
	if cfg.VLLM.MaxLoras == 0 {
		cfg.VLLM.MaxLoras = 200
	}
	if cfg.VLLM.MaxLoraRank == 0 {
		cfg.VLLM.MaxLoraRank = 64
	}
	if cfg.VLLM.HealthCheckInterval == "" {
		cfg.VLLM.HealthCheckInterval = "10s"
	}
}

func validate(cfg *Config) error {
	if _, err := ParseSize(cfg.Storage.MaxSize); err != nil {
		return fmt.Errorf("invalid storage max_size: %w", err)
	}
	if _, err := time.ParseDuration(cfg.Invalidation.TTL); err != nil {
		return fmt.Errorf("invalid invalidation ttl: %w", err)
	}
	if _, err := time.ParseDuration(cfg.Routing.ForwardTimeout); err != nil {
		return fmt.Errorf("invalid forward_timeout: %w", err)
	}
	switch cfg.Discovery.Mode {
	case "static", "file", "k8s":
	default:
		return fmt.Errorf("unknown discovery mode: %s", cfg.Discovery.Mode)
	}
	if cfg.Routing.OverflowThreshold < 0 || cfg.Routing.OverflowThreshold > 1 {
		return fmt.Errorf("overflow_threshold must be between 0.0 and 1.0")
	}
	if err := validateHashOn(cfg.Routing.HashOn); err != nil {
		return err
	}
	return nil
}

func validateHashOn(hashOn string) error {
	switch {
	case hashOn == "model", hashOn == "tenant", hashOn == "tenant/model":
		return nil
	case strings.HasPrefix(hashOn, "header:"):
		name := strings.TrimPrefix(hashOn, "header:")
		name = strings.TrimSuffix(name, "/model")
		if name == "" {
			return fmt.Errorf("hash_on header: name cannot be empty")
		}
		return nil
	default:
		return fmt.Errorf("invalid hash_on %q (valid: model, tenant, tenant/model, header:<name>, header:<name>/model)", hashOn)
	}
}

// ParseSize parses human-readable sizes like "10GB", "1TB", "500MB".
func ParseSize(s string) (int64, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid size: %s", s)
	}
	var multiplier int64
	var numStr string
	switch {
	case s[len(s)-2:] == "TB":
		multiplier = 1 << 40
		numStr = s[:len(s)-2]
	case s[len(s)-2:] == "GB":
		multiplier = 1 << 30
		numStr = s[:len(s)-2]
	case s[len(s)-2:] == "MB":
		multiplier = 1 << 20
		numStr = s[:len(s)-2]
	default:
		return 0, fmt.Errorf("unknown size unit in: %s", s)
	}
	var n int64
	if _, err := fmt.Sscanf(numStr, "%d", &n); err != nil {
		return 0, fmt.Errorf("invalid size number: %s", s)
	}
	return n * multiplier, nil
}
