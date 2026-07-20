package seaweedfs

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type fakeS3Client struct {
	putInput *s3.PutObjectInput
	getInput *s3.GetObjectInput
	getValue *s3.GetObjectOutput
	getErr   error
}

func (f *fakeS3Client) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putInput = input
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3Client) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.getInput = input
	return f.getValue, f.getErr
}

func TestPutUsesConfiguredBucketAndMetadata(t *testing.T) {
	client := &fakeS3Client{}
	store := &Store{client: client, bucket: "menu"}

	if err := store.Put(context.Background(), "menu/cake.jpg", strings.NewReader("jpeg"), 4, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(client.putInput.Bucket) != "menu" || aws.ToString(client.putInput.Key) != "menu/cake.jpg" {
		t.Fatalf("unexpected put target: %+v", client.putInput)
	}
	if aws.ToString(client.putInput.ContentType) != "image/jpeg" || aws.ToInt64(client.putInput.ContentLength) != 4 {
		t.Fatalf("unexpected put metadata: %+v", client.putInput)
	}
}

func TestGetMapsMissingObject(t *testing.T) {
	client := &fakeS3Client{getErr: &smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing"}}
	store := &Store{client: client, bucket: "menu"}

	_, err := store.Get(context.Background(), "menu/missing.jpg")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	var apiError smithy.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("expected the storage API error to remain unwrap-able, got %v", err)
	}
}

func TestGetReturnsObjectMetadata(t *testing.T) {
	client := &fakeS3Client{getValue: &s3.GetObjectOutput{
		Body:          io.NopCloser(strings.NewReader("jpeg")),
		ContentType:   aws.String("image/jpeg"),
		ContentLength: aws.Int64(4),
		ETag:          aws.String("\"etag\""),
	}}
	store := &Store{client: client, bucket: "menu"}

	object, err := store.Get(context.Background(), "menu/cake.jpg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := object.Body.Close(); err != nil {
			t.Errorf("close object body: %v", err)
		}
	})
	if object.ContentType != "image/jpeg" || object.ContentLength != 4 || object.ETag != "etag" {
		t.Fatalf("unexpected object: %+v", object)
	}
}

func TestStoreRejectsInvalidInputsBeforeCallingClient(t *testing.T) {
	client := &fakeS3Client{}
	store := &Store{client: client, bucket: "menu"}

	if err := store.Put(context.Background(), "", strings.NewReader("data"), 4, "image/webp"); err == nil {
		t.Fatal("expected an empty key to be rejected")
	}
	if _, err := store.Get(context.Background(), ""); err == nil {
		t.Fatal("expected an empty key to be rejected")
	}
	if client.putInput != nil || client.getInput != nil {
		t.Fatal("invalid input reached the storage client")
	}
}

func TestNewRejectsAWSEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"https://s3.amazonaws.com",
		"https://bucket.s3.us-east-1.amazonaws.com",
		"https://s3.us-gov-west-1.amazonaws.com",
		"https://s3.amazonaws.com.cn",
	} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := New(context.Background(), Config{
				Endpoint:  endpoint,
				Region:    "us-east-1",
				AccessKey: "access",
				SecretKey: "secret",
				Bucket:    "menu",
			})
			if err == nil || !strings.Contains(err.Error(), "cannot point to AWS") {
				t.Fatalf("expected AWS endpoint rejection, got %v", err)
			}
		})
	}
}

func TestNewAcceptsCustomS3CompatibleEndpoint(t *testing.T) {
	store, err := New(context.Background(), Config{
		Endpoint:  "https://seaweedfs.example.com",
		Region:    "us-east-1",
		AccessKey: "access",
		SecretKey: "secret",
		Bucket:    "menu",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("expected configured image store")
	}
}
