package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/dustin/go-humanize"
	"google.golang.org/api/iterator"
)

type Item struct {
	Name        string            `json:"item"`
	Size        *uint64           `json:"size,omitempty"`
	Fingerprint *string           `json:"fingerprint,omitempty"`
	ContentType *string           `json:"content_type,omitempty"`
	Timestamp   *time.Time        `json:"timestamp,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

//go:embed page.html
var pageHtml []byte

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Last-Modified", time.Now().Truncate(time.Minute).Format(http.TimeFormat)) // Listing shows relative timestamps.
	w.Header().Set("Cache-Control", defaultCacheControl)
	w.Header().Set("Vary", "Accept")

	if r.Method == http.MethodHead {
		// Directory index always returns 200 OK.
		return
	}

	var jsonOutput = r.Header.Get("Accept") == "application/json" || r.URL.Query().Get("format") == "json"
	if jsonOutput {
		w.Header().Set("Content-Type", "application/json")
	}

	var items = make([]Item, 0)

	items = append(items, linksFromMountPoints(r.URL.Path)...)

	var storageLinks, readmeObject = linksFromStorage(r.Context(), r.URL.Path)
	items = append(items, storageLinks...)

	slices.SortStableFunc(items, sortLinks)
	items = slices.CompactFunc(items, func(a Item, b Item) bool {
		return a.Name == b.Name
	})

	var output = bufio.NewWriter(w)

	if jsonOutput {
		json.NewEncoder(output).Encode(items)
	} else {
		output.Write(pageHtml)
		output.WriteString("<main><table>\n")
		if r.URL.Path != "/" {
			output.WriteString("<tr><td><a href=\"../\">../</a></td></tr>\n")
		}
		for i, item := range items {
			// Split links with and without extra information into separate tables.
			if i > 0 && items[i-1].Size != nil && item.Size == nil {
				output.WriteString("</table><table>\n")
			}
			// Skip the favicon link on the root page.
			if item.Name == "favicon.ico" && r.URL.Path == "/" {
				continue
			}

			var extra = ""
			if item.Size != nil {
				extra += fmt.Sprintf("<td>%s</td>", humanize.IBytes(*item.Size))
			}
			if item.Timestamp != nil {
				extra += fmt.Sprintf(
					"<td><time title=\"%s\">%s</time></td>",
					item.Timestamp.Format(time.DateTime),
					humanize.Time(*item.Timestamp),
				)
			}
			if item.Fingerprint != nil {
				extra += fmt.Sprintf("<td>%s</td>", *item.Fingerprint)
			}

			fmt.Fprintf(output, "<tr><td><a href=\"%s\">%s</a></td>%s</tr>\n", item.Name, item.Name, extra)
		}
		output.WriteString("</table></main>")

		if readmeObject != nil && *readme {
			output.WriteString("\n<footer>\n")
			renderReadme(r.Context(), output, readmeObject)
			output.WriteString("</footer>")
		}
	}

	output.Flush()
}

func linksFromMountPoints(path string) (links []Item) {
	for _, mountPoint := range mountPoints {
		if mountPoint.Path != path && strings.HasPrefix(mountPoint.Path, path) {
			links = append(links, Item{Name: strings.SplitAfterN(strings.TrimPrefix(mountPoint.Path, path), "/", 2)[0]})
		}
	}
	return
}

func linksFromStorage(ctx context.Context, path string) (links []Item, readme *storage.ObjectAttrs) {
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
			if strings.ToLower(attrs.Name) == "readme.md" {
				readme = attrs
				if *skipReadme {
					continue
				}
			}
			if attrs.Name != query.Prefix {
				var size = uint64(attrs.Size)
				var md5 = fmt.Sprintf("%x", attrs.MD5)
				links = append(links, Item{
					Name:        strings.TrimPrefix(attrs.Name, query.Prefix),
					Size:        &size,
					Fingerprint: &md5,
					ContentType: &attrs.ContentType,
					Timestamp:   &attrs.Updated,
					Metadata:    attrs.Metadata,
				})
			}
		} else if attrs.Prefix != "" {
			links = append(links, Item{Name: strings.TrimPrefix(attrs.Prefix, query.Prefix)})
		} else {
			slog.Warn("unexpected object", "attrs", attrs)
		}
	}
	return
}

func sortLinks(a, b Item) int {
	if aHasSize, bHasSize := a.Size != nil, b.Size != nil; aHasSize != bHasSize {
		if aHasSize {
			return -1
		}
		return 1
	}

	if *versionSort {
		va, i := guessVersion(a.Name)
		vb, j := guessVersion(b.Name)
		if va != nil && vb != nil {
			if cmp := strings.Compare(a.Name[:i], b.Name[:j]); cmp != 0 {
				return cmp
			}
			if cmp := vb.Compare(va); cmp != 0 {
				return cmp
			}
		}
	}

	return strings.Compare(a.Name, b.Name)
}
