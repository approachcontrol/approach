# Approach web viewer

A Next.js (App Router) app that renders repositories and Flows from the
read-only GraphQL API served by `approach serve`. It is a browser-accessible
view of the same state the TUI shows: a repo list, a repo detail with its
Flows, and a Flow detail with phases and status.

It is a **separate deployable**: no Go code compiles anything here, and CI runs
it as its own job (`.github/workflows/ci.yml`). `make build` and `make fmt-check`
ignore `web/` entirely.

`make test` is the one exception, and it is deliberate. It runs
`graphqlapi/web_documents_test.go`, which *reads* `web/src/lib/approach-api.ts`
to hold this app's GraphQL documents against the real schema — see [Layout](#layout).
It reads the file as text; it does not build the app.

One wrinkle: `web/node_modules` lives inside the repo tree and some npm packages
ship Go files, so once you have run `npm install` it is on the path of anything
that walks the tree. The go command skips it via the `ignore web/node_modules`
directive in `go.mod`, which covers bare `go test ./...` too. `gofmt` does not
read `go.mod` and still descends into it, so use `make fmt-check` rather than a
bare `gofmt -l .`.

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
snapshot construction at 20s and answers **504** itself, the client aborts at
25s, and the platform's function timeout is longer still — so a slow scan
surfaces as this app's error panel rather than an opaque platform timeout.

In practice the server's 504 is the timeout path that fires; the client abort is
the backstop for a request that gets no response at all, such as a hung tunnel.
The client maps status to a distinct message per condition (401 stale token, 403
refused host, 503 busy, 504 timed out), because "the API returned an unexpected
response" is useless when the actual fix is a new environment variable.

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

`npm run dev` passes `-H 127.0.0.1` deliberately. Next's own default is
`0.0.0.0`, and this app has no authentication of its own: on the default it
would answer anyone who can reach port 3000 and render your Flow state to them,
fetching the loopback API on their behalf. That would undo the exact protection
`approach serve` gives you by binding loopback and demanding a token anywhere
else.

To reach the dev server from another device, do not drop the flag, and do not
reuse the `cloudflared` recipe from the section below: those are *public*
tunnels. That is fine for the API, which checks a bearer token on every request,
and not fine for this app, which checks nothing — the tunnel would simply publish
your Flow state at a URL. Forward the port over a channel that authenticates:

```bash
# From the other device. The viewer stays bound to loopback on the host.
ssh -N -L 3000:127.0.0.1:3000 you@host
```

An authenticating proxy in front of the tunnel (Cloudflare Access, say) works
too. Plain tunnel plus no app auth does not.

`npm start` (`next start`, after `npm run build`) keeps Next's `0.0.0.0`
default, because a self-hosted container has to bind the wildcard to be reachable
at all. Nothing here documents running it on a workstation; if you do, pass
`-H 127.0.0.1` yourself or front it with authentication.

### Checks

```bash
npm run lint        # eslint (flat config, eslint-config-next)
npm run typecheck   # tsc --noEmit
npm test            # vitest — API client and status mapping, load(), repo-id
                    #   codec, formatting, badge tones, header rules
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
   Protection) and set the *scope* to **All Deployments**, not the default
   Standard Protection. See the warning below — the scope is the part that
   matters, and both it and the plan it requires are decisions to make before
   the first deploy, not after.
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
machine. Deployment Protection is the default posture here — but read the scope
carefully, because the free configuration does **not** cover the URL this app
will actually be served from.

Vercel splits Deployment Protection into a *method* (who gets in) and a *scope*
(which URLs are gated), and the scope is where the trap is:

| Scope | Covers | Plan |
| --- | --- | --- |
| **Standard Protection** (default) | Every URL **except production domains** | All plans |
| **All Deployments** | Everything, production domains included | Pro (as part of the paid Advanced Deployment Protection add-on) or Enterprise |

Standard Protection is the recommended default for an ordinary site, where the
production domain is *supposed* to be public. That is exactly backwards here.
Step 5 promotes `main` to production, so on the Hobby plan the documented setup
leaves `<project>.vercel.app` — the URL you would actually open — serving
repository paths, Flow titles, branches, and phase summaries to anyone, while
the dashboard reads "protected" because the preview and generated deployment
URLs are. Vercel's own docs state it plainly: on Hobby, "your production domain
remains publicly accessible."

Methods, once the scope is right:

- **Vercel Authentication** — free on all plans, restricts access to Vercel
  users with access to the project. Free covers the *method*, not the
  production-domain *scope*.
- **Password Protection** — Enterprise, or part of the same paid Pro add-on.
  Use it to share a link with someone outside the team.

So there are three honest options, and no free one that protects production:

1. Pay for the add-on (or Enterprise) and set the scope to All Deployments.
2. Stay on Hobby and never create a production deployment — deploy previews
   only, which Standard Protection does cover.
3. Do not deploy this at all; `npm run dev` against a loopback `approach serve`
   needs no hosting.

Leaving the instance public is a deliberate decision to publish that data, not
a default — and on Hobby it is the decision you make by *not* choosing. Make it
knowingly, and verify it by loading the production URL in a logged-out private
window rather than trusting the settings page.

The app narrows the exposure on its own too: it never selects
`Flow.instructions` or `Phase.notes`, the two largest agent-authored free-text
fields, so they cannot be rendered even by accident. That is a narrowing, not a
guarantee of brevity — `Phase.summary` *is* rendered and is also unvalidated
agent text, bounded only by the API's 16 MiB response cap. Error messages are
fixed strings that never carry the endpoint URL or the token; the detail goes to
the server log only. `robots` metadata is `noindex, nofollow`, and page
responses are uncacheable.

Two caveats worth knowing:

- The `Cache-Control: no-store` header in `next.config.ts` is belt-and-braces.
  The actual guarantee comes from `dynamic = 'force-dynamic'`, which makes Next
  send `private, no-cache, no-store, max-age=0, must-revalidate` on every
  dynamic route on its own — and on a prerendered route Next's own value wins
  over the configured one, so do not read the configured header as the control.
  Content-hashed `_next/static` assets are deliberately excluded from the rule
  so they keep their `immutable` caching; `src/test/next-config.test.ts` pins
  that, because the blanket version shipped once and silently disabled asset
  caching everywhere.
- **An API outage still renders HTTP 200** with an error panel, because the
  failure is caught and rendered rather than thrown. An uptime monitor pointed
  at `/` will report the app healthy while it shows nothing. Monitor
  `approach serve`'s own `/healthz` through the tunnel instead.

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

The queries in `src/lib/approach-api.ts` are the only place this app hard-codes
the server's schema, and nothing in the JavaScript toolchain can check them. A
Go test does: `graphqlapi/web_documents_test.go` reads the documents out of that
file and validates them against the live schema, so renaming a field in
`graphqlapi/schema.go` fails `make test` instead of only breaking production.
Keep the `const NAME_QUERY = \`…\`` shape — that test extracts them by pattern,
and fails if it finds a `_QUERY` constant it could not extract.

It binds the *documents*, not the TypeScript types. A field that changes
nullability — `Flow.branch` becoming non-null, say — still validates while the
interfaces in that file quietly lie about it. Check the interfaces by hand when
you touch nullability in `graphqlapi/schema.go`.

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
