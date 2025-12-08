// Package pipeline provides S3-based merge store implementation for secrets aggregation.
package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
)

// S3MergeStore implements a merge store using S3 for intermediate secret storage.
// This is useful when you want to use S3 as a central repository for merged secrets
// before syncing to target accounts, or for audit/backup purposes.
type S3MergeStore struct {
	Bucket   string
	Prefix   string
	KMSKeyID string
	Region   string

	client *s3.Client
}

// NewS3MergeStore creates a new S3-based merge store
func NewS3MergeStore(ctx context.Context, cfg *MergeStoreS3, region string) (*S3MergeStore, error) {
	l := log.WithFields(log.Fields{
		"action": "NewS3MergeStore",
		"bucket": cfg.Bucket,
		"prefix": cfg.Prefix,
	})
	l.Debug("Creating S3 merge store")

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	store := &S3MergeStore{
		Bucket:   cfg.Bucket,
		Prefix:   cfg.Prefix,
		KMSKeyID: cfg.KMSKeyID,
		Region:   region,
		client:   s3.NewFromConfig(awsCfg),
	}

	return store, nil
}

// keyPath returns the full S3 key for a given target and secret name
func (s *S3MergeStore) keyPath(targetName, secretName string) string {
	prefix := s.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return fmt.Sprintf("%s%s/%s.json", prefix, targetName, secretName)
}

// WriteSecret writes a secret to S3
func (s *S3MergeStore) WriteSecret(ctx context.Context, targetName, secretName string, data map[string]interface{}) error {
	l := log.WithFields(log.Fields{
		"action":     "S3MergeStore.WriteSecret",
		"bucket":     s.Bucket,
		"target":     targetName,
		"secretName": secretName,
	})
	l.Debug("Writing secret to S3")

	key := s.keyPath(targetName, secretName)

	// Marshal secret data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal secret data: %w", err)
	}

	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(jsonData),
		ContentType: aws.String("application/json"),
	}

	// Use KMS encryption if configured
	if s.KMSKeyID != "" {
		input.ServerSideEncryption = "aws:kms"
		input.SSEKMSKeyId = aws.String(s.KMSKeyID)
	} else {
		input.ServerSideEncryption = "AES256"
	}

	_, err = s.client.PutObject(ctx, input)
	if err != nil {
		l.WithError(err).Error("Failed to write secret to S3")
		return fmt.Errorf("failed to put object: %w", err)
	}

	l.Debug("Successfully wrote secret to S3")
	return nil
}

// ReadSecret reads a secret from S3
func (s *S3MergeStore) ReadSecret(ctx context.Context, targetName, secretName string) (map[string]interface{}, error) {
	l := log.WithFields(log.Fields{
		"action":     "S3MergeStore.ReadSecret",
		"bucket":     s.Bucket,
		"target":     targetName,
		"secretName": secretName,
	})
	l.Debug("Reading secret from S3")

	key := s.keyPath(targetName, secretName)

	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer output.Body.Close()

	body, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal secret: %w", err)
	}

	return data, nil
}

// ListSecrets lists all secrets for a target
func (s *S3MergeStore) ListSecrets(ctx context.Context, targetName string) ([]string, error) {
	l := log.WithFields(log.Fields{
		"action": "S3MergeStore.ListSecrets",
		"bucket": s.Bucket,
		"target": targetName,
	})
	l.Debug("Listing secrets from S3")

	prefix := s.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	targetPrefix := fmt.Sprintf("%s%s/", prefix, targetName)

	var secrets []string
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.Bucket),
		Prefix: aws.String(targetPrefix),
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range output.Contents {
			key := aws.ToString(obj.Key)
			// Extract secret name from key (remove prefix and .json suffix)
			name := strings.TrimPrefix(key, targetPrefix)
			name = strings.TrimSuffix(name, ".json")
			if name != "" && !strings.Contains(name, "/") {
				secrets = append(secrets, name)
			}
		}
	}

	return secrets, nil
}

// DeleteSecret deletes a secret from S3
func (s *S3MergeStore) DeleteSecret(ctx context.Context, targetName, secretName string) error {
	l := log.WithFields(log.Fields{
		"action":     "S3MergeStore.DeleteSecret",
		"bucket":     s.Bucket,
		"target":     targetName,
		"secretName": secretName,
	})
	l.Debug("Deleting secret from S3")

	key := s.keyPath(targetName, secretName)

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	return nil
}

// GetMergePath returns the S3 "path" representation for a target
// This is used for logging and reporting purposes
func (s *S3MergeStore) GetMergePath(targetName string) string {
	prefix := s.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return fmt.Sprintf("s3://%s/%s%s", s.Bucket, prefix, targetName)
}
