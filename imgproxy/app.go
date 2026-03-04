package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type App struct {
	db *sql.DB

	s3     *s3.Client
	bucket string
	prefix string

	httpClient *http.Client
	maxFetch   int64
	maxRedir   int
	uploadSem  chan struct{}
}

func newApp() (*App, error) {
	// --- MySQL
	dsn := env("MYSQL_DSN", "")
	if dsn == "" {
		return nil, errors.New("missing MYSQL_DSN")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("mysql ping: %w", err)
	}

	// --- S3/R2
	accessKey := env("S3_ACCESS_KEY", "")
	secretKey := env("S3_SECRET_KEY", "")
	if secretKey == "" {
		secretKeyFile := env("S3_SECRET_KEY_FILE", "")
		sk, err := os.ReadFile(secretKeyFile)
		if err == nil {
			secretKey = strings.TrimSpace(string(sk))
		}
	}
	endpoint := env("S3_ENDPOINT", "")
	region := env("S3_REGION", "auto")
	bucket := env("S3_BUCKET", "")
	prefix := env("S3_PREFIX", "cdnhub/sss")

	if bucket == "" || endpoint == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("missing S3_* envs")
	}

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	s3c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	timeout := envDuration("HTTP_TIMEOUT", 10*time.Second)

	app := &App{
		db: db,

		s3:     s3c,
		bucket: bucket,
		prefix: strings.TrimSuffix(prefix, "/"),

		httpClient: &http.Client{Timeout: timeout},
		maxFetch:   envInt64("MAX_FETCH_BYTES", 10<<20),
		maxRedir:   5,
		uploadSem:  make(chan struct{}, 32),
	}

	if envBool("S3_INIT_CHECK", true) {
		if err := app.checkS3Access(context.Background()); err != nil {
			return nil, err
		}
	} else {
		log.Printf("s3 init access checks are disabled (S3_INIT_CHECK=false)")
	}

	return app, nil
}

func (a *App) checkS3Access(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	probePrefix := strings.TrimSuffix(a.prefix, "/")
	if probePrefix == "" {
		probePrefix = "cdnhub/sss"
	}
	probeBase := fmt.Sprintf("%s/__init_access_probe/%d", probePrefix, time.Now().UnixNano())
	readProbeKey := probeBase + "-read-miss"
	writeProbeKey := probeBase + "-write"

	if err := a.checkS3Read(checkCtx, readProbeKey); err != nil {
		return err
	}
	if err := a.checkS3Write(checkCtx, writeProbeKey); err != nil {
		return err
	}
	if err := a.checkS3Head(checkCtx, readProbeKey); err != nil {
		if isS3AccessDenied(err) {
			log.Printf("s3 head access denied, continue without HeadObject: %v", err)
		} else {
			return err
		}
	}

	log.Printf("s3 access checks passed: read/write ok")
	return nil
}

func (a *App) checkS3Read(ctx context.Context, key string) error {
	out, err := a.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil
		}
		if isS3AccessDenied(err) {
			return fmt.Errorf("s3 read access denied (GetObject): %w", err)
		}
		return fmt.Errorf("s3 read check failed (GetObject): %w", err)
	}
	defer out.Body.Close()
	return nil
}

func (a *App) checkS3Write(ctx context.Context, key string) error {
	_, err := a.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader([]byte("ok")),
		ContentType: aws.String("text/plain"),
	})
	if err != nil {
		if isS3AccessDenied(err) {
			return fmt.Errorf("s3 write access denied (PutObject): %w", err)
		}
		return fmt.Errorf("s3 write check failed (PutObject): %w", err)
	}

	if _, err := a.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	}); err != nil {
		log.Printf("s3 access check cleanup warning (DeleteObject): key=%s err=%v", key, err)
	}

	return nil
}

func (a *App) checkS3Head(ctx context.Context, key string) error {
	_, err := a.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err == nil || isS3NotFound(err) {
		log.Println("s3 HeadObject OK")
		return nil
	}
	if isS3AccessDenied(err) {
		return fmt.Errorf("s3 head access denied (HeadObject): %w", err)
	}
	return fmt.Errorf("s3 head check failed (HeadObject): %w", err)
}

func isS3NotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "NotFound")
}

func isS3AccessDenied(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "accessdenied") || strings.Contains(msg, "forbidden") || strings.Contains(msg, "statuscode: 403")
}
