package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

	return &App{
		db: db,

		s3:     s3c,
		bucket: bucket,
		prefix: strings.TrimSuffix(prefix, "/"),

		httpClient: &http.Client{Timeout: timeout},
		maxFetch:   envInt64("MAX_FETCH_BYTES", 10<<20),
		maxRedir:   5,
		uploadSem:  make(chan struct{}, 32),
	}, nil
}
