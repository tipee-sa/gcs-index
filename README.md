# gcs-index

Provide auto-index in front of Google Cloud Storage buckets.

Plays well with a caching proxy in front. 😉

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

## Example nginx caching proxy configuration

```
http {
  proxy_cache_path  /var/cache/nginx keys_zone=static:10m max_size=1g inactive=1w;

  upstream gcs-index {
    server  unix:/path/to/gcs-index.sock;
  }

  server {
    proxy_cache static;
    proxy_cache_use_stale  error timeout invalid_header updating;
    proxy_cache_revalidate  on;
    proxy_cache_valid  200 404 1m;
    proxy_cache_background_update  on;

    location / {
      proxy_pass http://gcs-index;
    }
  }
}
```
