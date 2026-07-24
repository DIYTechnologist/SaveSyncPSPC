FROM golang:1.22-bookworm AS build

ARG VERSION=dev

WORKDIR /src

COPY go.mod ./
COPY embed.go ./
COPY cmd ./cmd
COPY internal ./internal
COPY games ./games

RUN go test ./... \
    && go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/save-sync ./cmd/save-sync \
    && go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/save-sync-ui ./cmd/save-sync-ui

FROM build AS release

RUN mkdir -p /dist/linux-amd64 /dist/linux-arm64 /dist/windows-amd64 \
    && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /dist/linux-amd64/save-sync ./cmd/save-sync \
    && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /dist/linux-amd64/save-sync-ui ./cmd/save-sync-ui \
    && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /dist/linux-arm64/save-sync ./cmd/save-sync \
    && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /dist/linux-arm64/save-sync-ui ./cmd/save-sync-ui \
    && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /dist/windows-amd64/save-sync.exe ./cmd/save-sync \
    && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /dist/windows-amd64/save-sync-ui.exe ./cmd/save-sync-ui \
    && cp -r games /dist/linux-amd64/games \
    && cp -r games /dist/linux-arm64/games \
    && cp -r games /dist/windows-amd64/games

FROM debian:bookworm-slim

WORKDIR /app

COPY --from=build /out/save-sync /usr/local/bin/save-sync
COPY --from=build /out/save-sync-ui /usr/local/bin/save-sync-ui
COPY README.md ./
COPY docs ./docs
COPY games ./games

EXPOSE 8765

ENTRYPOINT []
CMD ["save-sync", "--help"]
