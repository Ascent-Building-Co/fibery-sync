package fibsync

import (
	"context"
	"net/http"
)

// IntegrationCore is the minimal interface for a Fibery integration.
type IntegrationCore[AccountFields, Page any] interface {
	// Init is called once before the server begins serving requests. cfg provides
	// the resolved server configuration. Use cfg.Env to read environment
	// variables, cfg.Mode to branch on dev/prod, cfg.HTTPClient for outbound
	// requests (defaults to http.DefaultClient), and cfg.Logger for structured
	// logging via slog.
	Init(ctx context.Context, cfg InitConfig) error
	Config(ctx context.Context) (Config, *IntegrationError)
	Validate(ctx context.Context, req ValidateRequest[AccountFields]) (*AccountFields, *IntegrationError)
	SyncConfig(ctx context.Context, req SyncMetaRequest[AccountFields]) (SyncConfig, *IntegrationError)
	SyncSchema(ctx context.Context, req SyncMetaRequest[AccountFields]) (SyncSchema, *IntegrationError)
	SyncData(ctx context.Context, req DataRequest[AccountFields, Page]) (DataResponse[Page], *IntegrationError)
}

type IntegrationWithWebhooks[WHParams, WHReply, AccountFields, Headers, Notification any] interface {
	WebhookInit(ctx context.Context, req WebhookInitRequest[AccountFields, WHParams]) (WebhookConfig[WHParams], *IntegrationError)
	WebhookTransform(ctx context.Context, req WebhookTransformRequest[Headers, Notification, AccountFields]) (WebhookTransformResponse, *IntegrationError)
}

type IntegrationWithWebhooksPreProcess[Headers, Notification, WHReply any] interface {
	// WebhookPreProcess validates an incoming webhook and returns the workspace
	// IDs that should receive the update. rawBody contains the exact request
	// bytes as received, suitable for signature or HMAC verification. headers
	// and notification are decoded by the framework using server defaults
	// (strict mode, size limits) so implementations do not need to re-decode
	// them. Use DecodeJSON(rawBody, &v, strict) when custom typed decoding of
	// the raw body is also required.
	WebhookPreProcess(ctx context.Context, rawBody []byte, headers Headers, notification Notification) (WebhookPreProcessResponse[WHReply], *IntegrationError)
}

type IntegrationWithOAuth1[AccountFields, Oauth1Extra any] interface {
	Oauth1Authorize(ctx context.Context, req Oauth1AuthRequest) (Oauth1AuthResponse, *IntegrationError)
	Oauth1Token(ctx context.Context, req Oauth1TokenRequest[Oauth1Extra]) (AccountFields, *IntegrationError)
}

type IntegrationWithOAuth2[AccountFields, Oauth2Extra any] interface {
	Oauth2Authorize(ctx context.Context, req Oauth2AuthRequest) (Oauth2AuthResponse, *IntegrationError)
	Oauth2TokenRequest(ctx context.Context, req Oauth2TokenRequest[Oauth2Extra]) (AccountFields, *IntegrationError)
}

type IntegrationWithLogo interface {
	Logo(ctx context.Context) (*LogoResponse, *IntegrationError)
}

type IntegrationWithDatalist[AccountFields any] interface {
	SyncDatalist(ctx context.Context, req DatalistRequest[AccountFields]) (DatalistResponse, *IntegrationError)
}

type IntegrationWithFilter[AccountFields any] interface {
	SyncFilterValidate(ctx context.Context, req FilterValidateRequest[AccountFields]) *IntegrationError
}

type IntegrationWithResource[AccountFields, ResourceParams any] interface {
	SyncResource(ctx context.Context, req ResourceRequest[AccountFields, ResourceParams]) (ResourceStream, *IntegrationError)
}

type IntegrationWithAction[AccountFields any] interface {
	Action(ctx context.Context, req ActionRequest[AccountFields]) (ActionResponse, *IntegrationError)
}

// IntegrationWithWebhookRelay adds a framework-managed webhook relay at
// {prefix}/webhook. It exists for providers whose webhooks cannot be delivered
// directly to Fibery (for example a Content-Type Fibery rejects, or a single
// callback URL fronting multiple endpoints): the provider is pointed at the
// relay, which forwards each delivery on to the resolved target and mirrors the
// response back.
//
// The framework owns the mechanics — reading the body (subject to the server's
// max-body limit), stripping hop-by-hop headers, forwarding, applying the
// timeout, and mirroring the upstream status/headers/body. The integration only
// resolves where a given delivery should go. The relay path is registered as a
// public path, so Server.IPAllowlistMiddleware exempts it automatically.
type IntegrationWithWebhookRelay interface {
	// WebhookRelay resolves the forwarding target for a webhook received at
	// {prefix}/webhook. rawBody and header are the inbound request's body and
	// headers. Return a non-nil *IntegrationError to reject the delivery; its
	// Status becomes the response status.
	WebhookRelay(ctx context.Context, rawBody []byte, header http.Header) (RelayTarget, *IntegrationError)
}

// RelayTarget describes where IntegrationWithWebhookRelay forwards a delivery.
type RelayTarget struct {
	// URL is the destination the delivery is POSTed to. Required.
	URL string
	// ContentType, when non-empty, overrides the forwarded Content-Type header.
	ContentType string
	// Header, when non-nil, sets additional headers on the outbound request
	// (applied after inbound headers are copied, so it overrides them).
	Header http.Header
}

// CustomRoute is an integration-declared HTTP route.
//
// Path is relative to the integration's prefix; the framework prepends the
// effective prefix from WithBasePath / WithIntegrationID using the same rules
// as built-in routes. Method must be a non-empty HTTP method.
//
// Middleware is applied outermost-first: Middleware[0] wraps Middleware[1]
// wraps ... wraps Handler.
type CustomRoute struct {
	Method     string
	Path       string
	Handler    http.Handler
	Middleware []Middleware
}

// IntegrationWithRoutes lets an integration declare additional HTTP routes
// scoped under its integration prefix. Routes returned by this method are
// registered after the framework's own routes; method+path collisions with
// framework-reserved routes produce a startup error from AddIntegration.
type IntegrationWithRoutes interface {
	Routes() []CustomRoute
}
