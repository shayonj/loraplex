package origin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/shayonj/loraplex/internal/config"
)

func TestHTTPOriginFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/my-adapter/adapter_config.json":
			w.Header().Set("ETag", "\"abc123\"")
			w.Write([]byte(`{"r": 8}`))
		case "/my-adapter/adapter_model.safetensors":
			w.Write([]byte("weights-data"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	o := NewHTTP(srv.URL)
	dest := t.TempDir()

	result, err := o.Fetch(context.Background(), "my-adapter", dest)
	if err != nil {
		t.Fatal(err)
	}

	if result.Origin != "http" {
		t.Fatalf("expected origin http, got %s", result.Origin)
	}
	if result.ETag != "\"abc123\"" {
		t.Fatalf("expected etag abc123, got %s", result.ETag)
	}

	data, err := os.ReadFile(filepath.Join(dest, "adapter_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"r": 8}` {
		t.Fatalf("unexpected config content: %s", data)
	}

	data, err = os.ReadFile(filepath.Join(dest, "adapter_model.safetensors"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "weights-data" {
		t.Fatalf("unexpected weights content: %s", data)
	}
}

func TestHTTPOriginFetchNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	o := NewHTTP(srv.URL)
	dest := t.TempDir()

	_, err := o.Fetch(context.Background(), "missing", dest)
	if err == nil {
		t.Fatal("expected error for missing adapter")
	}
}

func TestHTTPOriginHead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" && r.URL.Path == "/my-adapter/adapter_config.json" {
			w.Header().Set("ETag", "\"etag-456\"")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	o := NewHTTP(srv.URL)
	result, err := o.Head(context.Background(), "my-adapter")
	if err != nil {
		t.Fatal(err)
	}
	if result.ETag != "\"etag-456\"" {
		t.Fatalf("expected etag etag-456, got %s", result.ETag)
	}
}

func TestHTTPOriginHeadNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	o := NewHTTP(srv.URL)
	_, err := o.Head(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for missing adapter")
	}
}

func TestHuggingFaceFetchMocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/org/my-lora":
			w.Write([]byte(`{"sha": "abc123"}`))
		case "/org/my-lora/resolve/main/adapter_config.json":
			w.Write([]byte(`{"r": 16}`))
		case "/org/my-lora/resolve/main/adapter_model.safetensors":
			w.Write([]byte("weights"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	o := &HuggingFaceOrigin{
		token:   "",
		client:  srv.Client(),
		baseURL: srv.URL,
	}
	dest := t.TempDir()

	result, err := o.Fetch(context.Background(), "org/my-lora", dest)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != "abc123" {
		t.Fatalf("expected revision abc123, got %s", result.Revision)
	}

	data, _ := os.ReadFile(filepath.Join(dest, "adapter_config.json"))
	if string(data) != `{"r": 16}` {
		t.Fatalf("unexpected config: %s", data)
	}
}

func TestHuggingFaceAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/models/org/lora":
			w.Write([]byte(`{"sha": "x"}`))
		case "/org/lora/resolve/main/adapter_config.json":
			w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	o := &HuggingFaceOrigin{
		token:   "hf_secret",
		client:  srv.Client(),
		baseURL: srv.URL,
	}
	dest := t.TempDir()

	o.Fetch(context.Background(), "org/lora", dest)
	if gotAuth != "Bearer hf_secret" {
		t.Fatalf("expected Bearer auth, got %q", gotAuth)
	}
}

func TestFromConfig(t *testing.T) {
	cfgs := []config.OriginConfig{
		{Type: "huggingface", Token: "tok"},
		{Type: "http", BaseURL: "http://example.com"},
	}
	origins, err := FromConfig(cfgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(origins) != 2 {
		t.Fatalf("expected 2 origins, got %d", len(origins))
	}
	if origins[0].Name() != "huggingface" {
		t.Fatalf("expected huggingface, got %s", origins[0].Name())
	}
	if origins[1].Name() != "http" {
		t.Fatalf("expected http, got %s", origins[1].Name())
	}
}

func TestFromConfigUnknownType(t *testing.T) {
	cfgs := []config.OriginConfig{{Type: "ftp"}}
	_, err := FromConfig(cfgs)
	if err == nil {
		t.Fatal("expected error for unknown origin type")
	}
}
