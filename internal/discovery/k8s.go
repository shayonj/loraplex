package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/shayonj/loraplex/internal/config"
)

type K8s struct {
	Base
	service   string
	namespace string
	client    *http.Client
	stopCh    chan struct{}
	stopOnce  sync.Once
}

func NewK8s(cfg config.K8sDiscovery) (*K8s, error) {
	ns := cfg.Namespace
	if ns == "" {
		data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			ns = "default"
		} else {
			ns = string(data)
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	if caCert, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"); err == nil {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		}
	}

	return &K8s{
		service:   cfg.Service,
		namespace: ns,
		client:    client,
		stopCh:    make(chan struct{}),
	}, nil
}

func (k *K8s) Start(ctx context.Context) error {
	go k.poll(ctx)
	return nil
}

func (k *K8s) Stop() {
	k.stopOnce.Do(func() {
		close(k.stopCh)
	})
}

func (k *K8s) poll(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	k.fetch()

	for {
		select {
		case <-ctx.Done():
			return
		case <-k.stopCh:
			return
		case <-ticker.C:
			k.fetch()
		}
	}
}

func (k *K8s) fetch() {
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		slog.Error("read k8s token", "err", err)
		return
	}

	url := fmt.Sprintf("https://kubernetes.default.svc/api/v1/namespaces/%s/endpoints/%s",
		k.namespace, k.service)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+string(token))

	resp, err := k.client.Do(req)
	if err != nil {
		slog.Error("fetch k8s endpoints", "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("k8s endpoints non-200", "status", resp.StatusCode, "body", string(body))
		return
	}

	var ep k8sEndpoints
	if err := json.NewDecoder(resp.Body).Decode(&ep); err != nil {
		slog.Error("decode k8s endpoints", "err", err)
		return
	}

	var peers []string
	for _, subset := range ep.Subsets {
		port := "8080"
		for _, p := range subset.Ports {
			if p.Name == "http" || p.Name == "loraplex" {
				port = fmt.Sprintf("%d", p.Port)
				break
			}
		}
		for _, addr := range subset.Addresses {
			peers = append(peers, fmt.Sprintf("%s:%s", addr.IP, port))
		}
	}

	k.SetPeers(peers)
}

type k8sEndpoints struct {
	Subsets []k8sSubset `json:"subsets"`
}

type k8sSubset struct {
	Addresses []k8sAddress `json:"addresses"`
	Ports     []k8sPort    `json:"ports"`
}

type k8sAddress struct {
	IP string `json:"ip"`
}

type k8sPort struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}
