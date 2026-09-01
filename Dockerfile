# Build stage: compile the static Go binary. Templates and static assets
# are baked in via go:embed (internal/webassets), so this stage's output
# is the only thing the runtime stage needs from it.
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY . .
# GIT_SHA is passed in from outside the build context (.git is excluded via
# .dockerignore) -- see docker-compose.yml and DEPLOY.md. Exposed at
# GET /version at runtime so "is the server running the latest code" is
# checkable without SSHing in.
ARG GIT_SHA=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.version=${GIT_SHA}" -o /out/server ./cmd/server

# Runtime stage: the OCR backend shells out to `tesseract` and `pdftoppm`
# (poppler-utils) — neither is statically linked, so both need to be
# installed here rather than just copying the binary into a scratch image.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      tesseract-ocr \
      poppler-utils \
      ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/server /usr/local/bin/server

ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
