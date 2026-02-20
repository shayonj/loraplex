package origin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shayonj/loraplex/internal/cache"
)

type HuggingFaceOrigin struct {
	token   string
	client  *http.Client
	baseURL string
}

func NewHuggingFace(token string) *HuggingFaceOrigin {
	return &HuggingFaceOrigin{
		token:   token,
		client:  &http.Client{Timeout: 90 * time.Second},
		baseURL: "https://huggingface.co",
	}
}

func (o *HuggingFaceOrigin) Name() string { return "huggingface" }

func (o *HuggingFaceOrigin) Fetch(ctx context.Context, adapterID string, destDir string) (*cache.FetchResult, error) {
	repoInfo, err := o.getRepoInfo(ctx, adapterID)
	if err != nil {
		return nil, err
	}

	files := []string{"adapter_config.json", "adapter_model.safetensors", "adapter_model.bin"}
	fetched := false

	for _, f := range files {
		url := fmt.Sprintf("%s/%s/resolve/main/%s", o.baseURL, adapterID, f)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		if o.token != "" {
			req.Header.Set("Authorization", "Bearer "+o.token)
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
	}

	if !fetched {
		return nil, fmt.Errorf("no adapter files found for %s", adapterID)
	}

	if _, err := os.Stat(filepath.Join(destDir, "adapter_config.json")); err != nil {
		return nil, fmt.Errorf("adapter_config.json not found for %s", adapterID)
	}

	return &cache.FetchResult{
		Origin:   "huggingface",
		Revision: repoInfo.SHA,
	}, nil
}

func (o *HuggingFaceOrigin) Head(ctx context.Context, adapterID string) (*cache.FetchResult, error) {
	repoInfo, err := o.getRepoInfo(ctx, adapterID)
	if err != nil {
		return nil, err
	}
	return &cache.FetchResult{
		Origin:   "huggingface",
		Revision: repoInfo.SHA,
	}, nil
}

type hfRepoInfo struct {
	SHA string `json:"sha"`
}

func (o *HuggingFaceOrigin) getRepoInfo(ctx context.Context, adapterID string) (*hfRepoInfo, error) {
	url := fmt.Sprintf("%s/api/models/%s", o.baseURL, adapterID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if o.token != "" {
		req.Header.Set("Authorization", "Bearer "+o.token)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("huggingface api %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	info := &hfRepoInfo{}
	if err := json.NewDecoder(resp.Body).Decode(info); err != nil {
		return nil, err
	}
	return info, nil
}
