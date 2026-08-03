package s3

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jbrixon/songdock/internal/artwork"
)

type fakeClient struct {
	put    *awss3.PutObjectInput
	get    *awss3.GetObjectInput
	delete *awss3.DeleteObjectInput
}

func (f *fakeClient) PutObject(_ context.Context, input *awss3.PutObjectInput, _ ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
	f.put = input
	return &awss3.PutObjectOutput{}, nil
}

func (f *fakeClient) GetObject(_ context.Context, input *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	f.get = input
	return &awss3.GetObjectOutput{Body: io.NopCloser(strings.NewReader("image")), ContentType: aws.String("image/png")}, nil
}

func (f *fakeClient) DeleteObject(_ context.Context, input *awss3.DeleteObjectInput, _ ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error) {
	f.delete = input
	return &awss3.DeleteObjectOutput{}, nil
}

func TestStorageUsesPrefixAndContentType(t *testing.T) {
	fake := new(fakeClient)
	storage := NewWithClient(fake, artwork.Config{Bucket: "bucket", Prefix: "artwork/covers", PublicURL: "https://assets.example.com"})

	if err := storage.Put(context.Background(), "abc.png", []byte("image"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(fake.put.Key); got != "artwork/covers/abc.png" {
		t.Fatalf("key = %q", got)
	}
	if got := aws.ToString(fake.put.ContentType); got != "image/png" {
		t.Fatalf("content type = %q", got)
	}
	if got := storage.PublicURL("abc.png"); got != "https://assets.example.com/artwork/covers/abc.png" {
		t.Fatalf("public URL = %q", got)
	}
}

func TestStorageOpensAndDeletesPrefixedObjects(t *testing.T) {
	fake := new(fakeClient)
	storage := NewWithClient(fake, artwork.Config{Bucket: "bucket", Prefix: "artwork"})

	object, err := storage.Open(context.Background(), "abc.webp")
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	if got, _ := io.ReadAll(object); string(got) != "image" {
		t.Fatalf("object body = %q", got)
	}
	if got := aws.ToString(fake.get.Key); got != "artwork/abc.webp" {
		t.Fatalf("get key = %q", got)
	}

	if err := storage.Delete(context.Background(), "abc.webp"); err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(fake.delete.Key); got != "artwork/abc.webp" {
		t.Fatalf("delete key = %q", got)
	}
}

func TestStorageRejectsUnsafeKeys(t *testing.T) {
	storage := NewWithClient(new(fakeClient), artwork.Config{Bucket: "bucket", Prefix: "artwork"})
	if err := storage.Delete(context.Background(), "../outside"); err == nil {
		t.Fatal("unsafe key was accepted")
	}
}
