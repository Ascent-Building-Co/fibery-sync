# fibsync Integration Helpers

This package provides helpers for building fibsync integrations with optional webhooks, OAuth, and synchronization endpoints.

## Usage

```go
server := fibsync.NewServer(
	fibsync.WithLogger(fibsync.SlogAdapter{Logger: slog.Default()}),
	fibsync.WithStrictJSON(true),
)

if err := fibsync.AddIntegration[
	MyValidateResponse,
	MyAccountFields,
	MyPage,
	MyWebhookParams,
	MyWebhookReply,
	MyWebhookHeaders,
	MyWebhookNotification,
	MyResourceParams,
](ctx, server, integration,
	fibsync.WithIntegrationID("quickbooks"),
); err != nil {
	log.Fatal(err)
}

if err := server.Serve(":8080"); err != nil {
	log.Fatal(err)
}
```

## Graceful shutdown

Use `BuildHTTPServer` to manage shutdown in your application:

```go
srv := server.BuildHTTPServer(
	":8080",
	fibsync.WithReadTimeout(5*time.Second),
	fibsync.WithWriteTimeout(60*time.Second),
)

go func() {
	<-shutdownCtx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}()

if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
	log.Fatal(err)
}
```

## Middleware

Middleware is applied with `WithServeMiddleware` (wraps the whole server) or per
`CustomRoute`. Each built-in middleware is a constructor returning a `Middleware`.
The framework ships:

- `GzipMiddleware()` — gzip-compresses responses when the client sends `Accept-Encoding: gzip`.
- `RateLimitMiddleware(RateLimit{...})` — per-client-IP token bucket; responds `429` with `Retry-After` on limit.
- `IPAllowlistMiddleware(IPAllowlist{...})` — permits only allow-listed CIDRs; `403` otherwise. Empty allowlist fails closed (denies all).
- `RequestLogMiddleware(RequestLog{...})` — one structured `slog` access record per request (method, path, status, duration, bytes, client IP). Metadata only — bodies are never logged, since they carry OAuth tokens and the account-gate secret.

### Composition

Order middleware outermost → innermost so cheap rejections happen early and the
log captures everything (including rejections and the on-wire, compressed byte count):

```go
trusted, err := fibsync.ParseCIDRs("10.0.0.0/8") // your proxy/ngrok CIDRs
if err != nil {
	log.Fatal(err)
}
allowed, err := fibsync.ParseCIDRs(/* fibsync egress CIDRs */)
if err != nil {
	log.Fatal(err)
}

srv := server.BuildHTTPServer(":8080", fibsync.WithServeMiddleware(
	fibsync.RequestLogMiddleware(fibsync.RequestLog{Trusted: trusted}),
	server.IPAllowlistMiddleware(fibsync.IPAllowlist{Allowed: allowed, Trusted: trusted}),
	fibsync.RateLimitMiddleware(fibsync.RateLimit{RPS: 20, Burst: 40, Trusted: trusted}),
	fibsync.GzipMiddleware(),
))
```

Use `server.IPAllowlistMiddleware(...)` (the method) rather than the package function:
it auto-exempts the framework's public paths (webhook relays) so provider-facing
traffic is not blocked. Call it after registering integrations. To exempt further
app-specific paths (for example static pages you add with `Server.Handle`), wrap it
in `SkipWhen`. The bare `fibsync.IPAllowlistMiddleware(...)` function is still
available when you want no automatic exemptions.

### Client IP and trusted proxies

`RateLimitMiddleware`, `IPAllowlistMiddleware`, and `RequestLogMiddleware` resolve
the client IP with `ClientIP(r, trusted)`. Forwarding headers (`X-Forwarded-For`,
`X-Real-IP`) are believed **only** when the peer (`RemoteAddr`) is within one of
the `Trusted` CIDRs; otherwise the raw peer address is used, so a direct client
cannot spoof its IP.

- Behind a proxy (ngrok, fibsync's infrastructure, a load balancer), you **must**
  set `Trusted` to the proxy's CIDR — otherwise every request appears to come
  from the proxy and per-IP limiting/filtering collapses to a single address.
- For local **ngrok** development the agent forwards over loopback, and
  `localhost` frequently resolves to IPv6 — trust **both** stacks
  (`127.0.0.1/8,::1/128`). Trusting only `127.0.0.1/8` while the agent connects
  over `::1` makes every request resolve to `::1`, which an IPv4 allowlist
  rejects with 403.
- Never list a proxy in `Trusted` unless you control it or the network path to it.

### Example environment-driven configuration

Middleware is configured in code, but a server typically sources the values from
the environment. An illustrative `.env` for a single integration behind ngrok:

```sh
# Middleware
HTTP_RATE_RPS=20
HTTP_RATE_BURST=40
HTTP_IP_FILTER_ENABLED=true
HTTP_IP_ALLOWLIST=18.198.169.41,18.196.77.165,3.124.79.168  # fibsync egress
HTTP_TRUSTED_PROXIES=127.0.0.1/8,::1/128                    # local ngrok agent

# Account gate — shared secret entered in fibsync's auth form
# generate: openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
INTEGRATION_GATE=<secret>
```

The env var names are yours to choose — the framework reads none of them
directly. Parse the CIDR lists with `fibsync.ParseCIDRs` and pass the results to
the middleware.

## Webhook relay

Some providers cannot deliver webhooks straight to fibsync (an unsupported
Content-Type, a single callback URL fronting several endpoints, etc.). Implement
`IntegrationWithWebhookRelay` and the framework mounts a relay at
`{prefix}/webhook` that forwards each inbound delivery to a target you resolve
and mirrors the upstream response back:

```go
func (i *MyIntegration) WebhookRelay(ctx context.Context, rawBody []byte, header http.Header) (fibsync.RelayTarget, *fibsync.IntegrationError) {
	url, err := i.resolveTarget() // e.g. fibsync's webhooks-svc URL for this app
	if err != nil {
		return fibsync.RelayTarget{}, fibsync.ServiceUnavailable(err.Error())
	}
	return fibsync.RelayTarget{URL: url, ContentType: "application/json"}, nil
}
```

The framework owns the mechanics (body read subject to `WithMaxBody`, hop-by-hop
header stripping, forwarding via the configured HTTP client under a timeout, and
response mirroring); you only resolve where each delivery goes. The relay path is
registered as a public path, so `Server.IPAllowlistMiddleware` exempts it
automatically — point the provider's callback at `{prefix}/webhook`. Enablement is
by capability: implementing the interface turns the relay on.

## Writable fields (Fibery → source)

A synced field can be made editable in Fibery, with the edit pushed back to the
integration. None of this is in Fibery's public docs; the shapes here were
captured from a live workspace. Three things must line up:

1. **App-level gate** — `ResponsibleFor.DataProviding` set in `Config`.
2. **Field-level opt-in** — `Writable` on the field's `DataField`.
3. **Type-level declaration** — the type's `SyncConfigType.Actions` declares
   `ExecuteUpdate`. Without it the field is editable but the edit never leaves
   Fibery.

```go
func (i *Integration) SyncConfig(ctx context.Context, req fibsync.SyncMetaRequest[AccountFields]) (fibsync.SyncConfig, *fibsync.IntegrationError) {
	return fibsync.SyncConfig{
		Types: []fibsync.SyncConfigType{
			{ID: "issue", Name: "Issue", Default: true}.Updatable(),
			{ID: "status", Name: "Status", Default: true},
		},
	}, nil
}
```

Implement `IntegrationWithExecute` to receive the edits; the framework mounts
`{prefix}/api/v1/synchronizer/execute` when you do.

```go
func (i *Integration) Execute(ctx context.Context, req fibsync.ExecuteRequest[AccountFields]) (fibsync.ExecuteResponse, *fibsync.IntegrationError) {
	for typeID, entities := range req.Action[fibsync.ExecuteUpdate] {
		for _, e := range entities {
			for fieldID, v := range e.Fields {
				// Relations arrive as {id, name}; scalars are plain values.
				if ref, ok := fibsync.AsRelation(v); ok {
					// write ref.ID to the source
					_ = ref
					continue
				}
				_ = fieldID
			}
		}
		_ = typeID
	}
	return fibsync.ExecuteResponse{}, nil
}
```

Worth knowing:

- Fibery calls this **once per edit**, and re-reads the sync config immediately
  beforehand, so changes to `Actions` take effect on the next edit.
- `Fields` carries **only what changed**, unlike `SyncData` where entities are
  applied whole.
- Only `ExecuteUpdate` is honored. `create` and `delete` can be declared but
  Fibery never sends them — creation and deletion stay source-owned, so use an
  `Action` that writes to the source and let the entity appear on the next sync.
- Fibery accepts an empty response. How it reacts to a non-2xx is unverified.
- `Config` caches on Fibery's side: bump `Version` whenever you change it, or
  the `DataProviding` change above will not be picked up.

## Account deletion cleanup

Fibery calls `DELETE {prefix}/api/v1/account` when a user deletes an account for
the integration, so the connector can clean up in the third-party system —
usually revoking an access token. Implement `IntegrationWithAccountDeletion` and
the framework mounts the route.

```go
func (i *Integration) DeleteAccount(ctx context.Context, account fibsync.Account[AccountFields]) *fibsync.IntegrationError {
	if err := revokeToken(ctx, account.AccountFields.Token); err != nil {
		// Only block deletion if retrying would help.
		slog.Warn("token revoke failed", "account", account.ID, "err", err)
	}
	return nil
}
```

The request body is the account itself rather than an envelope around it, but it
carries the same fields every other endpoint sees, custom auth fields included,
so the account gate applies here too.

**A non-2xx response aborts the deletion in Fibery.** Return an error only when
the caller should retry. If cleanup is permanently impossible — token already
revoked, remote 404 — log it and return nil, or the user is left unable to
delete their account.

## Serving other content

To serve non-integration routes (static pages, health checks) on the same server
under the shared middleware, register them with `Server.Handle` before `Serve`:

```go
server.Handle("GET /tos", tosHandler)
```

Alternatively, mount the whole integration server under a base path in your own
mux and build the `http.Server` yourself:

```go
root := http.NewServeMux()
root.Handle("/", myOtherContent)
root.Handle("/quickbooks/", server.Handler()) // integration routes are already prefixed
```

## Notes

- Register integrations before calling `Serve`.
- `WithIntegrationID` and `WithBasePath` control route prefixes.
- OAuth `access_token` responses are the integration `AccountFields` type.
- `WebhookPreProcess` receives `rawBody []byte` (exact wire bytes for signature/HMAC verification), typed `headers`, and a framework-decoded `notification`. Use `DecodeJSON(rawBody, &v, strict)` if custom typed decoding of the raw body is also required.
