package proxy

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestExtractRequestInfoModel(t *testing.T) {
	body := `{"model":"my-lora-adapter","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))

	info, err := ExtractRequestInfo(req, "model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Model != "my-lora-adapter" {
		t.Fatalf("expected model 'my-lora-adapter', got %q", info.Model)
	}
	if info.HashKey != "my-lora-adapter" {
		t.Fatalf("expected hash key 'my-lora-adapter', got %q", info.HashKey)
	}
}

func TestExtractRequestInfoTenantHeader(t *testing.T) {
	body := `{"model":"adapter-x"}`
	req := httptest.NewRequest("POST", "/v1/completions", bytes.NewBufferString(body))
	req.Header.Set("X-Tenant-ID", "tenant-42")

	info, err := ExtractRequestInfo(req, "model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Tenant != "tenant-42" {
		t.Fatalf("expected tenant 'tenant-42', got %q", info.Tenant)
	}
	if info.HashKey != "adapter-x" {
		t.Fatalf("hash_on=model should ignore tenant; expected 'adapter-x', got %q", info.HashKey)
	}
}

func TestExtractRequestInfoEmptyBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/completions", bytes.NewBufferString(""))

	info, err := ExtractRequestInfo(req, "model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Model != "" {
		t.Fatalf("expected empty model, got %q", info.Model)
	}
	if info.HashKey != "" {
		t.Fatalf("expected empty hash key, got %q", info.HashKey)
	}
	if len(info.Body) != 0 {
		t.Fatalf("expected empty body bytes, got %d bytes", len(info.Body))
	}
}

func TestHashOnVariants(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		hashOn  string
		headers map[string]string
		wantKey string
	}{
		{
			name:    "model only",
			body:    `{"model":"lora-a"}`,
			hashOn:  "model",
			wantKey: "lora-a",
		},
		{
			name:    "model ignores tenant",
			body:    `{"model":"lora-b"}`,
			hashOn:  "model",
			headers: map[string]string{"X-Tenant-ID": "acme"},
			wantKey: "lora-b",
		},
		{
			name:    "tenant only",
			body:    `{"model":"lora-c"}`,
			hashOn:  "tenant",
			headers: map[string]string{"X-Tenant-ID": "acme"},
			wantKey: "acme",
		},
		{
			name:    "tenant alone without header",
			body:    `{"model":"lora-d"}`,
			hashOn:  "tenant",
			wantKey: "",
		},
		{
			name:    "tenant/model composite",
			body:    `{"model":"lora-e"}`,
			hashOn:  "tenant/model",
			headers: map[string]string{"X-Tenant-ID": "acme"},
			wantKey: "acme/lora-e",
		},
		{
			name:    "tenant/model falls back to model when no tenant",
			body:    `{"model":"lora-f"}`,
			hashOn:  "tenant/model",
			wantKey: "lora-f",
		},
		{
			name:    "header value",
			body:    `{"model":"lora-g"}`,
			hashOn:  "header:X-Region",
			headers: map[string]string{"X-Region": "us-east-1"},
			wantKey: "us-east-1",
		},
		{
			name:    "header missing",
			body:    `{"model":"lora-h"}`,
			hashOn:  "header:X-Region",
			wantKey: "",
		},
		{
			name:    "header/model composite",
			body:    `{"model":"lora-i"}`,
			hashOn:  "header:X-Region/model",
			headers: map[string]string{"X-Region": "eu-west-1"},
			wantKey: "eu-west-1/lora-i",
		},
		{
			name:    "header/model falls back to model when header missing",
			body:    `{"model":"lora-j"}`,
			hashOn:  "header:X-Region/model",
			wantKey: "lora-j",
		},
		{
			name:    "empty body",
			body:    `{}`,
			hashOn:  "model",
			wantKey: "",
		},
		{
			name:    "invalid json",
			body:    `not-json`,
			hashOn:  "model",
			wantKey: "",
		},
		{
			name:    "unknown hash_on defaults to model",
			body:    `{"model":"lora-k"}`,
			hashOn:  "something-else",
			wantKey: "lora-k",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/completions", bytes.NewBufferString(tt.body))
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			info, err := ExtractRequestInfo(req, tt.hashOn)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.HashKey != tt.wantKey {
				t.Fatalf("HashKey = %q, want %q", info.HashKey, tt.wantKey)
			}
		})
	}
}
