package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdapterMetaIsExpired(t *testing.T) {
	fresh := &AdapterMeta{
		FetchedAt:  time.Now(),
		TTLSeconds: 3600,
	}
	if fresh.IsExpired() {
		t.Fatal("fresh meta should not be expired")
	}

	stale := &AdapterMeta{
		FetchedAt:  time.Now().Add(-2 * time.Hour),
		TTLSeconds: 3600,
	}
	if !stale.IsExpired() {
		t.Fatal("stale meta should be expired")
	}

	noTTL := &AdapterMeta{
		FetchedAt:  time.Now().Add(-100 * time.Hour),
		TTLSeconds: 0,
	}
	if noTTL.IsExpired() {
		t.Fatal("zero TTL should never expire")
	}
}

func TestAdapterMetaContentID(t *testing.T) {
	m1 := &AdapterMeta{ETag: "abc123"}
	if m1.ContentID() != "abc123" {
		t.Fatalf("expected abc123, got %s", m1.ContentID())
	}

	m2 := &AdapterMeta{Revision: "sha256"}
	if m2.ContentID() != "sha256" {
		t.Fatalf("expected sha256, got %s", m2.ContentID())
	}

	m3 := &AdapterMeta{ETag: "etag", Revision: "rev"}
	if m3.ContentID() != "etag" {
		t.Fatal("ETag should take precedence over Revision")
	}
}

func TestWriteAndReadMeta(t *testing.T) {
	dir := t.TempDir()
	meta := &AdapterMeta{
		Origin:     "s3",
		ETag:       "\"abc123\"",
		FetchedAt:  time.Now().Truncate(time.Second),
		TTLSeconds: 3600,
	}

	if err := WriteMeta(dir, meta); err != nil {
		t.Fatal(err)
	}

	read, err := ReadMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if read.Origin != "s3" {
		t.Fatalf("expected origin s3, got %s", read.Origin)
	}
	if read.ETag != "\"abc123\"" {
		t.Fatalf("expected etag, got %s", read.ETag)
	}
	if read.TTLSeconds != 3600 {
		t.Fatalf("expected ttl 3600, got %d", read.TTLSeconds)
	}
}

func TestReadMetaMissing(t *testing.T) {
	_, err := ReadMeta(t.TempDir())
	if err == nil {
		t.Fatal("expected error reading missing meta")
	}
}

func TestReadMetaInvalid(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, MetaFileName), []byte("not json"), 0644)
	_, err := ReadMeta(dir)
	if err == nil {
		t.Fatal("expected error reading invalid meta")
	}
}
