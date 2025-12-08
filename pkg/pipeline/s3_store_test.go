package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestS3MergeStoreKeyPath(t *testing.T) {
	tests := []struct {
		name       string
		prefix     string
		targetName string
		secretName string
		expected   string
	}{
		{
			name:       "no prefix",
			prefix:     "",
			targetName: "Serverless_Stg",
			secretName: "api-key",
			expected:   "Serverless_Stg/api-key.json",
		},
		{
			name:       "with prefix no trailing slash",
			prefix:     "merged",
			targetName: "Serverless_Stg",
			secretName: "api-key",
			expected:   "merged/Serverless_Stg/api-key.json",
		},
		{
			name:       "with prefix trailing slash",
			prefix:     "merged/",
			targetName: "Serverless_Stg",
			secretName: "api-key",
			expected:   "merged/Serverless_Stg/api-key.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &S3MergeStore{
				Bucket: "test-bucket",
				Prefix: tt.prefix,
			}
			result := store.keyPath(tt.targetName, tt.secretName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestS3MergeStoreGetMergePath(t *testing.T) {
	tests := []struct {
		name       string
		bucket     string
		prefix     string
		targetName string
		expected   string
	}{
		{
			name:       "no prefix",
			bucket:     "my-bucket",
			prefix:     "",
			targetName: "Serverless_Stg",
			expected:   "s3://my-bucket/Serverless_Stg",
		},
		{
			name:       "with prefix",
			bucket:     "my-bucket",
			prefix:     "secrets",
			targetName: "Serverless_Prod",
			expected:   "s3://my-bucket/secrets/Serverless_Prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &S3MergeStore{
				Bucket: tt.bucket,
				Prefix: tt.prefix,
			}
			result := store.GetMergePath(tt.targetName)
			assert.Equal(t, tt.expected, result)
		})
	}
}
