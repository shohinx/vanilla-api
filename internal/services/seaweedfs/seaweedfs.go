package seaweedfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/deepteams/webp"
	"github.com/disintegration/imaging"
)

const (
	MaxSourcePixels = 40_000_000
	MaxWidth        = 1600
	MaxHeight       = 1600
	MaxStoredBytes  = 2 << 20
	WebPQuality     = 80
)

var (
	ErrNotFound       = errors.New("image not found")
	ErrInvalidImage   = errors.New("invalid image")
	ErrTooManyPixels  = errors.New("image has too many pixels")
	ErrCannotCompress = errors.New("image cannot be compressed below the storage limit")
)

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

type Result struct {
	Data           []byte
	OriginalWidth  int
	OriginalHeight int
	Width          int
	Height         int
}

type clientAPI interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type Store struct {
	client clientAPI
	bucket string
}

func ConfigFromEnv() Config {
	return Config{
		Endpoint:  os.Getenv("IMAGE_S3_ENDPOINT"),
		Region:    os.Getenv("IMAGE_S3_REGION"),
		AccessKey: os.Getenv("IMAGE_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("IMAGE_S3_SECRET_KEY"),
		Bucket:    os.Getenv("IMAGE_S3_BUCKET"),
	}
}

// New returns nil when image storage is completely unconfigured. Partial
// configuration is rejected so uploads cannot silently lose their backing store.
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
	if s == nil || s.client == nil {
		return errors.New("store image: storage is not configured")
	}
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
	if s == nil || s.client == nil {
		return Object{}, errors.New("load image: storage is not configured")
	}
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

// Optimize validates, auto-orients, downsizes, and converts one still image
// to WebP. Re-encoding without metadata strips EXIF, ICC, and XMP payloads.
func Optimize(source io.ReadSeeker) (Result, error) {
	if source == nil {
		return Result{}, fmt.Errorf("%w: source is required", ErrInvalidImage)
	}
	config, format, err := image.DecodeConfig(source)
	if err != nil {
		return Result{}, fmt.Errorf("%w: decode configuration: %w", ErrInvalidImage, err)
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return Result{}, fmt.Errorf("%w: unsupported format %q", ErrInvalidImage, format)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return Result{}, fmt.Errorf("%w: invalid dimensions", ErrInvalidImage)
	}
	if int64(config.Width) > int64(MaxSourcePixels)/int64(config.Height) {
		return Result{}, ErrTooManyPixels
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return Result{}, fmt.Errorf("%w: rewind source: %w", ErrInvalidImage, err)
	}

	decoded, err := imaging.Decode(source, imaging.AutoOrientation(true))
	if err != nil {
		return Result{}, fmt.Errorf("%w: decode pixels: %w", ErrInvalidImage, err)
	}
	options := webp.DefaultOptions()
	options.Quality = WebPQuality
	options.Preset = webp.PresetPhoto
	options.UseSharpYUV = true

	var optimized image.Image
	var output bytes.Buffer
	for _, dimensions := range [][2]int{{MaxWidth, MaxHeight}, {1200, 1200}, {800, 800}} {
		optimized = imaging.Fit(decoded, dimensions[0], dimensions[1], imaging.Lanczos)
		output.Reset()
		if err := webp.Encode(&output, optimized, options); err != nil {
			return Result{}, fmt.Errorf("encode optimized WebP: %w", err)
		}
		if output.Len() <= MaxStoredBytes {
			break
		}
	}
	if output.Len() > MaxStoredBytes {
		return Result{}, ErrCannotCompress
	}

	originalBounds := decoded.Bounds()
	optimizedBounds := optimized.Bounds()
	return Result{
		Data:           output.Bytes(),
		OriginalWidth:  originalBounds.Dx(),
		OriginalHeight: originalBounds.Dy(),
		Width:          optimizedBounds.Dx(),
		Height:         optimizedBounds.Dy(),
	}, nil
}
