package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// UploadObject uploads a single file to the bucket under the given prefix.
// Opens a file picker dialog for the user to select a file.
func (a *App) UploadObject(bucket, prefix string) error {
	if a.client == nil {
		return fmt.Errorf("not connected")
	}

	path, err := wailsrt.OpenFileDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Select file to upload",
	})
	if err != nil {
		return err
	}
	if path == "" {
		return nil // cancelled
	}

	filename := filepath.Base(path)
	key := prefix + filename

	a.emit(fmt.Sprintf("Uploading %s...", filename))

	if err := a.uploadFrom(bucket, key, path); err != nil {
		return err
	}

	info, _ := os.Stat(path)
	if info != nil {
		a.emit(fmt.Sprintf("Uploaded %s (%s)", filename, formatBytes(info.Size())))
	}
	return nil
}

// UploadObjects uploads multiple files to the bucket under the given prefix.
// Opens a file picker dialog for the user to select files.
func (a *App) UploadObjects(bucket, prefix string) error {
	if a.client == nil {
		return fmt.Errorf("not connected")
	}

	paths, err := wailsrt.OpenMultipleFilesDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Select files to upload",
	})
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil // cancelled
	}

	conc, _ := a.GetDownloadConcurrency() // reuse download concurrency setting
	sem := make(chan struct{}, conc)
	a.emit(fmt.Sprintf("Uploading %d files (concurrency %d)...", len(paths), conc))

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		done     int
	)

	for _, path := range paths {
		path := path
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			filename := filepath.Base(path)
			key := prefix + filename

			if ulErr := a.uploadFrom(bucket, key, path); ulErr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = ulErr
				}
				mu.Unlock()
				a.emit(fmt.Sprintf("Error %s: %v", filename, ulErr))
				return
			}

			mu.Lock()
			done++
			a.emit(fmt.Sprintf("(%d/%d) %s", done, len(paths), filename))
			mu.Unlock()
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	a.emit(fmt.Sprintf("Done. Uploaded %d files to %s", len(paths), prefix))
	return nil
}

// uploadFrom uploads a local file to the bucket at the given key.
func (a *App) uploadFrom(bucket, key, localPath string) error {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	_, err = a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}
