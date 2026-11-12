# oapi-gen

Generates typed Go and TypeScript clients from an OpenAPI spec, with a
transport that already handles the things a generated client usually
leaves to you: per-attempt timeouts, bounded retries, `Retry-After`,
and a circuit breaker.

It wraps [ogen](https://github.com/ogen-go/ogen) for Go and
[hey-api](https://heyapi.dev) for TypeScript. Those produce the typed
operations; this produces everything around them, and keeps the two
languages behaving the same way.

Extracted from [homelabctl](https://github.com/ChristopherScot/homelabctl),
which still drives it for services it scaffolds.

## Install

```sh
go install github.com/ChristopherScot/oapi-gen@latest
```

Or download a binary from
[Releases](https://github.com/ChristopherScot/oapi-gen/releases).
After that, `oapi-gen update` pulls the newest one.

Coming from `oapi-clientgen`: `update` cannot cross the rename, because
an old binary looks for an asset named after the old tool and v0.1.0
does not publish one. It reports `release v0.1.0 has no asset for
<os>/<arch>`, which is the rename rather than a broken release. Run the
`go install` above once and `update` works again.

## Try a spec

No config, nothing committed:

```sh
oapi-gen generate client --spec https://example.com/openapi.json \
  --out ./internal/acmeapi
```

## Declare them in clients.yaml

Put it at the root of the repo that owns the clients. `generate
client` with no arguments does all of them; naming one does only that.

```yaml
clients:
  acme:
    spec: https://example.com/openapi.json
    vendor: specs/acme.json       # fetched once, committed
    out: ./internal/acmeapi
    languages: [go, ts]
    patches:                      # applied before generating
      - patches/acme-fix-enum.yaml
```

```sh
oapi-gen list
oapi-gen generate client
oapi-gen generate client acme --refresh
```

A remote spec is **vendored on first use** and reused after that, so a
build does not depend on a third party being reachable. `--refresh`
re-fetches it, which is how an upstream change enters the repo: as a
reviewable diff rather than a surprise.

`patches` exist because third-party specs are often not quite valid.
Rather than hand-editing the vendored copy — which the next refresh
would overwrite — a patch is applied on the way through, so the
vendored file stays exactly what upstream published.

## Generate from a spec you own

A service that owns its spec needs none of the fetching, vendoring or
patching above - a spec you own is fixed at the source. It needs the
server scaffolding too, which a consumer of someone else's API never
wants.

```sh
oapi-gen generate self                      # ./openapi.yml -> ./api
oapi-gen generate self ./openapi.yml --out api
```

Meant to run from the service's own `go:generate`, so CI's staleness
gate catches a spec change that was not regenerated.

There is no `ogen.yml` to edit: the settings are the same for every
service built this way, so they are composed rather than copied into
each one.

## What you get

Beside the generated operations:

| | |
|---|---|
| **Timeout** | 5s per attempt, so a call that retries can take longer than 5s overall |
| **Retry** | one retry by default on idempotent methods only; a POST may already have applied |
| **`Retry-After`** | obeyed over the local backoff, capped at 30s — a server asking for an hour is told no rather than retried in a second |
| **Circuit breaker** | opt-in; opens on the failure rate over a rolling window, half-opens one probe at a time |

Both languages implement the same rules, and the generator's own tests
render a client and run its suite to keep them honest.

### Paged operations

An operation that pages gets an iterator — `ListThingsIter` in Go,
`listThingsIter` in TypeScript — that walks the pages, so no caller
writes the cursor loop.

Say so in the spec. OpenAPI has no way to express "this operation
pages", so the generator is told rather than left to guess:

```yaml
paths:
  /things:
    get:
      operationId: listThings
      x-oapi-gen-pagination: {}
```

Empty is the whole declaration when the operation asks with an `after`
query parameter and answers with a `nextCursor`. Three fields override
that, one at a time:

| | |
|---|---|
| `cursor` | the parameter carrying it — `{name: page_token, in: query}` |
| `next` | where the next cursor is in the response — `/meta/next_page_token` |
| `items` | which array is the page, required when the response holds more than one |

Upgrading from `oapi-clientgen`: the key was `x-oapi-clientgen-pagination`
and means the same thing. A spec still carrying it is refused by name
rather than quietly read as unpaged.

**An operation without the key does not page**, whatever its parameters
are called. Guessing from the names gave an iterator to anything with
an `after` parameter and nothing to an API paging under other words,
and both were silent. A marked operation the spec does not support is
refused, naming what was looked for and what the spec actually has.

### Changing the defaults

Anything you leave out keeps its default.

```go
// A slow dependency you would rather wait for than hammer.
api.New(url, "webapp",
	api.WithTimeout(30*time.Second),
	api.WithPolicy(api.ExponentialRetry{}),
)

api.New(url, "webapp", api.WithPolicy(api.NoRetry{}))  // one attempt, no retry
api.New(url, "webapp", api.WithBreaker())              // stop calling a failing dependency
```

TypeScript takes the same settings as one object:

```ts
import { createClient, exponentialRetry, noRetry, Breaker, type RetryPolicy } from './api'

const c = createClient({
  baseUrl: url,
  name: 'webapp',
  timeoutMs: 30_000,
  policy: exponentialRetry,
})

const fastFail = createClient({ baseUrl: url, name: 'webapp', policy: noRetry })
const guarded = createClient({ baseUrl: url, name: 'webapp', breaker: new Breaker() })
```

**Your own retry rule.** A policy is two methods: how long to wait
before each attempt, and whether a given failure is worth repeating.
The length of the backoff list is the retry count.

```go
type sometimes struct{}

func (sometimes) Backoffs() []time.Duration {
	return []time.Duration{time.Second, 3 * time.Second}
}

func (sometimes) Retry(req *http.Request, resp *http.Response, err error) bool {
	return resp != nil && resp.StatusCode == http.StatusServiceUnavailable
}

api.New(url, "webapp", api.WithPolicy(sometimes{}))
```

```ts
const sometimes: RetryPolicy = {
  backoffs: () => [1000, 3000],
  retry: (req, res) => res?.status === 503,
}
```

**Bounding the whole call.** The timeout above covers one attempt.
To cap everything — attempts, waits, body read — give the request its
own deadline:

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
// pass ctx to the operation; the retry loop stops when it does
```

In TypeScript, pass a `signal` with the request. One signal covers
every attempt and the waits between them.

**Skipping this transport.** It plugs into ogen, so you can drop it
and use ogen's own constructor, or keep the retries and swap what
reaches the network underneath them:

```go
api.NewClient(url)                              // no resilience
api.New(url, "webapp", api.WithTransport(rt))   // resilience over your transport
```

```ts
createClient({ baseUrl: url, name: 'webapp', fetch: myFetch })
```

### What counts as a failure

The breaker distinguishes three outcomes, not two. A 501 or 505 is a
permanent statement about *this request*, not evidence the service is
unwell, so it neither trips the breaker nor spends a retry — and
crucially it does not *clear* a run of real failures either. A service
answering 500 on one route and 501 on another would otherwise never
trip.

A caller cancelling its own request is not a dependency failure. A
timeout is.

## Use it as a library

The CLI is a thin shell over `generate`; everything it does is
available directly.

```go
import "github.com/ChristopherScot/oapi-gen/generate"

err := generate.Generate(".", "acme", generate.Client{
    Spec:      "https://example.com/openapi.json",
    Out:       "./internal/acmeapi",
    Languages: []string{"go"},
}, false)
```

`apiclient` renders the support files on their own if you are driving
the generators yourself.

## Generated files are not yours to edit

Every file this writes carries a header saying so, and a regenerate
overwrites it. Change the template here instead.

It also *removes* files it used to write and no longer does — only
ones carrying that header, so a `client_test.go` you wrote yourself is
left alone.

The header still begins "Generated by homelabctl". That is deliberate:
it is how a file already on disk is recognised as ours, and files
written before the extraction have to stay recognisable or they can
never be cleaned up.

## Build

```sh
make build            # compile
make run ARGS='...'   # run without installing
make test             # go vet and go test -race. What CI runs.
```

Releases are cut by CI when the first line of `VERSION` changes on
`main`.
