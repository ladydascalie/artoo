package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type BucketInfo struct {
	Name         string `json:"name"`
	CreationDate string `json:"creation_date"`
}

type ObjectInfo struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified string `json:"last_modified"`
	IsFolder     bool   `json:"is_folder"`
}

type ListResult struct {
	Objects     []ObjectInfo `json:"objects"`
	Prefixes    []string     `json:"prefixes"`
	IsTruncated bool         `json:"is_truncated"`
	NextToken   string       `json:"next_token"`
}

// ListBuckets returns all R2 buckets for the account.
func (a *App) ListBuckets() ([]BucketInfo, error) {
	if a.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()

	out, err := a.client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}

	buckets := make([]BucketInfo, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		info := BucketInfo{Name: aws.ToString(b.Name)}
		if b.CreationDate != nil {
			info.CreationDate = b.CreationDate.Format(time.RFC3339)
		}
		buckets = append(buckets, info)
	}
	return buckets, nil
}

// ListObjects lists objects in a bucket under a given prefix (one level deep).
func (a *App) ListObjects(bucket, prefix, continuationToken string) (*ListResult, error) {
	if a.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
		MaxKeys:   aws.Int32(1000),
	}
	if continuationToken != "" {
		input.ContinuationToken = aws.String(continuationToken)
	}

	out, err := a.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, err
	}

	result := &ListResult{
		Objects:     make([]ObjectInfo, 0),
		Prefixes:    make([]string, 0),
		IsTruncated: aws.ToBool(out.IsTruncated),
	}
	if out.NextContinuationToken != nil {
		result.NextToken = *out.NextContinuationToken
	}

	for _, p := range out.CommonPrefixes {
		result.Prefixes = append(result.Prefixes, aws.ToString(p.Prefix))
	}
	for _, obj := range out.Contents {
		key := aws.ToString(obj.Key)
		if key == prefix { // skip folder marker
			continue
		}
		info := ObjectInfo{
			Key:  key,
			Size: aws.ToInt64(obj.Size),
		}
		if obj.LastModified != nil {
			info.LastModified = obj.LastModified.Format(time.RFC3339)
		}
		result.Objects = append(result.Objects, info)
	}

	return result, nil
}

// DeleteObjects bulk-deletes up to any number of objects (batched at 1000).
func (a *App) DeleteObjects(bucket string, keys []string) error {
	if a.client == nil {
		return fmt.Errorf("not connected")
	}
	if !a.DeleteAllowed() {
		return fmt.Errorf("delete is disabled — enable it in Settings")
	}
	if len(keys) == 0 {
		return nil
	}

	total := len(keys)
	deleted := 0
	a.emit(fmt.Sprintf("Deleting %d objects from %s...", total, bucket))

	for i := 0; i < len(keys); i += 1000 {
		end := i + 1000
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[i:end]

		ids := make([]s3types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			ids[j] = s3types.ObjectIdentifier{Key: aws.String(key)}
		}

		ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
		_, err := a.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		cancel()
		if err != nil {
			a.emit(fmt.Sprintf("Error: %v", err))
			return err
		}

		deleted += len(batch)
		a.emit(fmt.Sprintf("Deleted %d / %d objects", deleted, total))
	}

	a.emit(fmt.Sprintf("Done. Deleted %d objects.", total))
	return nil
}

// DeletePrefix deletes all objects under a given prefix (folder deletion).
func (a *App) DeletePrefix(bucket, prefix string) error {
	if a.client == nil {
		return fmt.Errorf("not connected")
	}
	if !a.DeleteAllowed() {
		return fmt.Errorf("delete is disabled — enable it in Settings")
	}

	a.emit(fmt.Sprintf("Deleting all objects under %s...", prefix))
	deleted := 0

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
			a.emit(fmt.Sprintf("Error listing: %v", err))
			return err
		}
		if len(out.Contents) == 0 {
			break
		}

		ids := make([]s3types.ObjectIdentifier, len(out.Contents))
		for i, obj := range out.Contents {
			ids[i] = s3types.ObjectIdentifier{Key: obj.Key}
		}

		ctx2, cancel2 := context.WithTimeout(a.ctx, 60*time.Second)
		_, err = a.client.DeleteObjects(ctx2, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		cancel2()
		if err != nil {
			a.emit(fmt.Sprintf("Error deleting: %v", err))
			return err
		}

		deleted += len(out.Contents)
		a.emit(fmt.Sprintf("Deleted %d objects so far...", deleted))

		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}

	a.emit(fmt.Sprintf("Done. Deleted %d objects under %s", deleted, prefix))
	return nil
}

// SearchObjects lists all objects under prefix (recursively, no delimiter) whose
// key contains query (case-insensitive). Emits progress for large buckets.
func (a *App) SearchObjects(bucket, prefix, query string) ([]ObjectInfo, error) {
	if a.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	q := strings.ToLower(query)
	var results []ObjectInfo
	var token *string
	scanned := 0

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
			return nil, err
		}

		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			if strings.Contains(strings.ToLower(key), q) {
				info := ObjectInfo{Key: key, Size: aws.ToInt64(obj.Size)}
				if obj.LastModified != nil {
					info.LastModified = obj.LastModified.Format(time.RFC3339)
				}
				results = append(results, info)
			}
		}

		scanned += len(out.Contents)
		if aws.ToBool(out.IsTruncated) {
			token = out.NextContinuationToken
			a.emit(fmt.Sprintf("Searching: scanned %s objects, %d matches so far...", formatCount(int64(scanned)), len(results)))
		} else {
			break
		}
	}

	a.emit(fmt.Sprintf("Search complete: %d matches in %s objects scanned", len(results), formatCount(int64(scanned))))
	return results, nil
}
