FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY gcs-index /gcs-index
USER nonroot:nonroot

ENTRYPOINT ["/gcs-index"]
