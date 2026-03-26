package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	defaultPreviewImageBytes = 512 << 10 // 512 KB default — balances quality vs R2 Class B cost
	maxPreviewTextBytes      = 2 << 20   // 2 MB (text is cheap, keep as-is)
)

// PreviewPayload is returned to the frontend for rendering.
type PreviewPayload struct {
	// Type is one of: "image", "json", "csv", "text", "unsupported"
	Type     string `json:"type"`
	MIMEType string `json:"mime_type"`
	// Content holds text for text/json/csv types.
	Content string `json:"content,omitempty"`
	// DataURL holds a base64 data URL for image types.
	DataURL string `json:"data_url,omitempty"`
	// Size is the full object size in bytes (even if content was truncated).
	Size int64 `json:"size"`
	// Truncated is true when the content was cut to the preview limit.
	Truncated bool `json:"truncated"`
}

// PreviewObject fetches an object and returns a typed payload for rendering.
func (a *App) PreviewObject(bucket, key string) (*PreviewPayload, error) {
	if a.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	out, err := a.client.GetObject(ctx, &s3sdk.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()

	objectSize := aws.ToInt64(out.ContentLength)
	mimeType := detectMIME(key, out.ContentType)
	previewType := classifyMIME(mimeType)

	limit := int64(maxPreviewTextBytes)
	if previewType == "image" {
		limit = defaultPreviewImageBytes
		if a.config != nil && a.config.PreviewSizeLimit > 0 {
			limit = a.config.PreviewSizeLimit
		}
	}

	if previewType == "unsupported" || (objectSize > 0 && objectSize > limit) {
		// Don't fetch the body for unsupported types or objects that are
		// definitely too large. Return metadata only.
		return &PreviewPayload{
			Type:     "unsupported",
			MIMEType: mimeType,
			Size:     objectSize,
		}, nil
	}

	// Read up to limit+1 bytes so we can detect truncation.
	buf := make([]byte, limit+1)
	n, _ := readFull(out.Body, buf)
	truncated := int64(n) > limit
	data := buf[:min64(int64(n), limit)]

	payload := &PreviewPayload{
		Type:      previewType,
		MIMEType:  mimeType,
		Size:      objectSize,
		Truncated: truncated,
	}

	if previewType == "image" {
		payload.DataURL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	} else {
		payload.Content = string(data)
	}

	return payload, nil
}

// detectMIME resolves a MIME type using the key extension first (reliable for
// structured types like JSON/CSV), falling back to the Content-Type header
// from S3, and finally to content sniffing on an empty 512-byte prefix.
func detectMIME(key string, s3ContentType *string) string {
	ext := strings.ToLower(filepath.Ext(key))
	if ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			// Strip parameters (e.g. "; charset=utf-8") for the comparison.
			if base, _, err := mime.ParseMediaType(t); err == nil {
				return base
			}
			return t
		}
	}

	if s3ContentType != nil && *s3ContentType != "" && *s3ContentType != "application/octet-stream" {
		if base, _, err := mime.ParseMediaType(*s3ContentType); err == nil {
			return base
		}
		return *s3ContentType
	}

	return "application/octet-stream"
}

// classifyMIME maps a MIME type to a preview category.
func classifyMIME(t string) string {
	switch {
	case strings.HasPrefix(t, "image/"):
		return "image"
	case t == "application/json" || strings.HasSuffix(t, "+json"):
		return "json"
	case t == "text/csv" || t == "application/csv":
		return "csv"
	case strings.HasPrefix(t, "text/"):
		return "text"
	case t == "application/xml" || strings.HasSuffix(t, "+xml"):
		return "text"
	default:
		return "unsupported"
	}
}

// readFull reads from r until buf is full or EOF/error.
func readFull(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
