package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type RequestInfo struct {
	Model   string
	Tenant  string
	HashKey string
	Body    []byte
}

const maxBodySize = 10 << 20 // 10 MB

func ExtractRequestInfo(r *http.Request, hashOn string) (*RequestInfo, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	r.Body.Close()
	if int64(len(body)) > maxBodySize {
		return nil, fmt.Errorf("request body too large (max %d bytes)", maxBodySize)
	}

	info := &RequestInfo{Body: body}

	if len(body) > 0 {
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(body, &parsed); err == nil {
			if raw, ok := parsed["model"]; ok {
				var model string
				if json.Unmarshal(raw, &model) == nil {
					info.Model = model
				}
			}
		}
	}

	info.Tenant = r.Header.Get("X-Tenant-ID")
	info.HashKey = buildHashKey(info, r, hashOn)

	return info, nil
}

func buildHashKey(info *RequestInfo, r *http.Request, hashOn string) string {
	switch {
	case hashOn == "model":
		return info.Model

	case hashOn == "tenant":
		return info.Tenant

	case hashOn == "tenant/model":
		if info.Tenant != "" {
			return info.Tenant + "/" + info.Model
		}
		return info.Model

	case strings.HasPrefix(hashOn, "header:"):
		spec := strings.TrimPrefix(hashOn, "header:")
		parts := strings.SplitN(spec, "/", 2)
		headerVal := r.Header.Get(parts[0])

		if len(parts) == 2 && parts[1] == "model" {
			if headerVal != "" {
				return headerVal + "/" + info.Model
			}
			return info.Model
		}
		return headerVal

	default:
		return info.Model
	}
}
