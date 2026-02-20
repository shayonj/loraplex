package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shayonj/loraplex/internal/admin"
	"github.com/shayonj/loraplex/internal/cache"
	"github.com/shayonj/loraplex/internal/config"
	"github.com/shayonj/loraplex/internal/discovery"
	"github.com/shayonj/loraplex/internal/health"
	"github.com/shayonj/loraplex/internal/metrics"
	"github.com/shayonj/loraplex/internal/origin"
	"github.com/shayonj/loraplex/internal/proxy"
	"github.com/shayonj/loraplex/internal/routing"
	"github.com/shayonj/loraplex/internal/server"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "", "path to config file")
	listen := flag.String("listen", "", "listen address (overrides config)")
	selfAddr := flag.String("self", "", "self address for ring identity (e.g. 10.0.0.1:9090)")
	vllmURL := flag.String("vllm-url", "", "vLLM URL (overrides config)")
	dir := flag.String("dir", "", "adapter storage directory (overrides config)")
	discoveryMode := flag.String("discovery", "", "discovery mode (overrides config)")
	peers := flag.String("peers", "", "comma-separated peer list for static discovery")
	flag.Parse()

	if *showVersion {
		fmt.Printf("loraplex %s (commit=%s date=%s)\n", version, commit, date)
		os.Exit(0)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	applyOverrides(cfg, *listen, *selfAddr, *vllmURL, *dir, *discoveryMode, *peers)

	if err := config.Validate(cfg); err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}

	if err := run(cfg); err != nil && err != http.ErrServerClosed {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.Load(path)
	}
	return config.Default(), nil
}

func applyOverrides(cfg *config.Config, listen, selfAddr, vllmURL, dir, disc, peers string) {
	if selfAddr != "" {
		cfg.SelfAddr = selfAddr
	}
	if listen != "" {
		cfg.Listen = listen
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if vllmURL != "" {
		cfg.VLLMUrl = vllmURL
	}
	if cfg.VLLMUrl == "" {
		cfg.VLLMUrl = "http://localhost:8000"
	}
	if dir != "" {
		cfg.Storage.Dir = dir
	}
	if cfg.Storage.Dir == "" {
		cfg.Storage.Dir = "/mnt/nvme/loraplex"
	}
	if cfg.Storage.MaxSize == "" {
		cfg.Storage.MaxSize = "100GB"
	}
	if disc != "" {
		cfg.Discovery.Mode = disc
	}
	if cfg.Discovery.Mode == "" {
		cfg.Discovery.Mode = "static"
	}
	if peers != "" {
		cfg.Discovery.Static.Peers = splitPeers(peers)
	}
	if cfg.Discovery.Static.Peers == nil {
		cfg.Discovery.Static.Peers = []string{cfg.Listen}
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
	if cfg.Invalidation.TTL == "" {
		cfg.Invalidation.TTL = "1h"
	}
	if cfg.VLLM.HealthCheckInterval == "" {
		cfg.VLLM.HealthCheckInterval = "10s"
	}
	if cfg.Discovery.File.HeartbeatInterval == "" {
		cfg.Discovery.File.HeartbeatInterval = "5s"
	}
	if cfg.Discovery.File.StaleThreshold == "" {
		cfg.Discovery.File.StaleThreshold = "15s"
	}
}

func splitPeers(s string) []string {
	var result []string
	start := 0
	for i := range len(s) {
		if s[i] == ',' {
			p := s[start:i]
			if p != "" {
				result = append(result, p)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

func run(cfg *config.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	maxSize, err := config.ParseSize(cfg.Storage.MaxSize)
	if err != nil {
		return fmt.Errorf("parse storage max_size: %w", err)
	}
	store := cache.NewStore(cfg.Storage.Dir, maxSize)

	origins, err := origin.FromConfig(cfg.Origins)
	if err != nil {
		return err
	}

	ttl, err := time.ParseDuration(cfg.Invalidation.TTL)
	if err != nil {
		return fmt.Errorf("parse invalidation ttl: %w", err)
	}
	cacheMgr := cache.NewManager(store, origins, ttl)
	if err := cacheMgr.Rebuild(); err != nil {
		slog.Warn("storage rebuild", "err", err)
	}

	ringAddr := cfg.Listen
	if cfg.SelfAddr != "" {
		ringAddr = cfg.SelfAddr
	}
	ring := routing.NewRing(cfg.Routing.VnodesPerPeer, ringAddr)

	disc, err := discovery.New(cfg.Discovery, ringAddr)
	if err != nil {
		return err
	}
	disc.OnChange(func(peers []string) {
		ring.Update(peers)
		metrics.PeersActive.Set(float64(len(peers)))
		metrics.RingRebuildsTotal.Inc()
		slog.Info("ring updated", "peers", len(peers))
	})
	if err := disc.Start(ctx); err != nil {
		return err
	}
	defer disc.Stop()

	forwardTimeout, err := time.ParseDuration(cfg.Routing.ForwardTimeout)
	if err != nil {
		return fmt.Errorf("parse forward_timeout: %w", err)
	}
	fwd := routing.NewForwarder(ring, forwardTimeout, cfg.Routing.OverflowThreshold)

	healthInterval, err := time.ParseDuration(cfg.VLLM.HealthCheckInterval)
	if err != nil {
		return fmt.Errorf("parse health_check_interval: %w", err)
	}
	healthChecker := health.NewChecker(ring, ringAddr, cfg.VLLMUrl, healthInterval)
	healthChecker.Start(ctx)

	p := proxy.New(proxy.ProxyConfig{
		VLLMUrl:        cfg.VLLMUrl,
		BaseModel:      cfg.VLLM.ModelName,
		CacheManager:   cacheMgr,
		Ring:           ring,
		Forwarder:      fwd,
		HashOn:         cfg.Routing.HashOn,
		FallbackToBase: cfg.Routing.FallbackToBaseModel,
	})

	adminHandler := admin.NewHandler(cacheMgr, ring, fwd)

	srv := server.New(server.Config{
		Listen: cfg.Listen,
		Proxy:  p,
		Health: healthChecker,
		Admin:  adminHandler,
	})

	go updateMetricsLoop(ctx, cacheMgr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		slog.Info("shutting down")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)
		cancel()
	}()

	return srv.Start()
}

func updateMetricsLoop(ctx context.Context, mgr *cache.Manager) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s := mgr.Store()
			metrics.UpdateCacheGauges("local", s.LRU().Size(), s.LRU().Len())
		}
	}
}
