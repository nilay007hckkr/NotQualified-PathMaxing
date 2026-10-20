# syntax=docker/dockerfile:1
#
# The Go server on its own: redirects, API and workers. It serves a web app
# only if one is mounted and WEB_DIST_DIR points at it; deploy/Dockerfile.full
# builds the image with the web app inside.
#   docker build -t pathmaxing-server .

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
# Static binary: the runtime image needs no C library.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20
# CA certificates for the trust scanner and the threat feeds over HTTPS.
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 app
COPY --from=build /out/server /usr/local/bin/pathmaxing
USER app
ENV APP_ENV=production PORT=8080
EXPOSE 8080
ENTRYPOINT ["pathmaxing"]
