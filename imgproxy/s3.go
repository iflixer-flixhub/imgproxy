package imgproxy

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func (a *App) getObject(ctx context.Context, key string) ([]byte, string, string, bool, error) {
	out, err := a.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) || strings.Contains(err.Error(), "NoSuchKey") || strings.Contains(err.Error(), "NotFound") {
			return nil, "", "", false, nil
		}
		return nil, "", "", false, err
	}
	defer out.Body.Close()

	b, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", "", false, err
	}
	ct := aws.ToString(out.ContentType)
	etag := strings.Trim(aws.ToString(out.ETag), `"`)
	return b, ct, etag, true, nil
}

func (a *App) putObject(ctx context.Context, key, contentType string, data []byte) (string, error) {
	out, err := a.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(a.bucket),
		Key:          aws.String(key),
		Body:         bytes.NewReader(data),
		ContentType:  aws.String(contentType),
		CacheControl: aws.String("public, max-age=31536000, immutable"),
	})
	if err != nil {
		return "", err
	}
	etag := strings.Trim(aws.ToString(out.ETag), `"`)
	if etag == "" {
		sum := md5.Sum(data)
		etag = hex.EncodeToString(sum[:])
	}
	return etag, nil
}
