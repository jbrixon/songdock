FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
LABEL org.opencontainers.image.version="$VERSION"

COPY dist/songdock /songdock
COPY --chown=nonroot:nonroot docker-data/ /data/

EXPOSE 8080
VOLUME ["/data"]
ENV ARTWORK_DIR=/data/uploads/artwork
ENTRYPOINT ["/songdock"]
CMD ["serve"]
