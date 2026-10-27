package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3 stores files as objects in an S3-compatible store, named
// s3://bucket/key. It is configured the way the AWS SDK is: AWS_REGION, the
// usual credentials, and AWS_ENDPOINT_URL for a store other than AWS, which
// is then addressed by path rather than by virtual host.
//
// An object store has no directories and no rename (ADR 0013). MkdirAll
// writes an empty marker object, dir/, for the directory and each parent,
// and Create fails unless the file's
// directory has one, as os.Create fails in a missing directory. Commit
// copies tmp to name on the server, then deletes tmp: a copy creates its
// object whole, so readers still never see part of a file.
type S3 struct {
	once   sync.Once
	client *s3.Client
	err    error
}

// NewS3 returns an S3 backend that uses client.
func NewS3(client *s3.Client) *S3 {
	b := &S3{client: client}
	b.once.Do(func() {})
	return b
}

func (b *S3) api(ctx context.Context) (*s3.Client, error) {
	b.once.Do(func() {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			b.err = fmt.Errorf("storage: s3: %w", err)
			return
		}
		if cfg.Region == "" {
			cfg.Region = "us-east-1"
		}
		b.client = s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = o.BaseEndpoint != nil })
	})
	return b.client, b.err
}

// object splits s3://bucket/key into its bucket and key.
func object(name string) (bucket, key string, err error) {
	rest, ok := strings.CutPrefix(name, "s3://")
	if bucket, key, _ = strings.Cut(rest, "/"); !ok || bucket == "" {
		return "", "", fmt.Errorf("storage: %q is not an s3://bucket/key name", name)
	}
	return bucket, key, nil
}

// List lists the keys under the pattern's longest literal prefix and returns
// those that match it.
func (b *S3) List(ctx context.Context, pattern string) ([]string, error) {
	c, err := b.api(ctx)
	if err != nil {
		return nil, err
	}
	bucket, keys, err := object(pattern)
	if err != nil {
		return nil, err
	}
	if _, err := path.Match(keys, ""); err != nil {
		return nil, err
	}
	prefix := keys[:strings.IndexAny(keys+"*", `*?[\`)]
	var names []string
	pages := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: &prefix})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			if ok, _ := path.Match(keys, *o.Key); ok && !strings.HasSuffix(*o.Key, "/") {
				names = append(names, "s3://"+bucket+"/"+*o.Key)
			}
		}
	}
	slices.Sort(names) // not every store lists keys in order
	return names, nil
}

func (b *S3) Size(ctx context.Context, name string) (int64, error) {
	c, bucket, key, err := b.resolve(ctx, name)
	if err != nil {
		return 0, err
	}
	out, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return 0, notExist(name, err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

func (b *S3) Open(ctx context.Context, name string, offset int64) (io.ReadCloser, error) {
	c, bucket, key, err := b.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	in := &s3.GetObjectInput{Bucket: &bucket, Key: &key}
	if offset > 0 {
		in.Range = aws.String(fmt.Sprintf("bytes=%d-", offset))
	}
	out, err := c.GetObject(ctx, in)
	if err != nil {
		return nil, notExist(name, err)
	}
	return out.Body, nil
}

// notExist adds fs.ErrNotExist to an error that says the object is missing,
// as the local backend's errors say it.
func notExist(name string, err error) error {
	var noKey *types.NoSuchKey
	var notFound *types.NotFound
	if errors.As(err, &noKey) || errors.As(err, &notFound) {
		return fmt.Errorf("storage: %s: %w: %w", name, fs.ErrNotExist, err)
	}
	return err
}

// Create spools the file to a local temporary file, which Close uploads in
// one PUT, so an object is at most 5 GB.
func (b *S3) Create(ctx context.Context, name string) (io.WriteCloser, error) {
	c, bucket, key, err := b.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if dir, _ := path.Split(key); dir != "" {
		var notFound *types.NotFound
		_, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &bucket, Key: &dir})
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("storage: creating %s: %w", name, fs.ErrNotExist)
		}
		if err != nil {
			return nil, err
		}
	}
	f, err := os.CreateTemp("", "mr-s3-*")
	if err != nil {
		return nil, err
	}
	return &upload{File: f, ctx: ctx, c: c, bucket: bucket, key: key}, nil
}

type upload struct {
	*os.File
	ctx         context.Context
	c           *s3.Client
	bucket, key string
}

func (u *upload) Close() error {
	_, err := u.Seek(0, io.SeekStart)
	if err == nil {
		_, err = u.c.PutObject(u.ctx, &s3.PutObjectInput{Bucket: &u.bucket, Key: &u.key, Body: u.File})
	}
	return errors.Join(err, u.File.Close(), os.Remove(u.Name()))
}

func (b *S3) Commit(ctx context.Context, tmp, name string) error {
	c, bucket, key, err := b.resolve(ctx, name)
	if err != nil {
		return err
	}
	srcBucket, srcKey, err := object(tmp)
	if err != nil {
		return err
	}
	src := srcBucket + "/" + (&url.URL{Path: srcKey}).EscapedPath()
	if _, err := c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: &bucket, Key: &key, CopySource: &src}); err != nil {
		return err
	}
	// The copy is the commit. A tmp left behind is removed with the rest of
	// the job's temporary files.
	_, _ = c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &srcBucket, Key: &srcKey})
	return nil
}

func (b *S3) Delete(ctx context.Context, name string) error {
	c, bucket, key, err := b.resolve(ctx, name)
	if err != nil {
		return err
	}
	_, err = c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &key})
	return err
}

func (b *S3) MkdirAll(ctx context.Context, dir string) error {
	c, bucket, key, err := b.resolve(ctx, dir)
	if err != nil || key == "" {
		return err
	}
	// One marker for the directory and each of its parents.
	key = strings.TrimSuffix(key, "/")
	for i := range len(key) + 1 {
		if i < len(key) && key[i] != '/' {
			continue
		}
		marker := key[:i] + "/"
		if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &marker, Body: strings.NewReader("")}); err != nil {
			return err
		}
	}
	return nil
}

// DeleteAll deletes every object under dir/, or in the bucket if dir is
// s3://bucket.
func (b *S3) DeleteAll(ctx context.Context, dir string) error {
	c, bucket, prefix, err := b.resolve(ctx, dir)
	if err != nil {
		return err
	}
	if prefix != "" { // a bucket's root has no "/" before its keys
		prefix = strings.TrimSuffix(prefix, "/") + "/"
	}
	pages := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{Bucket: &bucket, Prefix: &prefix})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return err
		}
		if len(page.Contents) == 0 {
			continue
		}
		ids := make([]types.ObjectIdentifier, len(page.Contents))
		for i, o := range page.Contents {
			ids[i] = types.ObjectIdentifier{Key: o.Key}
		}
		out, err := c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: &bucket, Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)}})
		if err != nil {
			return err
		}
		if len(out.Errors) > 0 {
			return fmt.Errorf("storage: deleting %s: %s", aws.ToString(out.Errors[0].Key), aws.ToString(out.Errors[0].Message))
		}
	}
	return nil
}

func (b *S3) resolve(ctx context.Context, name string) (*s3.Client, string, string, error) {
	c, err := b.api(ctx)
	if err != nil {
		return nil, "", "", err
	}
	bucket, key, err := object(name)
	return c, bucket, key, err
}
