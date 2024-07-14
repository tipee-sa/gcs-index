package main

import (
	"bufio"
	"context"
	_ "embed"
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

type Link struct {
	Target string
	Extra  string
}

//go:embed page.html
var pageHtml []byte

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Last-Modified", time.Now().Truncate(time.Minute).Format(http.TimeFormat)) // Listing shows relative timestamps.
	w.Header().Set("Cache-Control", defaultCacheControl)

	if r.Method == http.MethodHead {
		// Directory index always returns 200 OK.
		return
	}

	var links []Link

	links = append(links, linksFromMountPoints(r.URL.Path)...)

	var storageLinks, readmeObject = linksFromStorage(r.Context(), r.URL.Path)
	links = append(links, storageLinks...)

	links = slices.Compact(links)
	slices.SortStableFunc(links, sortLinks)

	var output = bufio.NewWriter(w)

	output.Write(pageHtml)
	output.WriteString("<main><table>\n")
	if r.URL.Path != "/" {
		output.WriteString("<tr><td><a href=\"../\">../</a></td></tr>\n")
	}
	for i, link := range links {
		// Split links with and without extra information into separate tables.
		if i > 0 && links[i-1].Extra != "" && link.Extra == "" {
			output.WriteString("</table><table>\n")
		}
		// Skip the favicon link on the root page.
		if link.Target == "favicon.ico" && r.URL.Path == "/" {
			continue
		}
		output.WriteString(fmt.Sprintf("<tr><td><a href=\"%s\">%s</a></td>%s</tr>\n", link.Target, link.Target, link.Extra))
	}
	output.WriteString("</table></main>")

	if readmeObject != nil && *readme {
		output.WriteString("\n<footer>\n")
		renderReadme(r.Context(), output, readmeObject)
		output.WriteString("</footer>")
	}

	output.Flush()
}

func linksFromMountPoints(path string) (links []Link) {
	for _, mountPoint := range mountPoints {
		if mountPoint.Path != path && strings.HasPrefix(mountPoint.Path, path) {
			links = append(links, Link{strings.SplitAfterN(strings.TrimPrefix(mountPoint.Path, path), "/", 2)[0], ""})
		}
	}
	return
}

func linksFromStorage(ctx context.Context, path string) (links []Link, readme *storage.ObjectAttrs) {
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
				links = append(links, Link{
					strings.TrimPrefix(attrs.Name, query.Prefix),
					fmt.Sprintf(
						"<td>%s</td><td><time title=\"%s\">%s</time></td><td>%x</td>",
						humanize.IBytes(uint64(attrs.Size)),
						attrs.Updated.Format(time.DateTime),
						humanize.Time(attrs.Updated),
						attrs.MD5,
					),
				})
			}
		} else if attrs.Prefix != "" {
			links = append(links, Link{strings.TrimPrefix(attrs.Prefix, query.Prefix), ""})
		} else {
			slog.Warn("unexpected object", "attrs", attrs)
		}
	}
	return
}

func sortLinks(a, b Link) int {
	if aHasExtra, bHasExtra := a.Extra != "", b.Extra != ""; aHasExtra != bHasExtra {
		if aHasExtra {
			return -1
		}
		return 1
	}

	if *versionSort {
		va, i := guessVersion(a.Target)
		vb, j := guessVersion(b.Target)
		if va != nil && vb != nil {
			if cmp := strings.Compare(a.Target[:i], b.Target[:j]); cmp != 0 {
				return cmp
			}
			if cmp := vb.Compare(va); cmp != 0 {
				return cmp
			}
		}
	}

	return strings.Compare(a.Target, b.Target)
}
