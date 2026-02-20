package origin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shayonj/loraplex/internal/cache"
)

type HTTPOrigin struct {
	baseURL string
	client  *http.Client
}

func NewHTTP(baseURL string) *HTTPOrigin {
	return &HTTPOrigin{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  &http.Client{Timeout: 90 * time.Second},
	}
}

func (o *HTTPOrigin) Name() string { return "http" }

func (o *HTTPOrigin) Fetch(ctx context.Context, adapterID string, destDir string) (*cache.FetchResult, error) {
	files := []string{"adapter_config.json", "adapter_model.safetensors", "adapter_model.bin"}
	fetched := false
	var etag string

	for _, f := range files {
		url := fmt.Sprintf("%s/%s/%s", o.baseURL, adapterID, f)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}

		resp, err := o.client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		localPath := filepath.Join(destDir, f)
		out, err := os.Create(localPath)
		if err != nil {
			resp.Body.Close()
			return nil, err
		}
		_, copyErr := io.Copy(out, resp.Body)
		resp.Body.Close()
		out.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		fetched = true

		if f == "adapter_config.json" && resp.Header.Get("ETag") != "" {
			etag = resp.Header.Get("ETag")
		}
	}

	if !fetched {
		return nil, fmt.Errorf("no adapter files found at %s/%s/", o.baseURL, adapterID)
	}

	if _, err := os.Stat(filepath.Join(destDir, "adapter_config.json")); err != nil {
		return nil, fmt.Errorf("adapter_config.json not found for %s", adapterID)
	}

	return &cache.FetchResult{
		Origin: "http",
		ETag:   etag,
	}, nil
}

func (o *HTTPOrigin) Head(ctx context.Context, adapterID string) (*cache.FetchResult, error) {
	url := fmt.Sprintf("%s/%s/adapter_config.json", o.baseURL, adapterID)
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("head returned %d", resp.StatusCode)
	}

	return &cache.FetchResult{
		Origin: "http",
		ETag:   resp.Header.Get("ETag"),
	}, nil
}
