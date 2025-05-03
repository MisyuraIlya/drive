package S3

import (
	"context"
	"fmt"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"os"
)

func NewMinioClient() (*minio.Client, error) {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		port := os.Getenv("MINIO_PORT")
		if port == "" {
			port = "9000"
		}
		endpoint = fmt.Sprintf("localhost:%s", port)
	}

	accessKey := os.Getenv("MINIO_ROOT_USER")
	secretKey := os.Getenv("MINIO_ROOT_PASSWORD")
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("MINIO_ROOT_USER and MINIO_ROOT_PASSWORD must be set")
	}

	useSSL := false

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	ctx := context.Background()
	if _, err := client.ListBuckets(ctx); err != nil {
		return nil, fmt.Errorf("unable to verify MinIO connection: %w", err)
	}

	return client, nil

}
