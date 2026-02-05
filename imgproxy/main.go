package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chai2010/webp"
	"github.com/disintegration/imaging"
	_ "github.com/go-sql-driver/mysql"

	"github.com/go-chi/chi/v5"
)

func main() {

	app, err := newApp()
	if err != nil {
		log.Fatal(err)
	}

	r := chi.NewRouter()
	r.Get("/readyz", app.Readyz)
	r.Head("/readyz", app.Readyz)
	r.Get("/healthz", app.Healthz)
	r.Head("/healthz", app.Healthz)
	r.Get("/sss/{type}/{id}/{md5}", app.handleSSS)

	addr := env("LISTEN", ":80")
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}

func (a *App) Healthz(w http.ResponseWriter, _ *http.Request) {
	// живой ли апп
	w.WriteHeader(http.StatusOK)
}

func (a *App) Readyz(w http.ResponseWriter, r *http.Request) {
	// живой ли апп и готов ли к работе (БД, хранилище)
	// ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	// defer cancel()
	// if err := db.PingContext(ctx); err != nil {
	// 	http.Error(w, "db not ready", 503)
	// 	return
	// }
	w.WriteHeader(http.StatusOK)
}

func (a *App) handleSSS(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	var startTimeLap time.Time
	logLap(startTime, &startTimeLap, "start")

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

	log.Printf("GET: %s", fullKey)

	// try resized in storage first (optimization)
	served, err := a.serveFromS3IfPresent(w, r, fullKey, "resized-cache", startTime)
	logLap(startTime, &startTimeLap, "s3 head+maybe-get resized")
	if err != nil {
		log.Println("s3 serve resized error:", err)
		// если served=true, ответ мог уже частично уйти; безопаснее просто выйти
		if served {
			return
		}
		http.Error(w, "storage error", 424)
		return
	}
	if served {
		return
	}

	// try original in storage
	origBody, origCT, _, ok, err := a.getObject(r.Context(), origKey)
	logLap(startTime, &startTimeLap, "s3 get orig")
	if err != nil {
		log.Println("error s3 get orig", err)
		http.Error(w, "storage error", 424)
		return
	}

	var data []byte
	var contentType string
	var source string
	var responseCode = 200

	if ok {
		log.Println("s3 get orig ok")
		data = origBody
		contentType = origCT
		source = "orig-cache"
	} else {
		log.Println("s3 get orig 404")
		// 2) resolve remote url via DB
		remoteURL, err := a.remoteURLFromDB(r.Context(), typ, id, hash)
		logLap(startTime, &startTimeLap, "db get remote url")
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				log.Println("db - not found hash", hash)
				http.Error(w, "not found", 404)
				return
			}
			log.Println("db error", err)
			http.Error(w, "db error", 424)
			return
		}

		body, ct, code, err := a.fetchRemoteWithRedirects(r.Context(), remoteURL)
		logLap(startTime, &startTimeLap, "fetch orig url")
		if err != nil {
			log.Println("fetch error", remoteURL, err)
			return
		}
		if code == 404 {
			// твоя логика: "заглушка" с 404
			log.Println("fetch 404", remoteURL, err)
			http.Error(w, "not found", 404)
			return
		}
		responseCode = code

		data = body
		contentType = ct
		source = "remote"

		// upload original - асинхронно
		a.uploadAsync(origKey, contentType, data)
	}

	// 3) resize if requested
	if resize != "" {
		log.Printf("resizing to %s", resize)
		resized, err := resizeToWebP(data, resize)
		logLap(startTime, &startTimeLap, "resize to webp")
		if err != nil {
			log.Println("resize error", err)
			http.Error(w, "resize error", 400)
			return
		}

		ct := "image/webp"
		// upload resized - асинхронно. etag пустой при этом но сгенерится при повторном запросе
		a.uploadAsync(fullKey, ct, resized)

		localEtag := md5hex(string(resized))
		writeCommon(w, r, ct, localEtag, "resized-"+source, time.Since(startTime))
		w.WriteHeader(responseCode)
		_, _ = w.Write(resized)
		return
	}

	logLap(startTime, &startTimeLap, "total http process")
	writeCommon(w, r, contentType, md5hex(string(data)), "orig-"+source, time.Since(startTime))
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
			return nil, fmt.Errorf("bad resize")
		}
		if v%100 != 0 {
			return nil, fmt.Errorf("bad resize: height must be multiple of 100")
		}
		h = v
	} else {
		v, err := strconv.Atoi(resize)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("bad resize")
		}
		if v%100 != 0 {
			return nil, fmt.Errorf("bad resize: width must be multiple of 100")
		}
		w = v
	}
	if w > 1000 || h > 1000 {
		return nil, fmt.Errorf("too big")
	}

	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}

	// preserve aspect ratio if one side is 0
	outImg := imaging.Resize(img, w, h, imaging.Lanczos)

	var buf bytes.Buffer
	// quality 80 примерно как у тебя
	if err := webp.Encode(&buf, outImg, &webp.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
