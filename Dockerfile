FROM golang:1.26.8-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /server ./cmd/server

FROM chromedp/headless-shell:stable
COPY --from=build /server /server
ENV PORT=8080 APP_ENV=production CHROME_BIN=/headless-shell/headless-shell HOME=/tmp XDG_CACHE_HOME=/tmp/.cache
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/server"]
