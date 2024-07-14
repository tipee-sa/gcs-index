package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/storage"
)

type MountPoint struct {
	Path   string
	Bucket string
	Prefix string
}

const defaultCacheControl = "public, max-age=60, stale-while-revalidate=86400, stale-if-error=86400"

var client *storage.Client
var mountPoints []MountPoint

var port = flag.Int("port", 8080, "port to listen on")
var readme = flag.Bool("readme", false, "enable README.md rendering")
var skipReadme = flag.Bool("skip-readme", false, "skip README.md in directory listings")
var socket = flag.String("socket", "", "socket to listen on")
var socketUmask = flag.Int("socket-umask", -1, "umask for the socket file")
var verbose = flag.Bool("v", false, "enable verbose logging")
var versionSort = flag.Bool("version-sort", false, "sort directory listings using a semver-aware algorithm")

func main() {
	flag.Parse()

	if *verbose {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	prepareMountPoints()
	slog.Info("initializing", "mountPoints", mountPoints)

	var err error
	client, err = storage.NewClient(context.Background(), storage.WithJSONReads())
	if err != nil {
		slog.Error("failed to create storage client", "err", err)
		os.Exit(4)
	}

	server := &http.Server{}
	http.HandleFunc("/", handle)

	var listener net.Listener
	if *socket != "" {
		var oldUmask = -1
		if *socketUmask >= 0 {
			slog.Info("setting umask", "umask", *socketUmask)
			oldUmask = syscall.Umask(*socketUmask)
		}
		slog.Info("listening on socket", "socket", *socket)
		listener, err = net.Listen("unix", *socket)
		if oldUmask >= 0 {
			syscall.Umask(oldUmask)
		}
	} else {
		slog.Info("listening on port", "port", *port)
		listener, err = net.Listen("tcp", fmt.Sprintf(":%d", *port))
	}
	if err != nil {
		slog.Error("failed to listen", "err", err)
		os.Exit(3)
	}

	go func() {
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(5)
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
		os.Exit(6)
	}
	slog.Info("shutdown completed")
}

func prepareMountPoints() {
	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s path:bucket:prefix [path:bucket:prefix ...]\n", os.Args[0])
		os.Exit(1)
	}

	for _, arg := range args {
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

	// Longest path first
	slices.SortFunc(mountPoints, func(a, b MountPoint) int {
		if len(a.Path) != len(b.Path) {
			return len(b.Path) - len(a.Path)
		} else {
			return strings.Compare(a.Path, b.Path)
		}
	})
}

func handle(w http.ResponseWriter, r *http.Request) {
	slog.Info("request", "path", r.URL.Path, "method", r.Method)

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		slog.Warn("method not allowed", "method", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if strings.HasSuffix(r.URL.Path, "/") {
		handleIndex(w, r)
	} else {
		handleObject(w, r)
	}
}

func findMountPoint(path string) *MountPoint {
	for i := 0; i < len(mountPoints); i++ {
		if strings.HasPrefix(path, mountPoints[i].Path) {
			return &mountPoints[i]
		}
	}
	return nil
}
