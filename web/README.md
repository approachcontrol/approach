# Approach web viewer

A Next.js (App Router) app that renders repositories and Flows from the
read-only GraphQL API served by `approach serve`. It is a browser-accessible
view of the same state the TUI shows: a repo list, a repo detail with its
Flows, and a Flow detail with phases and status.

It is a **separate deployable**, not part of the Go build. `go test ./...`,
`gofmt -l .`, and `make build` never look inside `web/`; CI runs it as its own
job (`.github/workflows/ci.yml`).

## How it talks to Approach

Every GraphQL request runs **server-side**, in a React Server Component on the
Node runtime. The browser never sees the endpoint URL or the token — that is
the shape `docs/graphql-api.md` recommends under "Consuming from a web app",
and it is why no CORS configuration is involved.

| Variable | Required | Meaning |
| --- | --- | --- |
| `APPROACH_GRAPHQL_URL` | yes | Full endpoint URL, e.g. `http://127.0.0.1:8787/graphql` or `https://<tunnel-host>/graphql` |
| `APPROACH_API_TOKEN` | only when the server has one | Sent as `Authorization: Bearer <token>` |

Never give either a `NEXT_PUBLIC_` prefix; that would ship it to the browser.

Requests use `cache: 'no-store'` and a 25s `AbortSignal` timeout, and the data
routes set `maxDuration = 30`. The ordering is deliberate: the API bounds
snapshot construction at 20s, the client aborts after that, and the platform's
function timeout is longer still — so a slow scan surfaces as this app's error
panel rather than an opaque platform timeout.

## Local development

```bash
# 1. Serve Approach against a scratch state root. NEVER point a dev build at
#    the default artifact root; use --state-root.
approach serve --state-root /tmp/approach-demo --scan-root ~/dev

# 2. Point the app at it.
cd web
cp .env.example .env.local     # edit if you changed --addr
npm install
npm run dev                    # http://localhost:3000
```

A loopback `approach serve` needs no token, so leave `APPROACH_API_TOKEN`
empty for that setup.

### Checks

```bash
npm run lint        # eslint (flat config, eslint-config-next)
npm run typecheck   # tsc --noEmit
npm test            # vitest — GraphQL client + repo-id codec + formatting
npm run build       # next build
```

## Reaching a machine-local API from Vercel

`approach serve` binds loopback by default and speaks **plaintext HTTP with no
TLS**. A deployed Vercel function cannot reach your laptop directly, so put a
TLS-terminating tunnel in front of it and point `APPROACH_GRAPHQL_URL` at the
tunnel. Do **not** simply bind `0.0.0.0` and forward a port: that puts the
token and every Flow record on the wire in the clear.

Set a token whenever the server is reachable off-loopback — `approach serve`
requires one for any non-loopback bind.

```bash
export APPROACH_API_TOKEN="$(openssl rand -hex 32)"
approach serve --state-root ~/.local/state/approach/sessions/v1

# Quick tunnel — ephemeral URL, fine for a demo.
cloudflared tunnel --url http://127.0.0.1:8787

# Named tunnel — stable hostname, recommended, because a quick tunnel's URL
# changes every run and each change means editing the Vercel env var and
# redeploying.
cloudflared tunnel create approach
cloudflared tunnel route dns approach approach.example.com
cloudflared tunnel run --url http://127.0.0.1:8787 approach
```

Self-hosting `approach serve` behind a reverse proxy (nginx/Caddy with a real
certificate) works the same way: the app only needs a reachable HTTPS URL that
proxies to the server, and the token.

Verify the tunnel independently of the app before blaming the app:

```bash
curl -s https://approach.example.com/healthz          # {"status":"ok"}
curl -s -H 'Content-Type: application/json' \
     -H "Authorization: Bearer $APPROACH_API_TOKEN" \
     -d '{"query":"{ repos { id displayName } }"}' \
     https://approach.example.com/graphql
```

## Deploying to Vercel

The code and config live here; connecting the repository and setting
environment variables are account-level actions in the Vercel dashboard.

1. **Import the repository** in Vercel → *Add New… → Project*.
2. Set **Root Directory** to `web`. Framework preset auto-detects as Next.js;
   leave the build and install commands at their defaults.
3. Add **Environment Variables** for Production *and* Preview:
   - `APPROACH_GRAPHQL_URL` = `https://<tunnel-host>/graphql`
   - `APPROACH_API_TOKEN` = the token the server was started with
4. **Enable Deployment Protection** (Project → Settings → Deployment
   Protection). See the warning below — decide this before the first deploy,
   not after.
5. Deploy. The Git integration then gives preview deploys per pull request and
   a production deploy on merge to `main`.
6. Verify the deployed instance: load `/`, open a repository, and confirm a
   Flow detail renders. Check the repository URL specifically — repo ids are
   filesystem paths encoded into the route segment, and edge-proxy URL handling
   is worth confirming once on the real deployment rather than only in dev.

If the tunnel URL changes (quick tunnels get a new one per run), update
`APPROACH_GRAPHQL_URL` and redeploy.

### Protect the deployment

**A deployed instance republishes machine-local data.** The views render
repository absolute paths, Flow titles, branch names, phase summaries, and
issue/PR links — data the API's own auth posture treats as never leaving your
machine. Deployment Protection is the default posture here:

- **Vercel Authentication** — free on all plans; only members of your Vercel
  team can load the deployment.
- **Password Protection** — a paid alternative if you need to share a link
  with someone outside the team.

Leaving the instance public is a deliberate decision to publish that data, not
a default. Make it knowingly.

The app narrows the exposure on its own too: it never selects
`Flow.instructions` or `Phase.notes` — the unbounded, agent-authored free-text
fields — so they cannot be rendered even by accident. Error messages are fixed
strings that never carry the endpoint URL or the token; the detail goes to the
server log only. `robots` metadata is `noindex, nofollow`, and every response
carries `Cache-Control: no-store`.

## Layout

```
web/
  src/lib/approach-api.ts   typed fetch wrapper + the three queries
  src/lib/repo-id.ts        base64url codec for path-shaped repo ids
  src/lib/load.ts           turns an expected API failure into a rendered panel
  src/lib/format.ts         UTC timestamp / short-commit formatting
  src/components/           status badges, error and empty panels
  src/app/                  layout, `/`, `/repos/[id]`, `/flows/[id]`
```

Two things are worth knowing before changing them:

- **Repo ids are base64url in the URL.** `Repo.id` is an absolute filesystem
  path, and percent-encoding a `/` as `%2F` inside one dynamic segment is
  handled inconsistently across Next's router and edge proxies. Encoding
  sidesteps it entirely; a segment that does not decode is a 404, never an
  exception. `Flow.id` is already URL-safe and is only `encodeURIComponent`d
  defensively.
- **There is no `loading.tsx`.** A root loading boundary makes Next flush the
  response shell before the page resolves, which commits a 200 status — so
  `notFound()` rendered the 404 page under an HTTP 200. Correct status codes
  won over a skeleton.

There is no GraphQL client library. The API is a plain `POST /graphql` with a
JSON body, so a small typed `fetch` wrapper covers it and keeps the dependency
surface auditable.
