//go:build ignore

package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("easyshare-dev", "replace-with-a-long-random-development-secret", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://127.0.0.1:9000")
		o.UsePathStyle = true
	})
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String("easyshare"),
	})
	if err != nil {
		fmt.Println("create bucket:", err)
		return
	}
	fmt.Println("bucket 'easyshare' created successfully")
}
