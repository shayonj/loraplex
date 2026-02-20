package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	Adapter     string
	Latency     time.Duration
	StatusCode  int
	Err         string
	Passthrough bool
}

type adapterStats struct {
	Count     int
	Successes int
	Errors    int
	Latencies []time.Duration
}

func main() {
	target := flag.String("target", "http://localhost:9090", "loraplex URL")
	adapters := flag.String("adapters", "", "comma-separated adapter IDs")
	baseModel := flag.String("base-model", "", "base model name for passthrough requests")
	concurrency := flag.Int("concurrency", 4, "concurrent workers")
	duration := flag.Duration("duration", 60*time.Second, "benchmark duration")
	warmup := flag.Duration("warmup", 10*time.Second, "warmup period (excluded from results)")
	zipfS := flag.Float64("zipf-s", 1.5, "Zipf distribution skew (higher = more skewed)")
	mixPassthrough := flag.Float64("mix-passthrough", 0.2, "fraction of passthrough requests")
	maxTokens := flag.Int("max-tokens", 10, "max tokens per request")
	reportInterval := flag.Duration("report-interval", 5*time.Second, "stats print interval")
	flag.Parse()

	adapterList := splitNonEmpty(*adapters, ",")
	if len(adapterList) == 0 {
		fmt.Fprintln(os.Stderr, "error: --adapters is required")
		os.Exit(1)
	}

	fmt.Printf("=== Loraplex Load Generator ===\n")
	fmt.Printf("Target:      %s\n", *target)
	fmt.Printf("Adapters:    %d (%s)\n", len(adapterList), truncList(adapterList, 3))
	fmt.Printf("Base model:  %s\n", *baseModel)
	fmt.Printf("Concurrency: %d\n", *concurrency)
	fmt.Printf("Duration:    %s (warmup: %s)\n", *duration, *warmup)
	fmt.Printf("Zipf skew:   %.1f\n", *zipfS)
	fmt.Printf("Passthrough: %.0f%%\n\n", *mixPassthrough*100)

	client := &http.Client{Timeout: 120 * time.Second}

	var (
		results []result
		mu      sync.Mutex
		warmEnd = time.Now().Add(*warmup)
		done    atomic.Bool
		total   atomic.Int64
	)

	fmt.Printf("--- Warmup phase (%s) ---\n", *warmup)

	var wg sync.WaitGroup
	start := time.Now()

	for i := range *concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			localRng := rand.New(rand.NewSource(int64(workerID) + time.Now().UnixNano()))
			localZipf := rand.NewZipf(localRng, *zipfS, 1, uint64(len(adapterList)-1))

			for !done.Load() {
				passthrough := localRng.Float64() < *mixPassthrough && *baseModel != ""
				var model string
				if passthrough {
					model = *baseModel
				} else {
					idx := localZipf.Uint64()
					model = adapterList[idx]
				}

				r := sendRequest(client, *target, model, *maxTokens)
				r.Passthrough = passthrough
				total.Add(1)

				if time.Now().After(warmEnd) {
					mu.Lock()
					results = append(results, r)
					mu.Unlock()
				}
			}
		}(i)
	}

	ticker := time.NewTicker(*reportInterval)
	deadline := time.After(*warmup + *duration)

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(start)
			mu.Lock()
			n := len(results)
			var p50 time.Duration
			if n > 0 {
				sorted := make([]time.Duration, n)
				copy(sorted, latenciesOf(results))
				sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
				p50 = sorted[n/2]
			}
			mu.Unlock()
			fmt.Printf("[%6.0fs] total=%d measured=%d rps=%.1f p50=%s\n",
				elapsed.Seconds(), total.Load(), n, float64(total.Load())/elapsed.Seconds(), p50)

		case <-deadline:
			done.Store(true)
			wg.Wait()
			ticker.Stop()
			printReport(results, *duration, adapterList)
			return
		}
	}
}

func sendRequest(client *http.Client, target, model string, maxTokens int) result {
	body := map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "Hello"}},
		"max_tokens":  maxTokens,
		"temperature": 0,
	}
	data, _ := json.Marshal(body)

	start := time.Now()
	resp, err := client.Post(target+"/v1/chat/completions", "application/json", bytes.NewReader(data))
	latency := time.Since(start)

	if err != nil {
		return result{Adapter: model, Latency: latency, Err: err.Error()}
	}
	resp.Body.Close()
	r := result{Adapter: model, Latency: latency, StatusCode: resp.StatusCode}
	if resp.StatusCode >= 400 {
		r.Err = fmt.Sprintf("http %d", resp.StatusCode)
	}
	return r
}

func printReport(results []result, duration time.Duration, adapterList []string) {
	n := len(results)
	if n == 0 {
		fmt.Println("\nNo results collected.")
		return
	}

	successes := 0
	errors := 0
	byAdapter := make(map[string]*adapterStats)

	for _, r := range results {
		key := r.Adapter
		if r.Passthrough {
			key = "(passthrough)"
		}
		if byAdapter[key] == nil {
			byAdapter[key] = &adapterStats{}
		}
		s := byAdapter[key]
		s.Count++
		s.Latencies = append(s.Latencies, r.Latency)
		if r.Err == "" {
			s.Successes++
			successes++
		} else {
			s.Errors++
			errors++
		}
	}

	allLat := latenciesOf(results)
	sort.Slice(allLat, func(i, j int) bool { return allLat[i] < allLat[j] })

	fmt.Printf("\n")
	fmt.Printf("============================================\n")
	fmt.Printf("  Loraplex Benchmark Results\n")
	fmt.Printf("============================================\n")
	fmt.Printf("Duration:    %s\n", duration)
	fmt.Printf("Adapters:    %d\n", len(adapterList))
	fmt.Printf("Total reqs:  %d\n", n)
	fmt.Printf("Success:     %d (%.1f%%)\n", successes, pct(successes, n))
	fmt.Printf("Errors:      %d (%.1f%%)\n", errors, pct(errors, n))
	fmt.Printf("Throughput:  %.1f req/s\n", float64(n)/duration.Seconds())
	fmt.Printf("\n")
	fmt.Printf("Latency (all requests):\n")
	fmt.Printf("  p50:  %s\n", percentile(allLat, 0.50))
	fmt.Printf("  p95:  %s\n", percentile(allLat, 0.95))
	fmt.Printf("  p99:  %s\n", percentile(allLat, 0.99))
	fmt.Printf("  min:  %s\n", allLat[0])
	fmt.Printf("  max:  %s\n", allLat[len(allLat)-1])
	fmt.Printf("\n")

	fastCount := 0
	for _, l := range allLat {
		if l < 100*time.Millisecond {
			fastCount++
		}
	}
	fmt.Printf("Cache proxy estimate: %d/%d (%.1f%%) requests < 100ms\n", fastCount, n, pct(fastCount, n))
	fmt.Printf("\n")

	fmt.Printf("Per-adapter breakdown:\n")
	fmt.Printf("  %-55s %6s %6s %10s %10s %10s\n", "ADAPTER", "REQS", "OK%", "p50", "p95", "p99")

	keys := make([]string, 0, len(byAdapter))
	for k := range byAdapter {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return byAdapter[keys[i]].Count > byAdapter[keys[j]].Count
	})

	for _, k := range keys {
		s := byAdapter[k]
		sort.Slice(s.Latencies, func(i, j int) bool { return s.Latencies[i] < s.Latencies[j] })
		name := k
		if len(name) > 55 {
			name = "..." + name[len(name)-52:]
		}
		fmt.Printf("  %-55s %6d %5.1f%% %10s %10s %10s\n",
			name, s.Count, pct(s.Successes, s.Count),
			percentile(s.Latencies, 0.50),
			percentile(s.Latencies, 0.95),
			percentile(s.Latencies, 0.99))
	}
	fmt.Printf("============================================\n")
}

func latenciesOf(results []result) []time.Duration {
	out := make([]time.Duration, len(results))
	for i, r := range results {
		out[i] = r.Latency
	}
	return out
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

func splitNonEmpty(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncList(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(", ... +%d more", len(items)-max)
}
