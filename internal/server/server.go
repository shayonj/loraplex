package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/shayonj/loraplex/internal/admin"
	"github.com/shayonj/loraplex/internal/health"
	"github.com/shayonj/loraplex/internal/proxy"
)

type Server struct {
	httpServer *http.Server
	proxy      *proxy.Proxy
	health     *health.Checker
	admin      *admin.Handler
}

type Config struct {
	Listen  string
	Proxy   *proxy.Proxy
	Health  *health.Checker
	Admin   *admin.Handler
}

func New(cfg Config) *Server {
	mux := http.NewServeMux()
	s := &Server{
		proxy:  cfg.Proxy,
		health: cfg.Health,
		admin:  cfg.Admin,
	}

	mux.HandleFunc("/healthz", cfg.Health.ServeHTTP)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/admin/", cfg.Admin.ServeHTTP)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cfg.Health.IncrPending()
		defer cfg.Health.DecrPending()
		cfg.Proxy.ServeHTTP(w, r)
	})

	s.httpServer = &http.Server{
		Addr:         cfg.Listen,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

func (s *Server) Start() error {
	slog.Info("starting loraplex", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
