# gcs-index

Provide auto-index in front of Google Cloud Storage buckets.

Intended to be used together with a caching proxy.

## Usage

```
gcs-index path:bucket:prefix [path:bucket:prefix ...]
```

For each bucket:
- `path` is the "mount point" in the global tree.
- `bucket` is the name of the bucket.
- `prefix` is a prefix to apply to objects when listing (might be empty).

## Flags

  - `-port int`: port to listen on (default 8080)
  - `-socket string`: socket to listen on
  - `-socket-umask int`: umask for the socket file (default -1)
  - `-readme`: enable README.md rendering
  - `-skip-readme`: skip README.md in directory listings
  - `-version-sort`: sort directory listings using a semver-aware algorithm
  - `-v`: enable verbose logging
