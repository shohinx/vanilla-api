package seaweedfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

var ErrNotFound = errors.New("image not found")

type Config struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	Bucket    string
}

type Object struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ETag          string
}

type clientAPI interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type Store struct {
	client clientAPI
	bucket string
}

// New returns nil when image storage is completely unconfigured. A partial
// configuration is rejected so a deployment cannot silently accept uploads
// without a usable backing store.
func New(ctx context.Context, config Config) (*Store, error) {
	config = normalizeConfig(config)

	if config.Endpoint == "" && config.AccessKey == "" && config.SecretKey == "" && config.Bucket == "" {
		return nil, nil
	}
	if config.Endpoint == "" || config.AccessKey == "" || config.SecretKey == "" || config.Bucket == "" {
		return nil, errors.New("IMAGE_S3_ENDPOINT, IMAGE_S3_ACCESS_KEY, IMAGE_S3_SECRET_KEY, and IMAGE_S3_BUCKET must all be configured")
	}
	if err := validateEndpoint(config.Endpoint); err != nil {
		return nil, err
	}
	if config.Region == "" {
		config.Region = "us-east-1"
	}

	awsConfig, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(config.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(config.AccessKey, config.SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load image storage configuration: %w", err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(config.Endpoint)
		options.UsePathStyle = true
	})
	return &Store{client: client, bucket: config.Bucket}, nil
}

func normalizeConfig(config Config) Config {
	config.Endpoint = strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	config.Region = strings.TrimSpace(config.Region)
	config.AccessKey = strings.TrimSpace(config.AccessKey)
	config.SecretKey = strings.TrimSpace(config.SecretKey)
	config.Bucket = strings.TrimSpace(config.Bucket)
	return config
}

func validateEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("IMAGE_S3_ENDPOINT must be an explicit HTTP(S) SeaweedFS endpoint")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("IMAGE_S3_ENDPOINT must use HTTP or HTTPS")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("IMAGE_S3_ENDPOINT cannot contain credentials, a query, or a fragment")
	}

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "amazonaws.com" || strings.HasSuffix(host, ".amazonaws.com") ||
		host == "amazonaws.com.cn" || strings.HasSuffix(host, ".amazonaws.com.cn") {
		return errors.New("IMAGE_S3_ENDPOINT cannot point to AWS; configure a SeaweedFS endpoint")
	}
	return nil
}

func (s *Store) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if key == "" {
		return errors.New("store image: key is required")
	}
	if body == nil {
		return errors.New("store image: body is required")
	}
	if size < 0 {
		return errors.New("store image: size cannot be negative")
	}
	if contentType == "" {
		return errors.New("store image: content type is required")
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
		CacheControl:  aws.String("public, max-age=31536000, immutable"),
	})
	if err != nil {
		return fmt.Errorf("store image: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, key string) (Object, error) {
	if key == "" {
		return Object{}, errors.New("load image: key is required")
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == "NoSuchKey" || apiError.ErrorCode() == "NotFound") {
			return Object{}, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return Object{}, fmt.Errorf("load image: %w", err)
	}
	if result == nil || result.Body == nil {
		return Object{}, errors.New("load image: storage returned an empty object")
	}
	return Object{
		Body:          result.Body,
		ContentType:   aws.ToString(result.ContentType),
		ContentLength: aws.ToInt64(result.ContentLength),
		ETag:          strings.Trim(aws.ToString(result.ETag), "\""),
	}, nil
}
