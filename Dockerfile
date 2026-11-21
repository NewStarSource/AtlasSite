# Runtime image for the star atlas service.
# The linux/amd64 binary is built by CI (or locally) into dist/staratlas first.
FROM gcr.io/distroless/static-debian12:nonroot
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="staratlas" \
      org.opencontainers.image.source="https://github.com/NewStarSource/AtlasSite" \
      org.opencontainers.image.revision="${COMMIT}"
COPY dist/staratlas /usr/local/bin/staratlas
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/staratlas"]
CMD ["-config", "/data/conf/atlas.json"]
