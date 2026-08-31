//go:build ignore

// 运维脚本：建 RustFS bucket。凭据只从环境变量读，不落硬编码——
// 唯一来源是 deploy/rustfs/.env（ADR-0007 不变量 1）。
//
//	set -a; . deploy/rustfs/.env; set +a
//	go run scripts/create_bucket.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	accessKey := os.Getenv("RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("RUSTFS_SECRET_KEY")
	if accessKey == "" || secretKey == "" {
		fmt.Println("缺少 RUSTFS_ACCESS_KEY / RUSTFS_SECRET_KEY，请先加载 deploy/rustfs/.env")
		os.Exit(1)
	}
	endpoint := os.Getenv("RUSTFS_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:9000"
	}
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
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
