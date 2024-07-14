package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func handleObject(w http.ResponseWriter, r *http.Request) {
	var mountPoint = findMountPoint(r.URL.Path)
	if mountPoint == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	bucket := client.Bucket(mountPoint.Bucket)
	obj := bucket.Object(mountPoint.Prefix + strings.TrimPrefix(r.URL.Path, mountPoint.Path))

	attrs, err := obj.Attrs(r.Context())
	if err != nil {
		slog.Error("failed to get object attributes",
			"bucket", obj.BucketName(),
			"object", obj.ObjectName(),
			"err", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var h = w.Header()

	h.Set("ETag", fmt.Sprintf("\"%s\"", attrs.Etag))
	h.Set("Last-Modified", attrs.Updated.Format(http.TimeFormat))

	// Conditional requests
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		inm = strings.Trim(strings.TrimPrefix(inm, "W/"), "\"")
		if inm == attrs.Etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if t, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); err == nil {
		if !attrs.Updated.Truncate(time.Second).After(t) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	// Set headers
	h.Set("Content-Length", fmt.Sprintf("%d", attrs.Size))
	setHeaderIfNotEmpty(h, "Content-Type", attrs.ContentType)
	setHeaderIfNotEmpty(h, "Content-Encoding", attrs.ContentEncoding)
	setHeaderIfNotEmpty(h, "Content-Disposition", attrs.ContentDisposition)
	if !setHeaderIfNotEmpty(h, "Cache-Control", attrs.CacheControl) {
		h.Set("Cache-Control", defaultCacheControl)
	}

	for k, v := range attrs.Metadata {
		setHeaderIfNotEmpty(h, k, v)
	}

	h.Set("X-Fetched-At", time.Now().Format(http.TimeFormat))

	if r.Method == http.MethodHead {
		return
	}

	slog.Info("serving object", "bucket", obj.BucketName(), "object", obj.ObjectName())
	reader, err := obj.NewReader(r.Context())
	if err != nil {
		slog.Error("failed to read object",
			"bucket", obj.BucketName(),
			"object", obj.ObjectName(),
			"err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	// Reset Content-Length (just in case?)
	h.Set("Content-Length", fmt.Sprintf("%d", reader.Attrs.Size))

	if _, err := io.Copy(w, reader); err != nil {
		slog.Error("failed to write object", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func setHeaderIfNotEmpty(h http.Header, key, value string) bool {
	if value != "" {
		h.Set(key, value)
		return true
	}
	return false
}
