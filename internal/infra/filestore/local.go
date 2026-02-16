package filestore

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// LocalFileStore stores blobs on the local filesystem.
// This implementation is suitable for dev/prototype and can be swapped for S3/GCS.
type LocalFileStore struct {
	BaseDir string
}

func (s LocalFileStore) SaveSource(ctx context.Context, sourceID uuid.UUID, r io.Reader) (string, error) {
	_ = ctx
	path := filepath.Join(s.BaseDir, "sources", sourceID.String())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	return path, nil
}

func (s LocalFileStore) OpenSource(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	_ = ctx
	return os.Open(storageKey)
}

func (s LocalFileStore) PrepareExport(ctx context.Context, exportID uuid.UUID, ext string) (string, error) {
	_ = ctx
	if ext == "" {
		ext = "csv"
	}
	outDir := filepath.Join(s.BaseDir, "exports")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(outDir, exportID.String()+"."+ext), nil
}
