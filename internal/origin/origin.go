package origin

import (
	"context"
	"fmt"

	"github.com/shayonj/loraplex/internal/cache"
	"github.com/shayonj/loraplex/internal/config"
)

type Origin interface {
	Name() string
	Fetch(ctx context.Context, adapterID string, destDir string) (*cache.FetchResult, error)
	Head(ctx context.Context, adapterID string) (*cache.FetchResult, error)
}

func FromConfig(cfgs []config.OriginConfig) ([]cache.OriginFetcher, error) {
	var origins []cache.OriginFetcher
	for _, cfg := range cfgs {
		switch cfg.Type {
		case "s3":
			o, err := NewS3(cfg.Bucket, cfg.Region, cfg.Prefix)
			if err != nil {
				return nil, fmt.Errorf("init s3 origin: %w", err)
			}
			origins = append(origins, o)
		case "huggingface":
			origins = append(origins, NewHuggingFace(cfg.Token))
		case "http":
			origins = append(origins, NewHTTP(cfg.BaseURL))
		default:
			return nil, fmt.Errorf("unknown origin type: %s", cfg.Type)
		}
	}
	return origins, nil
}
