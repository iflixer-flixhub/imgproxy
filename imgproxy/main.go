package imgproxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/go-chi/chi/v5"

	"github.com/davidbyttow/govips/v2/vips"
)

func main() {
	vips.Startup(nil)
	defer vips.Shutdown()

	app, err := newApp()
	if err != nil {
		log.Fatal(err)
	}

	r := chi.NewRouter()
	r.Get("/readyz", app.handleReadyz)
	r.Get("/sss/{type}/{id}/{md5}", app.handleSSS)

	addr := env("LISTEN", env("LISTEN", ""))
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}

func (a *App) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	_, _ = w.Write([]byte("ok"))
}

func (a *App) handleSSS(w http.ResponseWriter, r *http.Request) {
	t0 := time.Now()

	typ := chi.URLParam(r, "type")
	idStr := chi.URLParam(r, "id")
	md5raw := chi.URLParam(r, "md5")

	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", 400)
		return
	}

	md5clean := md5raw
	if i := strings.IndexByte(md5clean, '?'); i >= 0 {
		md5clean = md5clean[:i]
	}
	if i := strings.IndexByte(md5clean, '.'); i >= 0 {
		md5clean = md5clean[:i]
	}

	hash, resize := splitHashResize(md5clean)

	origKey := fmt.Sprintf("%s/%s/%d/%s", a.prefix, typ, id, hash)
	fullKey := fmt.Sprintf("%s/%s/%d/%s", a.prefix, typ, id, md5clean)

	// 1) try original in storage
	origBody, origCT, origETag, ok, err := a.getObject(r.Context(), origKey)
	if err != nil {
		http.Error(w, "storage error", 424)
		return
	}

	var data []byte
	var contentType string
	var source string
	var responseCode = 200

	if ok {
		data = origBody
		contentType = origCT
		source = "orig-cache"
	} else {
		// 2) resolve remote url via DB
		remoteURL, err := a.remoteURLFromDB(r.Context(), typ, id, hash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "not found", 404)
				return
			}
			http.Error(w, "db error", 424)
			return
		}

		body, ct, code, err := a.fetchRemoteWithRedirects(r.Context(), remoteURL)
		if err != nil {
			http.Error(w, "fetch error", 424)
			return
		}
		if code == 404 {
			// твоя логика: "заглушка" с 404
			http.Error(w, "not found", 404)
			return
		}
		responseCode = code

		data = body
		contentType = ct
		source = "remote"

		// upload original
		et, err := a.putObject(r.Context(), origKey, contentType, data)
		if err != nil {
			http.Error(w, "upload orig error", 424)
			return
		}
		origETag = et
	}

	// 3) resize if requested
	if resize != "" {
		resized, err := resizeToWebP(data, resize)
		if err != nil {
			http.Error(w, "resize error", 400)
			return
		}
		ct := "image/webp"
		etag, err := a.putObject(r.Context(), fullKey, ct, resized)
		if err != nil {
			http.Error(w, "upload resized error", 424)
			return
		}
		writeCommon(w, r, ct, etag, "resized-"+source, time.Since(t0))
		w.WriteHeader(responseCode)
		_, _ = w.Write(resized)
		return
	}

	// 4) return original
	writeCommon(w, r, contentType, origETag, "orig-"+source, time.Since(t0))
	w.WriteHeader(responseCode)
	_, _ = w.Write(data)
}

func (a *App) remoteURLFromDB(ctx context.Context, typ string, id int, wantHash string) (string, error) {
	// IMPORTANT: подстрой названия таблиц/полей под твои реальные
	switch typ {
	case "videos":
		var img, backdrop sql.NullString
		err := a.db.QueryRowContext(ctx,
			`SELECT img, backdrop FROM videos WHERE id = ? LIMIT 1`, id,
		).Scan(&img, &backdrop)
		if err != nil {
			return "", err
		}
		if img.Valid && md5hex(img.String) == wantHash {
			return img.String, nil
		}
		if backdrop.Valid && md5hex(backdrop.String) == wantHash {
			return backdrop.String, nil
		}
		return "", sql.ErrNoRows

	case "actors":
		var poster sql.NullString
		err := a.db.QueryRowContext(ctx,
			`SELECT poster_url FROM actors WHERE id = ? LIMIT 1`, id,
		).Scan(&poster)
		if err != nil {
			return "", err
		}
		if poster.Valid && md5hex(poster.String) == wantHash {
			return poster.String, nil
		}
		return "", sql.ErrNoRows

	case "directors":
		var poster sql.NullString
		err := a.db.QueryRowContext(ctx,
			`SELECT poster_url FROM directors WHERE id = ? LIMIT 1`, id,
		).Scan(&poster)
		if err != nil {
			return "", err
		}
		if poster.Valid && md5hex(poster.String) == wantHash {
			return poster.String, nil
		}
		return "", sql.ErrNoRows

	case "screenshots":
		var u sql.NullString
		err := a.db.QueryRowContext(ctx,
			`SELECT url FROM screenshots WHERE id = ? LIMIT 1`, id,
		).Scan(&u)
		if err != nil {
			return "", err
		}
		if u.Valid && md5hex(u.String) == wantHash {
			return u.String, nil
		}
		return "", sql.ErrNoRows

	default:
		return "", sql.ErrNoRows
	}
}

func (a *App) fetchRemoteWithRedirects(ctx context.Context, startURL string) ([]byte, string, int, error) {
	cur := startURL

	for i := 0; i <= a.maxRedir; i++ {
		req, err := http.NewRequestWithContext(ctx, "GET", cur, nil)
		if err != nil {
			return nil, "", 0, err
		}

		// ВАЖНО: не следуем редиректам автоматически — хотим видеть Location
		client := *a.httpClient
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, "", 0, err
		}

		// 3xx redirect
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			resp.Body.Close()

			if strings.Contains(loc, "no-poster.gif") {
				return nil, "", 404, nil
			}
			if loc == "" {
				return nil, "", 0, fmt.Errorf("redirect without location")
			}
			// relative -> absolute
			next := loc
			if strings.HasPrefix(loc, "/") {
				// очень грубо, но обычно хватает; при желании нормализовать через url.Parse
				next = originOf(cur) + loc
			}
			cur = next
			continue
		}

		if resp.StatusCode >= 400 {
			resp.Body.Close()
			if resp.StatusCode == 404 {
				return nil, "", 404, nil
			}
			return nil, "", resp.StatusCode, nil
		}

		limited := io.LimitReader(resp.Body, a.maxFetch+1)
		b, err := io.ReadAll(limited)
		resp.Body.Close()
		if err != nil {
			return nil, "", 0, err
		}
		if int64(len(b)) > a.maxFetch {
			return nil, "", 0, fmt.Errorf("remote too large")
		}

		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = http.DetectContentType(b)
		}
		if j := strings.Index(ct, ";"); j >= 0 {
			ct = strings.TrimSpace(ct[:j])
		}
		return b, ct, resp.StatusCode, nil
	}

	return nil, "", 0, fmt.Errorf("too many redirects")
}

func resizeToWebP(input []byte, resize string) ([]byte, error) {
	w, h := 0, 0
	if strings.HasPrefix(resize, "h") {
		v, err := strconv.Atoi(strings.TrimPrefix(resize, "h"))
		if err != nil || v <= 0 {
			return nil, errors.New("bad resize")
		}
		h = v
	} else {
		v, err := strconv.Atoi(resize)
		if err != nil || v <= 0 {
			return nil, errors.New("bad resize")
		}
		w = v
	}
	if w > 1000 || h > 1000 {
		return nil, errors.New("too big")
	}

	img, err := vips.NewImageFromBuffer(input)
	if err != nil {
		return nil, err
	}
	defer img.Close()

	// keep aspect ratio: resize by ratio
	if w > 0 {
		ratio := float64(w) / float64(img.Width())
		if ratio > 1 {
			ratio = 1
		}
		if err := img.Resize(ratio, vips.KernelLanczos3); err != nil {
			return nil, err
		}
	} else if h > 0 {
		ratio := float64(h) / float64(img.Height())
		if ratio > 1 {
			ratio = 1
		}
		if err := img.Resize(ratio, vips.KernelLanczos3); err != nil {
			return nil, err
		}
	}

	ep := vips.NewWebpExportParams()
	ep.Quality = 80
	out, _, err := img.ExportWebp(ep)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func writeCommon(w http.ResponseWriter, r *http.Request, ct, etag, source string, dur time.Duration) {
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-B-Source", source)
	w.Header().Set("X-Req-Ms", fmt.Sprintf("%.2f", float64(dur.Microseconds())/1000.0))
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
}
