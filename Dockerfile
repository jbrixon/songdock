FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
LABEL org.opencontainers.image.version="$VERSION"

COPY dist/songdock /songdock

EXPOSE 8080
ENTRYPOINT ["/songdock"]
