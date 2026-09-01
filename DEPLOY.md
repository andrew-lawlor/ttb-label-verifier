# Deploy Runbook — Hetzner VPS

Deploys this app to a Debian VPS via Docker Compose: an `app` container
(this repo's `Dockerfile`) behind a `caddy` container that handles TLS
automatically via Let's Encrypt. See `docker-compose.yml` and `Caddyfile`.

## One-time setup

**1. DNS.** Point `ttb-poc.lawlor.io`'s **A record** at the VPS's public
IPv4 address (and an AAAA record if the VPS has IPv6). This is a subdomain
record only — it doesn't touch `lawlor.io`'s existing mail (MX/SPF/DKIM)
records. Let's Encrypt needs this to resolve correctly before Caddy can
issue a certificate, so do this first and let it propagate before starting
the stack.

**2. Docker, on the VPS (Debian):**

```bash
sudo apt-get update
sudo apt-get install -y docker.io docker-cli docker-compose
sudo systemctl enable --now docker
```

(On Debian 13/trixie, `docker.io` ships only the daemon — `docker-cli` is a
separate package for the client. Without it, `docker` isn't on PATH even
though the daemon is installed and running.)

**3. Firewall.** Open 80 and 443 (Caddy needs both — 80 for the ACME
HTTP-01 challenge and redirect, 443 for the actual TLS traffic). Leave SSH
open on whatever port it's already on:

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
```

**4. Get the code onto the VPS** — either clone directly if the repo is
pushed somewhere reachable from the VPS, or rsync a local copy:

```bash
git clone <repo-url> ttb-label-verifier
# or: rsync -az --exclude .git ./ user@vps-host:ttb-label-verifier/
cd ttb-label-verifier
```

## Deploy

```bash
docker compose up -d --build
```

First run will take a couple of minutes — `app`'s build context installs
`tesseract-ocr`/`poppler-utils`, and `caddy`'s build compiles a custom
Caddy binary from source via `xcaddy` (see "Rate limiting" below) rather
than pulling a stock image, which is the slower part (~2 min). Caddy will
request a certificate for `ttb-poc.lawlor.io` automatically once it can
reach the `app` container and the DNS record resolves.

**Verify:**

```bash
docker compose ps                       # both services should be "Up"
docker compose logs caddy --tail 50     # look for successful cert issuance
curl -sI https://ttb-poc.lawlor.io/healthz
```

## Redeploy (after a code change)

```bash
git pull
docker compose up -d --build
```

**Caution, verified the hard way:** editing `docker-compose.yml` itself
(not just a service's build context) can cause Compose to recreate *every*
service, not just the one you touched — it invalidates the project's
per-service config hash regardless of which service the YAML change was
about. Don't assume a change scoped to one service's file is actually
scoped to that service's container; run `docker compose up -d --build
<service>` for a single service the same as always, but check `docker
compose images` / container creation timestamps afterward if you need to
be sure something else wasn't also recreated. (Rebuilding `caddy` doesn't
lose its certificate either way — that's in the `caddy_data` named volume,
independent of the container lifecycle.)

## Rate limiting

`caddy` is a custom build (`Caddy.Dockerfile`, via `xcaddy`) with the
[`mholt/caddy-ratelimit`](https://github.com/mholt/caddy-ratelimit) plugin
— not in Caddy core. See `Caddyfile` for the config: `POST /api/*` (the
OCR/PDF-extraction endpoints) is limited to 10 requests/minute per client
IP, scoped by method so it does not affect `GET /api/verify/batch/{id}`
(htmx polls that every 2s during a batch — catching it in the same limit
would break the progress UI). Verified live: an 11th rapid POST gets
`429`, the polling endpoint and page loads are unaffected throughout.

## Switching to the Claude vision backend

Not needed by default (OCR backend, no API key required). If wanted for a
specific deployment, add a `.env` file (already gitignored) alongside
`docker-compose.yml`:

```
EXTRACTION_BACKEND=claude
ANTHROPIC_API_KEY=sk-...
```

and add `env_file: .env` to the `app` service in `docker-compose.yml`
before redeploying. Never commit the `.env` file itself.

## Rollback

```bash
git checkout <previous-commit-or-tag>
docker compose up -d --build
```

## Known limitation carried from the app design

Batch job state is in-memory (see `SPEC.md` §"No persistent database").
A `docker compose restart app` or redeploy loses any in-flight or
just-completed batch that hasn't been read yet — there's exactly one `app`
container in this setup by design, consistent with that assumption, but
it's worth knowing before redeploying mid-demo.
