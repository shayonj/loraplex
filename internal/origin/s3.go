package origin

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/shayonj/loraplex/internal/cache"
)

type S3Origin struct {
	client *s3.Client
	bucket string
	prefix string
}

func NewS3(bucket, region, prefix string) (*S3Origin, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &S3Origin{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
		prefix: strings.TrimSuffix(prefix, "/") + "/",
	}, nil
}

func (o *S3Origin) Name() string { return "s3" }

func (o *S3Origin) Fetch(ctx context.Context, adapterID string, destDir string) (*cache.FetchResult, error) {
	keyPrefix := o.prefix + adapterID + "/"

	listInput := &s3.ListObjectsV2Input{
		Bucket: &o.bucket,
		Prefix: &keyPrefix,
	}
	listOut, err := o.client.ListObjectsV2(ctx, listInput)
	if err != nil {
		return nil, fmt.Errorf("list s3 objects: %w", err)
	}
	if len(listOut.Contents) == 0 {
		return nil, fmt.Errorf("no objects found at s3://%s/%s", o.bucket, keyPrefix)
	}

	var etag string
	for _, obj := range listOut.Contents {
		key := *obj.Key
		relPath := strings.TrimPrefix(key, keyPrefix)
		if relPath == "" || strings.HasSuffix(relPath, "/") {
			continue
		}

		localPath := filepath.Join(destDir, relPath)
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			return nil, err
		}

		getInput := &s3.GetObjectInput{
			Bucket: &o.bucket,
			Key:    &key,
		}
		getOut, err := o.client.GetObject(ctx, getInput)
		if err != nil {
			return nil, fmt.Errorf("get s3 object %s: %w", key, err)
		}

		f, err := os.Create(localPath)
		if err != nil {
			getOut.Body.Close()
			return nil, err
		}
		_, copyErr := io.Copy(f, getOut.Body)
		getOut.Body.Close()
		f.Close()
		if copyErr != nil {
			return nil, copyErr
		}

		if relPath == "adapter_config.json" && getOut.ETag != nil {
			etag = *getOut.ETag
		}
	}

	return &cache.FetchResult{
		Origin: "s3",
		ETag:   etag,
	}, nil
}

func (o *S3Origin) Head(ctx context.Context, adapterID string) (*cache.FetchResult, error) {
	key := o.prefix + adapterID + "/adapter_config.json"
	input := &s3.HeadObjectInput{
		Bucket: &o.bucket,
		Key:    &key,
	}
	out, err := o.client.HeadObject(ctx, input)
	if err != nil {
		return nil, err
	}
	var etag string
	if out.ETag != nil {
		etag = *out.ETag
	}
	return &cache.FetchResult{
		Origin: "s3",
		ETag:   etag,
	}, nil
}
