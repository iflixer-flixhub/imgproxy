package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

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

func (a *App) uploadAsync(key, ct string, data []byte) {
	// ограничиваем параллелизм
	select {
	case a.uploadSem <- struct{}{}:
		// ok
	default:
		log.Printf("async upload skipped (busy): %s", key)
		return
	}

	go func() {
		defer func() { <-a.uploadSem }()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if _, err := a.putObject(ctx, key, ct, data); err != nil {
			log.Printf("async upload failed key=%s err=%v", key, err)
		} else {
			log.Printf("async upload ok key=%s", key)
		}
	}()
}

func (a *App) headObject(ctx context.Context, key string) (ct, etag string, size int64, ok bool, err error) {
	out, err := a.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &a.bucket,
		Key:    &key,
	})
	if err != nil {
		// подстрой под свой детектор NotFound как в getObject
		msg := err.Error()
		if strings.Contains(msg, "NotFound") || strings.Contains(msg, "NoSuchKey") {
			return "", "", 0, false, nil
		}
		return "", "", 0, false, err
	}

	ct = aws.ToString(out.ContentType)
	etag = strings.Trim(aws.ToString(out.ETag), `"`)
	size = *out.ContentLength
	return ct, etag, size, true, nil
}

func (a *App) streamObject(ctx context.Context, key string, w http.ResponseWriter) error {
	out, err := a.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &a.bucket,
		Key:    &key,
	})
	if err != nil {
		return err
	}
	defer out.Body.Close()

	_, err = io.Copy(w, out.Body)
	return err
}

// Возвращает true если ответ уже отправлен (304 или 200 из кеша), иначе false.
func (a *App) serveFromS3IfPresent(
	w http.ResponseWriter,
	r *http.Request,
	key string,
	source string,
	start time.Time,
) (bool, error) {

	ct, etag, size, ok, err := a.headObject(r.Context(), key)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	// 304 без скачивания тела
	if inm := strings.Trim(r.Header.Get("If-None-Match"), `"`); inm != "" && etag != "" && inm == etag {
		// Можно тоже добавить полезные заголовки (ETag, Cache-Control)
		writeCommon(w, r, ct, etag, source, time.Since(start))
		w.WriteHeader(http.StatusNotModified)
		return true, nil
	}

	// Заголовки до передачи тела
	writeCommon(w, r, ct, etag, source, time.Since(start))
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}

	w.WriteHeader(http.StatusOK)

	// Стримим тело (не аллоцируем весь файл)
	if err := a.streamObject(r.Context(), key, w); err != nil {
		// Тут уже поздно делать http.Error (заголовки отправлены).
		// Остаётся только лог.
		return true, err
	}
	return true, nil
}
