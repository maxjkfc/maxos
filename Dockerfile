FROM golang:1.27.4-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maxos ./cmd/maxos

FROM alpine:3.24
# ca-certificates: the Access verifier fetches JWKS over HTTPS.
RUN apk add --no-cache ca-certificates \
 && adduser -D -H -u 10001 maxos
COPY --from=build /out/maxos /usr/local/bin/maxos
USER maxos
ENTRYPOINT ["maxos"]
