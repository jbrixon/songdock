package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jbrixon/songdock/internal/artwork"
)

type api interface {
	PutObject(context.Context, *awss3.PutObjectInput, ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
	GetObject(context.Context, *awss3.GetObjectInput, ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
	DeleteObject(context.Context, *awss3.DeleteObjectInput, ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error)
}

type Storage struct {
	client api
	cfg    artwork.Config
}

func New(ctx context.Context, cfg artwork.Config) (*Storage, error) {
	awsConfig, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load S3 client configuration: %w", err)
	}
	client := awss3.NewFromConfig(awsConfig, func(options *awss3.Options) {
		options.UsePathStyle = cfg.ForcePathStyle
		if cfg.Endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return NewWithClient(client, cfg), nil
}

func NewWithClient(client api, cfg artwork.Config) *Storage {
	return &Storage{client: client, cfg: cfg}
}

func (s *Storage) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if !artwork.ValidKey(key) {
		return fmt.Errorf("invalid artwork key %q", key)
	}
	_, err := s.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(s.objectKey(key)),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	return err
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if !artwork.ValidKey(key) {
		return fmt.Errorf("invalid artwork key %q", key)
	}
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	return err
}

func (s *Storage) Open(ctx context.Context, key string) (artwork.Object, error) {
	if !artwork.ValidKey(key) {
		return artwork.Object{}, fmt.Errorf("invalid artwork key %q", key)
	}
	result, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		return artwork.Object{}, err
	}
	defer result.Body.Close()
	data, err := io.ReadAll(result.Body)
	if err != nil {
		return artwork.Object{}, err
	}
	modTime := time.Time{}
	if result.LastModified != nil {
		modTime = *result.LastModified
	}
	return artwork.Object{
		ReadSeekCloser: readSeekCloser{Reader: bytes.NewReader(data)},
		ModTime:        modTime,
		ContentType:    aws.ToString(result.ContentType),
	}, nil
}

func (s *Storage) PublicURL(key string) string {
	if s.cfg.PublicURL == "" || !artwork.ValidKey(key) {
		return ""
	}
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/" + escapePath(s.objectKey(key))
}

func (s *Storage) objectKey(key string) string {
	if s.cfg.Prefix == "" {
		return key
	}
	return path.Join(s.cfg.Prefix, key)
}

func escapePath(value string) string {
	parts := strings.Split(value, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }
