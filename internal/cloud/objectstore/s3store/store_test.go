package s3store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"easyshare/internal/cloud/objectstore"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func TestValidateConfig(t *testing.T) {
	t.Parallel()
	validCredentials := Config{AccessKeyID: "access", SecretAccessKey: "secret"}
	for _, test := range []struct {
		name         string
		config       Config
		wantEndpoint string
		wantRegion   string
		valid        bool
	}{
		{
			name:         "HTTPS and default region",
			config:       mergeConfig(validCredentials, Config{Endpoint: "https://storage.example.com/"}),
			wantEndpoint: "https://storage.example.com",
			wantRegion:   defaultRegion,
			valid:        true,
		},
		{
			name:         "development HTTP explicitly enabled",
			config:       mergeConfig(validCredentials, Config{Endpoint: "http://127.0.0.1:9000", Region: "local", AllowInsecureHTTP: true}),
			wantEndpoint: "http://127.0.0.1:9000",
			wantRegion:   "local",
			valid:        true,
		},
		{name: "relative endpoint", config: mergeConfig(validCredentials, Config{Endpoint: "localhost:9000"})},
		{name: "HTTP not allowed", config: mergeConfig(validCredentials, Config{Endpoint: "http://127.0.0.1:9000"})},
		{name: "endpoint path", config: mergeConfig(validCredentials, Config{Endpoint: "https://example.com/s3"})},
		{name: "endpoint credentials", config: mergeConfig(validCredentials, Config{Endpoint: "https://user:pass@example.com"})},
		{name: "endpoint query", config: mergeConfig(validCredentials, Config{Endpoint: "https://example.com?x=1"})},
		{name: "unsupported scheme", config: mergeConfig(validCredentials, Config{Endpoint: "ftp://example.com"})},
		{name: "empty access key", config: Config{Endpoint: "https://example.com", SecretAccessKey: "secret"}},
		{name: "empty secret", config: Config{Endpoint: "https://example.com", AccessKeyID: "access"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			endpoint, region, err := validateConfig(test.config)
			if test.valid {
				if err != nil {
					t.Fatalf("validateConfig() error = %v", err)
				}
				if endpoint != test.wantEndpoint || region != test.wantRegion {
					t.Fatalf("validateConfig() = (%q, %q), want (%q, %q)", endpoint, region, test.wantEndpoint, test.wantRegion)
				}
				return
			}
			if !errors.Is(err, objectstore.ErrInvalidInput) {
				t.Fatalf("validateConfig() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestConfigureClientForcesRustFSPathStyle(t *testing.T) {
	t.Parallel()
	var options s3.Options
	configureClient("https://storage.example.com")(&options)
	if !options.UsePathStyle {
		t.Fatal("UsePathStyle = false, RustFS requires true")
	}
	if aws.ToString(options.BaseEndpoint) != "https://storage.example.com" {
		t.Fatalf("BaseEndpoint = %q", aws.ToString(options.BaseEndpoint))
	}
}

func TestCompleteMultipartUploadSortsProviderParts(t *testing.T) {
	t.Parallel()
	client := &recordingS3Client{}
	store := &Store{client: client}
	_, err := store.CompleteMultipartUpload(context.Background(), objectstore.CompleteMultipartUploadInput{
		ObjectRef: objectstore.ObjectRef{Bucket: "bucket", Key: "key"},
		UploadID:  "upload-id",
		Parts: []objectstore.CompletedPart{
			{PartNumber: 3, Token: "three"},
			{PartNumber: 1, Token: "one"},
			{PartNumber: 2, Token: "two"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []int32
	for _, part := range client.completeInput.MultipartUpload.Parts {
		got = append(got, aws.ToInt32(part.PartNumber))
	}
	if want := []int32{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("provider parts = %v, want %v", got, want)
	}
}

func TestMapErrorCategories(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		code  string
		fault smithy.ErrorFault
		want  error
	}{
		{code: "NoSuchKey", fault: smithy.FaultClient, want: objectstore.ErrNotFound},
		{code: "NoSuchUpload", fault: smithy.FaultClient, want: objectstore.ErrNotFound},
		{code: "PreconditionFailed", fault: smithy.FaultClient, want: objectstore.ErrPreconditionFailed},
		{code: "AccessDenied", fault: smithy.FaultClient, want: objectstore.ErrUnauthorized},
		{code: "InvalidPart", fault: smithy.FaultClient, want: objectstore.ErrInvalidInput},
		{code: "SlowDown", fault: smithy.FaultServer, want: objectstore.ErrTemporary},
		{code: "UnknownServerFailure", fault: smithy.FaultServer, want: objectstore.ErrTemporary},
	} {
		test := test
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			cause := &smithy.GenericAPIError{Code: test.code, Message: "provider detail", Fault: test.fault}
			err := mapError("test operation", cause)
			if !errors.Is(err, test.want) {
				t.Fatalf("mapError() = %v, want category %v", err, test.want)
			}
			if !errors.Is(err, cause) {
				t.Fatal("mapError() did not preserve provider cause")
			}
		})
	}
}

func mergeConfig(base, override Config) Config {
	base.Endpoint = override.Endpoint
	base.Region = override.Region
	base.AllowInsecureHTTP = override.AllowInsecureHTTP
	return base
}

type recordingS3Client struct {
	completeInput *s3.CompleteMultipartUploadInput
}

func (client *recordingS3Client) CreateMultipartUpload(context.Context, *s3.CreateMultipartUploadInput, ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	panic("unexpected CreateMultipartUpload call")
}

func (client *recordingS3Client) CompleteMultipartUpload(_ context.Context, input *s3.CompleteMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	client.completeInput = input
	return &s3.CompleteMultipartUploadOutput{ETag: aws.String("opaque-etag")}, nil
}

func (client *recordingS3Client) AbortMultipartUpload(context.Context, *s3.AbortMultipartUploadInput, ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	panic("unexpected AbortMultipartUpload call")
}

func (client *recordingS3Client) HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	panic("unexpected HeadObject call")
}

func (client *recordingS3Client) DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	panic("unexpected DeleteObject call")
}
