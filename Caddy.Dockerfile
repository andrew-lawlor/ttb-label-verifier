# Custom Caddy build with the rate-limit plugin (not in Caddy core --
# see Caddyfile for why: protecting the OCR/PDF endpoints from abuse on a
# public, unauthenticated URL).
FROM caddy:2-builder-alpine AS builder
RUN xcaddy build --with github.com/mholt/caddy-ratelimit

FROM caddy:2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
