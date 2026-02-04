package imgproxy

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"time"
)

func splitHashResize(s string) (hash, resize string) {
	parts := strings.SplitN(s, "@", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return s, ""
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func originOf(u string) string {
	// грубый парсер: scheme://host
	// (достаточно для typical Location: /path)
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return u[:i+3] + rest[:j]
		}
		return u
	}
	return u
}

func env(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
func envDuration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
func envInt64(k string, def int64) int64 {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}
