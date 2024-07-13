package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type MountPoint struct {
	Path   string
	Bucket string
	Prefix string
}

var client *storage.Client
var mountPoints []MountPoint

//go:embed page.html
var pageHtml []byte

func main() {
	prepareMountPoints()
	slog.Info("initializing", "mountPoints", mountPoints)

	var err error
	client, err = storage.NewClient(context.Background(), storage.WithJSONReads())
	if err != nil {
		slog.Error("failed to create storage client", "err", err)
		os.Exit(3)
	}

	server := &http.Server{Addr: fmt.Sprintf(":%s", envOr("PORT", "8080"))}
	http.HandleFunc("/", handle)

	go func() {
		slog.Info("server started", "addr", server.Addr)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(4)
		}
		slog.Warn("server stopped")
	}()

	// Wait for a signal to stop the server
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	slog.Warn("shutting down server")

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownRelease()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
		os.Exit(5)
	}
	slog.Info("shutdown completed")
}

func envOr(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	} else {
		return defaultValue
	}
}

func prepareMountPoints() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s path:bucket:prefix [path:bucket:prefix ...]\n", os.Args[0])
		os.Exit(1)
	}

	for _, arg := range os.Args[1:] {
		mountPointParts := strings.SplitN(arg, ":", 3)
		if len(mountPointParts) != 3 {
			slog.Error("invalid mount point", "arg", arg, "reason", "expected 'path:bucket:prefix'")
			os.Exit(2)
		}

		// Normalize the path
		if !strings.HasPrefix(mountPointParts[0], "/") {
			mountPointParts[0] = "/" + mountPointParts[0]
		}
		if !strings.HasSuffix(mountPointParts[0], "/") {
			mountPointParts[0] += "/"
		}

		mountPoints = append(mountPoints, MountPoint{
			Path:   mountPointParts[0],
			Bucket: mountPointParts[1],
			Prefix: mountPointParts[2],
		})
	}

	slices.SortFunc(mountPoints, func(a, b MountPoint) int {
		if len(a.Path) != len(b.Path) {
			return len(b.Path) - len(a.Path)
		} else {
			return strings.Compare(a.Path, b.Path)
		}
	})
}

func handle(w http.ResponseWriter, r *http.Request) {
	slog.Info("request",
		"path", r.URL.Path,
		"method", r.Method,
		"remote", r.RemoteAddr,
		"forwarded-for", r.Header.Get("X-Forwarded-For"),
		"user-agent", r.UserAgent())

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		slog.Warn("method not allowed", "method", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Path == "/favicon.ico" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if strings.HasSuffix(r.URL.Path, "/") {
		handleIndex(w, r)
	} else {
		handleObject(w, r)
	}
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		// Directory index always returns 200 OK.
		return
	}

	var links []string
	if r.URL.Path != "/" {
		links = append(links, "../")
	}

	links = append(links, linksFromMountPoints(r.URL.Path)...)
	links = append(links, linksFromStorage(r.Context(), r.URL.Path)...)

	links = slices.Compact(links)
	slices.Sort(links)

	var output bytes.Buffer
	for _, link := range links {
		_, _ = output.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a><br>\n", link, link))
	}

	w.Write(pageHtml)
	w.Write(output.Bytes())
}

func linksFromMountPoints(path string) (links []string) {
	for _, mountPoint := range mountPoints {
		if mountPoint.Path != path && strings.HasPrefix(mountPoint.Path, path) {
			links = append(links, strings.SplitAfterN(strings.TrimPrefix(mountPoint.Path, path), "/", 2)[0])
		}
	}
	return
}

func linksFromStorage(ctx context.Context, path string) (links []string) {
	var mountPoint = findMountPoint(path)
	if mountPoint == nil {
		return
	}

	bucket := client.Bucket(mountPoint.Bucket)
	query := &storage.Query{
		Prefix:    mountPoint.Prefix + strings.TrimPrefix(path, mountPoint.Path),
		Delimiter: "/",
	}

	slog.Debug("listing objects", "bucket", mountPoint.Bucket, "query", query)

	objects := bucket.Objects(ctx, query)
	for {
		attrs, err := objects.Next()
		if err == iterator.Done {
			break
		} else if err != nil {
			slog.Error("failed to list objects", "err", err)
			break
		}

		if attrs.Name != "" {
			if attrs.Name != query.Prefix {
				links = append(links, strings.TrimPrefix(attrs.Name, query.Prefix))
			}
		} else if attrs.Prefix != "" {
			links = append(links, strings.TrimPrefix(attrs.Prefix, query.Prefix))
		} else {
			slog.Warn("unexpected object", "attrs", attrs)
		}
	}

	return
}

func findMountPoint(path string) *MountPoint {
	for i := len(mountPoints) - 1; i >= 0; i-- {
		if strings.HasPrefix(path, mountPoints[i].Path) {
			return &mountPoints[i]
		}
	}
	return nil
}

func handleObject(w http.ResponseWriter, r *http.Request) {
	var mountPoint = findMountPoint(r.URL.Path)
	if mountPoint == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	bucket := client.Bucket(mountPoint.Bucket)
	obj := bucket.Object(mountPoint.Prefix + strings.TrimPrefix(r.URL.Path, mountPoint.Path))

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

	setHeaderIfNotEmpty(w, "Content-Length", fmt.Sprintf("%d", reader.Attrs.Size))
	setHeaderIfNotEmpty(w, "Content-Type", reader.Attrs.ContentType)
	setHeaderIfNotEmpty(w, "Content-Encoding", reader.Attrs.ContentEncoding)
	setHeaderIfNotEmpty(w, "Last-Modified", reader.Attrs.LastModified.Format(http.TimeFormat))
	setHeaderIfNotEmpty(w, "Cache-Control", reader.Attrs.CacheControl)

	if r.Method == http.MethodHead {
		return
	}

	if _, err := io.Copy(w, reader); err != nil {
		slog.Error("failed to write object", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func setHeaderIfNotEmpty(w http.ResponseWriter, key, value string) {
	if value != "" {
		w.Header().Set(key, value)
	}
}
