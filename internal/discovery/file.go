package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shayonj/loraplex/internal/config"
)

type File struct {
	Base
	dir       string
	heartbeat time.Duration
	stale     time.Duration
	selfAddr  string
	stopCh    chan struct{}
	stopOnce  sync.Once
}

func NewFile(cfg config.FileDiscovery, selfAddr string) (*File, error) {
	hb, err := time.ParseDuration(cfg.HeartbeatInterval)
	if err != nil {
		return nil, fmt.Errorf("parse heartbeat_interval: %w", err)
	}
	st, err := time.ParseDuration(cfg.StaleThreshold)
	if err != nil {
		return nil, fmt.Errorf("parse stale_threshold: %w", err)
	}

	addr := selfAddr
	if addr == "" {
		addr, _ = os.Hostname()
	}

	return &File{
		dir:       cfg.Dir,
		heartbeat: hb,
		stale:     st,
		selfAddr:  addr,
		stopCh:    make(chan struct{}),
	}, nil
}

func (f *File) Start(ctx context.Context) error {
	if err := os.MkdirAll(f.dir, 0755); err != nil {
		return err
	}

	go f.loop(ctx)
	return nil
}

func (f *File) Stop() {
	f.stopOnce.Do(func() {
		close(f.stopCh)
		selfFile := filepath.Join(f.dir, f.selfAddr)
		os.Remove(selfFile)
	})
}

func (f *File) loop(ctx context.Context) {
	ticker := time.NewTicker(f.heartbeat)
	defer ticker.Stop()

	f.writeHeartbeat()
	f.scan()

	for {
		select {
		case <-ctx.Done():
			return
		case <-f.stopCh:
			return
		case <-ticker.C:
			f.writeHeartbeat()
			f.scan()
		}
	}
}

func (f *File) writeHeartbeat() {
	selfFile := filepath.Join(f.dir, f.selfAddr)
	data := []byte(fmt.Sprintf("%s\n%d", f.selfAddr, time.Now().UnixNano()))
	if err := os.WriteFile(selfFile, data, 0644); err != nil {
		slog.Error("write heartbeat", "err", err)
	}
}

func (f *File) scan() {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		slog.Error("scan peers dir", "err", err)
		return
	}

	now := time.Now()
	var peers []string

	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > f.stale {
			os.Remove(filepath.Join(f.dir, e.Name()))
			continue
		}

		data, err := os.ReadFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			continue
		}
		lines := strings.SplitN(string(data), "\n", 2)
		if len(lines) > 0 && lines[0] != "" {
			peers = append(peers, strings.TrimSpace(lines[0]))
		}
	}

	f.SetPeers(peers)
}
