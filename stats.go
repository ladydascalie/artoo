package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type BucketStats struct {
	ObjectCount  int64  `json:"object_count"`
	TotalSize    int64  `json:"total_size"`
	LastModified string `json:"last_modified"` // RFC3339, scan path only
	Location     string `json:"location"`
}

// GetBucketStats returns aggregate statistics for a bucket.
// Uses the Cloudflare GraphQL Analytics API when an API token is configured;
// otherwise falls back to a full ListObjectsV2 scan (slow for large buckets).
func (a *App) GetBucketStats(bucket string) (*BucketStats, error) {
	if a.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	if a.config != nil && a.config.APIToken != "" {
		return a.getBucketStatsGraphQL(bucket)
	}
	return a.getBucketStatsScan(bucket)
}

func (a *App) getBucketStatsGraphQL(bucket string) (*BucketStats, error) {
	const gqlURL = "https://api.cloudflare.com/client/v4/graphql"

	query := `query R2BucketStats($accountTag: string!, $bucketName: string, $start: Time!, $end: Time!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      r2StorageAdaptiveGroups(
        limit: 1
        filter: { bucketName: $bucketName, datetime_geq: $start, datetime_leq: $end }
      ) {
        max { objectCount payloadSize metadataSize uploadCount }
      }
    }
  }
}`
	now := time.Now().UTC()
	variables := map[string]any{
		"accountTag": a.config.AccountID,
		"bucketName": bucket,
		"start":      now.AddDate(0, 0, -30).Format(time.RFC3339),
		"end":        now.Format(time.RFC3339),
	}
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})

	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gqlURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphql request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("analytics token rejected (HTTP %d): needs 'Account Analytics: Read' — R2 admin tokens cannot access analytics", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("graphql HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var result struct {
		Data struct {
			Viewer struct {
				Accounts []struct {
					R2StorageAdaptiveGroups []struct {
						Max struct {
							ObjectCount  int64 `json:"objectCount"`
							PayloadSize  int64 `json:"payloadSize"`
							MetadataSize int64 `json:"metadataSize"`
							UploadCount  int64 `json:"uploadCount"`
						} `json:"max"`
					} `json:"r2StorageAdaptiveGroups"`
				} `json:"accounts"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("graphql decode: %w", err)
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("graphql error: %s", result.Errors[0].Message)
	}

	accounts := result.Data.Viewer.Accounts
	if len(accounts) == 0 || len(accounts[0].R2StorageAdaptiveGroups) == 0 {
		return &BucketStats{}, nil
	}
	m := accounts[0].R2StorageAdaptiveGroups[0].Max

	loc := a.bucketLocation(bucket)
	return &BucketStats{
		ObjectCount: m.ObjectCount,
		TotalSize:   m.PayloadSize + m.MetadataSize,
		Location:    loc,
	}, nil
}

func (a *App) getBucketStatsScan(bucket string) (*BucketStats, error) {
	stats := &BucketStats{Location: a.bucketLocation(bucket)}

	var (
		token   *string
		newest  time.Time
		scanned int64
	)
	for {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		out, err := a.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			MaxKeys:           aws.Int32(1000),
			ContinuationToken: token,
		})
		cancel()
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			stats.ObjectCount++
			stats.TotalSize += aws.ToInt64(obj.Size)
			if obj.LastModified != nil && obj.LastModified.After(newest) {
				newest = *obj.LastModified
			}
		}
		scanned += int64(len(out.Contents))
		if aws.ToBool(out.IsTruncated) {
			token = out.NextContinuationToken
			a.emit(fmt.Sprintf("Scanning %s: %s objects so far...", bucket, formatCount(scanned)))
		} else {
			break
		}
	}
	if !newest.IsZero() {
		stats.LastModified = newest.Format(time.RFC3339)
	}
	return stats, nil
}

// bucketLocation fetches the bucket's region via GetBucketLocation.
func (a *App) bucketLocation(bucket string) string {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	out, err := a.client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(bucket)})
	if err != nil || out.LocationConstraint == "" {
		return ""
	}
	return string(out.LocationConstraint)
}

func formatCount(n int64) string {
	if n < 1_000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}
