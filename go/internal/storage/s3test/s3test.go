// Package s3test runs an in-process S3 server for tests.
package s3test

import (
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
)

// Start starts an S3 server with one empty bucket and registers a backend
// for it as s3://, until the test ends. It returns the bucket's URI.
func Start(t testing.TB) string {
	t.Helper()
	srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(srv.Close)
	client := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
	})
	const bucket = "test"
	if _, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	storage.Register("s3", storage.NewS3(client))
	t.Cleanup(func() { storage.Register("s3", &storage.S3{}) })
	return "s3://" + bucket
}
