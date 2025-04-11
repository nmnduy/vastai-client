package s3

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Client wraps the AWS S3 client and provides upload functionality.
type Client struct {
	s3Client *s3.Client
	uploader *manager.Uploader
}

// NewClient creates a new S3 client.
// It automatically loads configuration from the environment (e.g., AWS_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY).
func NewClient(ctx context.Context) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	if cfg.Region == "" {
		log.Println("Warning: AWS region not set in configuration, S3 operations might fail or use an unintended region.")
		// Optionally set a default region if none is found, or return an error
		// return nil, fmt.Errorf("AWS region must be configured")
	}

	s3Client := s3.NewFromConfig(cfg)
	uploader := manager.NewUploader(s3Client)

	log.Printf("AWS S3 client initialized for region: %s", cfg.Region)

	return &Client{
		s3Client: s3Client,
		uploader: uploader,
	}, nil
}

// UploadStream uploads data from an io.Reader to the specified S3 bucket and key.
// It uses the s3manager for efficient multipart uploads for larger streams.
func (c *Client) UploadStream(ctx context.Context, bucket, key string, body io.Reader) error {
	log.Printf("Uploading stream to s3://%s/%s", bucket, key)

	input := &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   body,
		// Consider adding other options like ACL, ContentType, Metadata etc. if needed
		// ACL: types.ObjectCannedACLPublicRead, // Example: if the object needs to be public
	}

	// The uploader handles context cancellation internally during the upload parts.
	result, err := c.uploader.Upload(ctx, input)
	if err != nil {
		// Check if the error is due to context cancellation
		if ctx.Err() != nil {
			return fmt.Errorf("s3 upload context cancelled for s3://%s/%s: %w", bucket, key, ctx.Err())
		}
		return fmt.Errorf("failed to upload stream to s3://%s/%s: %w", bucket, key, err)
	}

	log.Printf("Successfully uploaded stream to %s", result.Location)
	return nil
}

// Ensure Client implements the S3Uploader interface from the cleanup command.
// This interface needs to be defined or imported properly if used across packages.
// For now, we assume the interface definition is compatible.
// Example interface (should match the one in cleanup/main.go):
/*
type S3Uploader interface {
	UploadStream(ctx context.Context, bucket, key string, body io.Reader) error
}
var _ S3Uploader = (*Client)(nil)
*/
