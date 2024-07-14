package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"cloud.google.com/go/storage"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

const rmCacheMaxSize = 16 * 1024 * 1024 // 16 MB

var rmCacheSize = 0
var rmCache = make(map[string]readmeCacheEntry)
var rmKeys = make([]string, 0)

type readmeCacheEntry struct {
	markdown  []byte
	timestamp time.Time
}

func renderReadme(ctx context.Context, w *bufio.Writer, attrs *storage.ObjectAttrs) {
	if markdown, err := fetchReadme(ctx, attrs); err != nil {
		slog.Error("failed to fetch readme", "err", err)
	} else if err := md.Convert(markdown, w); err != nil {
		slog.Error("failed to render readme", "err", err)
	}
}

func fetchReadme(ctx context.Context, attrs *storage.ObjectAttrs) ([]byte, error) {
	var key = cacheKey(attrs)
	if entry, ok := rmCache[key]; ok && !entry.timestamp.After(attrs.Updated) {
		return entry.markdown, nil
	}

	slog.Info("fetching readme", "bucket", attrs.Bucket, "name", attrs.Name)

	obj := client.Bucket(attrs.Bucket).Object(attrs.Name)
	reader, err := obj.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("newReader: %w", err)
	}
	defer reader.Close()

	var readme bytes.Buffer
	if _, err = readme.ReadFrom(reader); err != nil {
		return nil, fmt.Errorf("readFrom: %w", err)
	}

	var markdown = readme.Bytes()

	// Insert in cache
	var _, wasInCache = rmCache[key]
	rmCache[key] = readmeCacheEntry{
		markdown:  markdown,
		timestamp: attrs.Updated,
	}

	// Purge cache
	if !wasInCache {
		rmCacheSize += len(markdown)
		rmKeys = append(rmKeys, key)

		slog.Info("purging readme cache", "size", rmCacheSize)
		for rmCacheSize > rmCacheMaxSize && len(rmKeys) > 0 {
			var key = rmKeys[0]
			rmCacheSize -= len(rmCache[key].markdown)
			delete(rmCache, key)
			rmKeys = rmKeys[1:]
		}
	}

	return markdown, nil
}

func cacheKey(attrs *storage.ObjectAttrs) string {
	return attrs.Bucket + "/" + attrs.Name
}
