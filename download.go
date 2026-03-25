package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// PrefixEstimate is returned by EstimatePrefixes before a folder download.
type PrefixEstimate struct {
	ObjectCount int64    `json:"object_count"`
	TotalSize   int64    `json:"total_size"`
	Keys        []string `json:"keys"`
}

// EstimatePrefixes scans one or more prefixes and returns object count, total
// size, and the full key list. The frontend uses this to warn the user before
// committing to a large download.
func (a *App) EstimatePrefixes(bucket string, prefixes []string) (*PrefixEstimate, error) {
	if a.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	est := &PrefixEstimate{
		Keys: make([]string, 0),
	}

	for _, prefix := range prefixes {
		var token *string
		for {
			ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
			out, err := a.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
				Bucket:            aws.String(bucket),
				Prefix:            aws.String(prefix),
				MaxKeys:           aws.Int32(1000),
				ContinuationToken: token,
			})
			cancel()
			if err != nil {
				return nil, fmt.Errorf("listing %s: %w", prefix, err)
			}
			for _, obj := range out.Contents {
				est.ObjectCount++
				est.TotalSize += aws.ToInt64(obj.Size)
				est.Keys = append(est.Keys, aws.ToString(obj.Key))
			}
			if !aws.ToBool(out.IsTruncated) {
				break
			}
			token = out.NextContinuationToken
			a.emit(fmt.Sprintf("Scanning %s: %s objects...", prefix, formatCount(est.ObjectCount)))
		}
	}

	return est, nil
}

// DownloadObject downloads a single S3 object to a user-chosen path.
func (a *App) DownloadObject(bucket, key string) error {
	if a.client == nil {
		return fmt.Errorf("not connected")
	}

	filename := filepath.Base(key)
	dest, err := wailsrt.SaveFileDialog(a.ctx, wailsrt.SaveDialogOptions{
		DefaultFilename: filename,
		Title:           "Save file",
	})
	if err != nil {
		return err
	}
	if dest == "" {
		return nil // cancelled
	}

	a.emit(fmt.Sprintf("Downloading %s...", key))

	if err := a.downloadTo(bucket, key, dest); err != nil {
		return err
	}

	info, _ := os.Stat(dest)
	if info != nil {
		a.emit(fmt.Sprintf("Saved %s (%s)", filename, formatBytes(info.Size())))
	}
	return nil
}

// DownloadObjects downloads multiple objects concurrently into a user-chosen directory.
func (a *App) DownloadObjects(bucket string, keys []string) error {
	if a.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(keys) == 0 {
		return nil
	}

	base, err := wailsrt.OpenDirectoryDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Choose download folder",
	})
	if err != nil {
		return err
	}
	if base == "" {
		return nil // cancelled
	}

	// Create a timestamped export folder so repeated exports don't collide.
	exportName := "artoo-" + time.Now().Format("2006-01-02-150405") + "-export"
	dir := filepath.Join(base, exportName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create export dir: %w", err)
	}

	conc, _ := a.GetDownloadConcurrency()
	sem := make(chan struct{}, conc)
	a.emit(fmt.Sprintf("Downloading %d files into %s (concurrency %d)...", len(keys), exportName, conc))

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		done     int
	)

	for _, key := range keys {
		key := key
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			// Preserve the full key path inside the export folder.
			destPath := filepath.Join(dir, filepath.FromSlash(key))
			if dlErr := a.downloadTo(bucket, key, destPath); dlErr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = dlErr
				}
				mu.Unlock()
				a.emit(fmt.Sprintf("Error %s: %v", key, dlErr))
				return
			}

			mu.Lock()
			done++
			a.emit(fmt.Sprintf("(%d/%d) %s", done, len(keys), key))
			mu.Unlock()
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	a.emit(fmt.Sprintf("Done. %d files → %s", len(keys), filepath.Join(base, exportName)))
	return nil
}

// downloadTo fetches a single S3 object and writes it to destPath.
func (a *App) downloadTo(bucket, key, destPath string) error {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()

	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("create dirs: %w", err)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, out.Body); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func formatBytes(b int64) string {
	if b == 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	f := float64(b)
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", b)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
