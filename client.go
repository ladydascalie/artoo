package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx    context.Context
	client *s3.Client
	config *Config
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) initClient() {
	if a.config == nil {
		return
	}
	a.client = buildClient(a.config.AccountID, a.config.AccessKeyID, a.config.SecretAccessKey)
}

func buildClient(accountID, accessKeyID, secretAccessKey string) *s3.Client {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
	return s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
	})
}

// TestConnection verifies credentials by listing buckets.
func (a *App) TestConnection(accountID, accessKeyID, secretAccessKey string) error {
	client := buildClient(strings.TrimSpace(accountID), strings.TrimSpace(accessKeyID), strings.TrimSpace(secretAccessKey))
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	_, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	return nil
}

// emit sends a log event to the frontend.
func (a *App) emit(msg string) {
	wailsrt.EventsEmit(a.ctx, "log", msg)
}
