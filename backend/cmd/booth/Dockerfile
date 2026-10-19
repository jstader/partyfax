# Build context is the backend/ directory:
#   docker build -f cmd/booth/Dockerfile -t partyfax/booth .
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /booth ./cmd/booth

# The data directory, owned by the runtime user, to seed the named volume:
# Docker copies an image's directory, permissions included, into a new one.
RUN mkdir -p /data && chown 65534:65534 /data

# scratch, plus the CA roots: the booth fetches Cloudflare Access's signing
# keys over HTTPS to check admin logins.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /booth /booth
COPY --from=build --chown=65534:65534 /data /var/lib/booth

EXPOSE 8083
USER 65534:65534

ENTRYPOINT ["/booth"]
CMD ["-addr", ":8083"]
