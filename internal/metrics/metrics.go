package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loraplex_requests_total",
	}, []string{"tenant", "cache_tier"})

	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "loraplex_request_duration_seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"tenant", "cache_tier"})

	PeerForwardsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loraplex_peer_forwards_total",
	}, []string{"target_peer"})

	OverflowLocalTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "loraplex_overflow_local_total",
	})

	CacheHitsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loraplex_cache_hits_total",
	}, []string{"tier"})

	CacheEvictionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loraplex_cache_evictions_total",
	}, []string{"tier"})

	CacheSizeBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "loraplex_cache_size_bytes",
	}, []string{"tier"})

	CacheItems = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "loraplex_cache_items",
	}, []string{"tier"})

	PeersActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "loraplex_peers_active",
	})

	RingRebuildsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "loraplex_ring_rebuilds_total",
	})

	VLLMHealthy = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "loraplex_vllm_healthy",
	})

	ForwardDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "loraplex_forward_duration_seconds",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	})
)

func RecordRequest(tenant, tier string, d time.Duration) {
	if tenant == "" {
		tenant = "_default"
	}
	RequestsTotal.WithLabelValues(tenant, tier).Inc()
	RequestDuration.WithLabelValues(tenant, tier).Observe(d.Seconds())
}

func RecordForward(peer string) {
	PeerForwardsTotal.WithLabelValues(peer).Inc()
}

func RecordOverflow() {
	OverflowLocalTotal.Inc()
}

func RecordCacheHit(tier string) {
	CacheHitsTotal.WithLabelValues(tier).Inc()
}

func RecordEviction(tier string, count int) {
	CacheEvictionsTotal.WithLabelValues(tier).Add(float64(count))
}

func UpdateCacheGauges(tier string, sizeBytes int64, itemCount int) {
	CacheSizeBytes.WithLabelValues(tier).Set(float64(sizeBytes))
	CacheItems.WithLabelValues(tier).Set(float64(itemCount))
}

func RecordForwardDuration(d time.Duration) {
	ForwardDuration.Observe(d.Seconds())
}
