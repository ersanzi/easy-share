// Package s3store implements objectstore.Store using the S3 API supported by RustFS.
package s3store

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"easyshare/internal/cloud/objectstore"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const defaultRegion = "us-east-1"

type Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	// AllowInsecureHTTP must only be enabled for loopback development or an
	// equivalently isolated test network.
	AllowInsecureHTTP bool
	HTTPClient        aws.HTTPClient
}

type s3API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	CreateMultipartUpload(context.Context, *s3.CreateMultipartUploadInput, ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	CompleteMultipartUpload(context.Context, *s3.CompleteMultipartUploadInput, ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	AbortMultipartUpload(context.Context, *s3.AbortMultipartUploadInput, ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type presignAPI interface {
	PresignUploadPart(context.Context, *s3.UploadPartInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

type Store struct {
	client    s3API
	presigner presignAPI
	now       func() time.Time
}

func New(config Config) (*Store, error) {
	endpoint, region, err := validateConfig(config)
	if err != nil {
		return nil, err
	}
	awsConfig := aws.Config{
		Region: region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			config.AccessKeyID,
			config.SecretAccessKey,
			config.SessionToken,
		)),
		HTTPClient: config.HTTPClient,
	}
	client := s3.NewFromConfig(awsConfig, configureClient(endpoint))
	return &Store{
		client:    client,
		presigner: s3.NewPresignClient(client),
		now:       time.Now,
	}, nil
}

func configureClient(endpoint string) func(*s3.Options) {
	return func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		// RustFS requires path-style addressing. Do not make this configurable.
		options.UsePathStyle = true
	}
}
func validateConfig(config Config) (string, string, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("%w: endpoint must be an absolute HTTP(S) URL", objectstore.ErrInvalidInput)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", "", fmt.Errorf("%w: endpoint scheme must be HTTP or HTTPS", objectstore.ErrInvalidInput)
	}
	if parsed.Scheme == "http" && !config.AllowInsecureHTTP {
		return "", "", fmt.Errorf("%w: HTTP endpoint requires AllowInsecureHTTP", objectstore.ErrInvalidInput)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("%w: endpoint must not contain credentials, query, or fragment", objectstore.ErrInvalidInput)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", "", fmt.Errorf("%w: endpoint must not contain a path", objectstore.ErrInvalidInput)
	}
	if strings.TrimSpace(config.AccessKeyID) == "" || strings.TrimSpace(config.SecretAccessKey) == "" {
		return "", "", fmt.Errorf("%w: access key ID and secret access key must not be empty", objectstore.ErrInvalidInput)
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = defaultRegion
	}
	parsed.Path = ""
	return strings.TrimSuffix(parsed.String(), "/"), region, nil
}

func (store *Store) CreateMultipartUpload(ctx context.Context, input objectstore.CreateMultipartUploadInput) (objectstore.MultipartUpload, error) {
	if err := input.Validate(); err != nil {
		return objectstore.MultipartUpload{}, err
	}
	request := &s3.CreateMultipartUploadInput{
		Bucket:   aws.String(input.Bucket),
		Key:      aws.String(input.Key),
		Metadata: objectstore.CloneMetadata(input.Metadata),
	}
	if input.ContentType != "" {
		request.ContentType = aws.String(input.ContentType)
	}
	output, err := store.client.CreateMultipartUpload(ctx, request)
	if err != nil {
		return objectstore.MultipartUpload{}, mapError("create multipart upload", err)
	}
	if output.UploadId == nil || *output.UploadId == "" {
		return objectstore.MultipartUpload{}, mapError("create multipart upload", errors.New("provider returned an empty upload ID"))
	}
	return objectstore.MultipartUpload{UploadID: *output.UploadId}, nil
}

func (store *Store) PresignUploadPart(ctx context.Context, input objectstore.PresignUploadPartInput) (objectstore.PresignedRequest, error) {
	if err := input.Validate(); err != nil {
		return objectstore.PresignedRequest{}, err
	}
	output, err := store.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(input.Bucket),
		Key:        aws.String(input.Key),
		UploadId:   aws.String(input.UploadID),
		PartNumber: aws.Int32(input.PartNumber),
	}, func(options *s3.PresignOptions) {
		options.Expires = input.Expires
	})
	if err != nil {
		return objectstore.PresignedRequest{}, mapError("presign upload part", err)
	}
	return presignedRequest(output, store.now().Add(input.Expires)), nil
}

func (store *Store) CompleteMultipartUpload(ctx context.Context, input objectstore.CompleteMultipartUploadInput) (objectstore.CompleteResult, error) {
	if err := input.ObjectRef.Validate(); err != nil {
		return objectstore.CompleteResult{}, err
	}
	if strings.TrimSpace(input.UploadID) == "" {
		return objectstore.CompleteResult{}, fmt.Errorf("%w: upload ID must not be empty", objectstore.ErrInvalidInput)
	}
	parts, err := objectstore.NormalizeCompletedParts(input.Parts)
	if err != nil {
		return objectstore.CompleteResult{}, err
	}
	providerParts := make([]types.CompletedPart, len(parts))
	for index, part := range parts {
		providerParts[index] = types.CompletedPart{
			ETag:       aws.String(part.Token),
			PartNumber: aws.Int32(part.PartNumber),
		}
	}
	output, err := store.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(input.Bucket),
		Key:      aws.String(input.Key),
		UploadId: aws.String(input.UploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: providerParts,
		},
	})
	if err != nil {
		return objectstore.CompleteResult{}, mapError("complete multipart upload", err)
	}
	return objectstore.CompleteResult{
		ETag:      aws.ToString(output.ETag),
		VersionID: aws.ToString(output.VersionId),
	}, nil
}

func (store *Store) AbortMultipartUpload(ctx context.Context, input objectstore.AbortMultipartUploadInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	_, err := store.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(input.Bucket),
		Key:      aws.String(input.Key),
		UploadId: aws.String(input.UploadID),
	})
	if err != nil {
		return mapError("abort multipart upload", err)
	}
	return nil
}

func (store *Store) HeadObject(ctx context.Context, ref objectstore.ObjectRef) (objectstore.ObjectInfo, error) {
	if err := ref.Validate(); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	output, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(ref.Bucket),
		Key:    aws.String(ref.Key),
	})
	if err != nil {
		return objectstore.ObjectInfo{}, mapError("head object", err)
	}
	result := objectstore.ObjectInfo{
		ObjectRef:   ref,
		Size:        aws.ToInt64(output.ContentLength),
		ETag:        aws.ToString(output.ETag),
		VersionID:   aws.ToString(output.VersionId),
		ContentType: aws.ToString(output.ContentType),
		Metadata:    objectstore.CloneMetadata(output.Metadata),
	}
	if output.LastModified != nil {
		result.LastModified = *output.LastModified
	}
	return result, nil
}

func (store *Store) PresignDownload(ctx context.Context, input objectstore.PresignDownloadInput) (objectstore.PresignedRequest, error) {
	if err := input.Validate(); err != nil {
		return objectstore.PresignedRequest{}, err
	}
	output, err := store.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(input.Bucket),
		Key:    aws.String(input.Key),
	}, func(options *s3.PresignOptions) {
		options.Expires = input.Expires
	})
	if err != nil {
		return objectstore.PresignedRequest{}, mapError("presign download", err)
	}
	return presignedRequest(output, store.now().Add(input.Expires)), nil
}

func (store *Store) DeleteObject(ctx context.Context, ref objectstore.ObjectRef) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	_, err := store.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(ref.Bucket),
		Key:    aws.String(ref.Key),
	})
	if err != nil {
		return mapError("delete object", err)
	}
	return nil
}

func (store *Store) PutObject(ctx context.Context, input objectstore.PutObjectInput) (objectstore.CompleteResult, error) {
	if err := input.Validate(); err != nil {
		return objectstore.CompleteResult{}, err
	}
	request := &s3.PutObjectInput{
		Bucket:        aws.String(input.Bucket),
		Key:           aws.String(input.Key),
		Body:          input.Body,
		ContentLength: aws.Int64(input.Size),
		Metadata:      objectstore.CloneMetadata(input.Metadata),
	}
	if input.ContentType != "" {
		request.ContentType = aws.String(input.ContentType)
	}
	output, err := store.client.PutObject(ctx, request, func(o *s3.Options) {
		// Streaming bodies are not seekable; skip SHA256 payload hashing.
		o.APIOptions = append(o.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
	})
	if err != nil {
		return objectstore.CompleteResult{}, mapError("put object", err)
	}
	return objectstore.CompleteResult{
		ETag:      aws.ToString(output.ETag),
		VersionID: aws.ToString(output.VersionId),
	}, nil
}

func (store *Store) ListObjects(ctx context.Context, input objectstore.ListObjectsInput) (objectstore.ListObjectsResult, error) {
	if err := input.Validate(); err != nil {
		return objectstore.ListObjectsResult{}, err
	}
	request := &s3.ListObjectsV2Input{
		Bucket: aws.String(input.Bucket),
	}
	if input.Prefix != "" {
		request.Prefix = aws.String(input.Prefix)
	}
	if input.MaxKeys > 0 {
		request.MaxKeys = aws.Int32(input.MaxKeys)
	}
	if input.ContinuationToken != "" {
		request.ContinuationToken = aws.String(input.ContinuationToken)
	}
	output, err := store.client.ListObjectsV2(ctx, request)
	if err != nil {
		return objectstore.ListObjectsResult{}, mapError("list objects", err)
	}
	entries := make([]objectstore.ObjectEntry, 0, len(output.Contents))
	for _, obj := range output.Contents {
		entry := objectstore.ObjectEntry{
			Key:  aws.ToString(obj.Key),
			Size: aws.ToInt64(obj.Size),
			ETag: aws.ToString(obj.ETag),
		}
		if obj.LastModified != nil {
			entry.LastModified = *obj.LastModified
		}
		entries = append(entries, entry)
	}
	return objectstore.ListObjectsResult{
		Objects:           entries,
		ContinuationToken: aws.ToString(output.NextContinuationToken),
		IsTruncated:       aws.ToBool(output.IsTruncated),
	}, nil
}

func presignedRequest(value *v4.PresignedHTTPRequest, expiresAt time.Time) objectstore.PresignedRequest {
	return objectstore.PresignedRequest{
		Method:    value.Method,
		URL:       value.URL,
		Headers:   value.SignedHeader.Clone(),
		ExpiresAt: expiresAt,
	}
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var kind error
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		kind = classifyCode(apiError.ErrorCode(), apiError.ErrorFault())
	}
	if kind == nil {
		var responseError *smithyhttp.ResponseError
		if errors.As(err, &responseError) {
			kind = classifyStatus(responseError.HTTPStatusCode())
		}
	}
	return &objectstore.ProviderError{Operation: operation, Kind: kind, Err: err}
}

func classifyCode(code string, fault smithy.ErrorFault) error {
	switch strings.ToLower(code) {
	case "nosuchkey", "nosuchbucket", "nosuchupload", "notfound", "xnosuchbucket":
		return objectstore.ErrNotFound
	case "preconditionfailed", "conditionalrequestconflict":
		return objectstore.ErrPreconditionFailed
	case "accessdenied", "invalidaccesskeyid", "signaturedoesnotmatch", "expiredtoken", "invalidtoken", "unauthorized":
		return objectstore.ErrUnauthorized
	case "invalidargument", "invalidrequest", "invalidpart", "invalidpartorder", "entitytoosmall", "malformedxml", "badrequest":
		return objectstore.ErrInvalidInput
	case "slowdown", "serviceunavailable", "internalerror", "requesttimeout", "requesttimeoutexception", "throttling", "throttlingexception":
		return objectstore.ErrTemporary
	}
	if fault == smithy.FaultServer {
		return objectstore.ErrTemporary
	}
	return nil
}

func classifyStatus(status int) error {
	switch {
	case status == http.StatusBadRequest:
		return objectstore.ErrInvalidInput
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return objectstore.ErrUnauthorized
	case status == http.StatusNotFound:
		return objectstore.ErrNotFound
	case status == http.StatusPreconditionFailed || status == http.StatusConflict:
		return objectstore.ErrPreconditionFailed
	case status == http.StatusTooManyRequests || status >= 500:
		return objectstore.ErrTemporary
	default:
		return nil
	}
}

var _ objectstore.Store = (*Store)(nil)
