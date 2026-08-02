package fibsync

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// ServerOption configures shared server-level behaviour that applies to all
// integrations registered with a Server.
type ServerOption func(*serverOptions)

type serverOptions struct {
	env        EnvProvider
	httpClient *http.Client
	maxBody    int64
	strictJSON bool
}

func defaultServerOptions() serverOptions {
	return serverOptions{
		env:        packageEnvProvider(),
		httpClient: http.DefaultClient,
	}
}

func applyServerOptions(o *serverOptions, opts ...ServerOption) {
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	if o.env == nil {
		o.env = packageEnvProvider()
	}
	if o.httpClient == nil {
		o.httpClient = http.DefaultClient
	}
}

// WithEnv sets the environment provider used by all integrations on this server.
// Passing nil resets to the package-level default (OSEnv).
func WithEnv(provider EnvProvider) ServerOption {
	return func(o *serverOptions) {
		if provider == nil {
			o.env = packageEnvProvider()
			return
		}
		o.env = provider
	}
}

// WithHTTPClient sets the HTTP client propagated to each integration's Init
// call. If not set (or set to nil), http.DefaultClient is used.
func WithHTTPClient(client *http.Client) ServerOption {
	return func(o *serverOptions) {
		o.httpClient = client
	}
}

// WithMaxBody limits the request body size (in bytes) for JSON decoding and raw
// webhook pre-processing across all integrations. Zero means no limit.
func WithMaxBody(maxBody int64) ServerOption {
	return func(o *serverOptions) {
		o.maxBody = maxBody
	}
}

// WithStrictJSON causes JSON decoding to reject unknown fields.
func WithStrictJSON(strict bool) ServerOption {
	return func(o *serverOptions) {
		o.strictJSON = strict
	}
}

// HandlerOption configures per-integration routing behaviour.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	basePath      string
	integrationID string
	gate          accountGate
}

// WithBasePath sets the base path prefix for integration routes.
// See WithIntegrationID for the full routing rules.
func WithBasePath(basePath string) HandlerOption {
	return func(o *handlerOptions) {
		o.basePath = basePath
	}
}

// WithIntegrationID sets the integration ID used for routing and logging.
// Routing rules:
//   - basePath == "" and id == "" -> "/"
//   - basePath == "" and id != "" -> "/{id}/"
//   - basePath != "" and id == "" -> "/{basePath}/"
//   - basePath != "" and id != "" -> "/{basePath}/{id}/"
func WithIntegrationID(id string) HandlerOption {
	return func(o *handlerOptions) {
		o.integrationID = id
	}
}

// ServeOption configures the underlying http.Server.
type ServeOption func(*serveOptions)

type serveOptions struct {
	middleware        []Middleware
	baseContext       func(net.Listener) context.Context
	maxHeaderBytes    *int64
	readHeaderTimeout *time.Duration
	readTimeout       *time.Duration
	writeTimeout      *time.Duration
	idleTimeout       *time.Duration
}

func applyServeOptions(opts ...ServeOption) serveOptions {
	options := serveOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}

// WithServeMiddleware applies middleware in the order provided.
// The first middleware wraps the next (outermost).
func WithServeMiddleware(middleware ...Middleware) ServeOption {
	return func(o *serveOptions) {
		o.middleware = append(o.middleware, middleware...)
	}
}

// WithBaseContext sets the base context function for the HTTP server.
func WithBaseContext(ctx func(net.Listener) context.Context) ServeOption {
	return func(o *serveOptions) {
		o.baseContext = ctx
	}
}

// WithMaxHeaderBytes sets the maximum size of request headers.
func WithMaxHeaderBytes(n int64) ServeOption {
	return func(o *serveOptions) {
		o.maxHeaderBytes = &n
	}
}

// WithReadHeaderTimeout sets the time allowed to read request headers.
func WithReadHeaderTimeout(dur time.Duration) ServeOption {
	return func(o *serveOptions) {
		o.readHeaderTimeout = &dur
	}
}

// WithReadTimeout sets the maximum duration for reading the entire request.
func WithReadTimeout(dur time.Duration) ServeOption {
	return func(o *serveOptions) {
		o.readTimeout = &dur
	}
}

// WithWriteTimeout sets the maximum duration before timing out writes.
func WithWriteTimeout(dur time.Duration) ServeOption {
	return func(o *serveOptions) {
		o.writeTimeout = &dur
	}
}

// WithIdleTimeout sets the maximum time to wait for the next request.
func WithIdleTimeout(dur time.Duration) ServeOption {
	return func(o *serveOptions) {
		o.idleTimeout = &dur
	}
}

// Server provides shared defaults for integration handlers.
type Server struct {
	mux     *http.ServeMux
	opts    serverOptions
	started atomic.Bool

	// publicPaths are fully-prefixed route paths registered by integrations
	// that must bypass the account-facing IP allowlist (e.g. webhook relays).
	// Populated by AddIntegration; consumed by IPAllowlistMiddleware.
	publicPaths []string
}

// ErrServerStarted is returned when Serve or AddIntegration is called after
// the server has started.
var ErrServerStarted = errors.New("server already started")

// NewServer creates a server with shared defaults for all integrations.
func NewServer(opts ...ServerOption) *Server {
	options := defaultServerOptions()
	applyServerOptions(&options, opts...)

	return &Server{
		mux:  http.NewServeMux(),
		opts: options,
	}
}

// Handler returns the server's http.Handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Handle registers an additional handler on the server's mux, alongside
// integration routes and under the same serve middleware. Patterns use
// net/http.ServeMux syntax (e.g. "GET /tos"). It must be called before Serve;
// it returns ErrServerStarted otherwise. Patterns must not duplicate an
// already-registered pattern (net/http.ServeMux panics on conflicts).
func (s *Server) Handle(pattern string, handler http.Handler) error {
	if s == nil {
		return errors.New("fibery: nil server")
	}
	if s.started.Load() {
		return ErrServerStarted
	}
	s.mux.Handle(pattern, handler)
	return nil
}

// IPAllowlistMiddleware builds an IP allowlist (see the package-level
// IPAllowlistMiddleware) that automatically exempts the framework's public
// paths — routes such as webhook relays that receive requests from an upstream
// provider rather than from Fibery. Call it after registering integrations so
// their public paths are known. Wrap the result in SkipWhen to add further
// application-specific exemptions (for example static pages).
func (s *Server) IPAllowlistMiddleware(cfg IPAllowlist) Middleware {
	public := make(map[string]struct{}, len(s.publicPaths))
	for _, p := range s.publicPaths {
		public[p] = struct{}{}
	}
	return SkipWhen(IPAllowlistMiddleware(cfg), func(r *http.Request) bool {
		_, ok := public[r.URL.Path]
		return ok
	})
}

// BuildHTTPServer constructs an http.Server with optional middleware applied.
// It does not start listening.
func (s *Server) BuildHTTPServer(addr string, opts ...ServeOption) *http.Server {
	if s == nil {
		panic("fibery: nil server")
	}

	options := applyServeOptions(opts...)

	handler := s.Handler()
	if len(options.middleware) > 0 {
		handler = applyMiddleware(handler, options.middleware...)
	}

	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	if options.baseContext != nil {
		server.BaseContext = options.baseContext
	}
	if options.maxHeaderBytes != nil {
		server.MaxHeaderBytes = int(*options.maxHeaderBytes)
	}
	if options.readHeaderTimeout != nil {
		server.ReadHeaderTimeout = *options.readHeaderTimeout
	}
	if options.readTimeout != nil {
		server.ReadTimeout = *options.readTimeout
	}
	if options.writeTimeout != nil {
		server.WriteTimeout = *options.writeTimeout
	}
	if options.idleTimeout != nil {
		server.IdleTimeout = *options.idleTimeout
	}

	return server
}

// Serve starts an HTTP server with optional middleware applied.
// It returns ErrServerStarted if called more than once.
func (s *Server) Serve(addr string, opts ...ServeOption) error {
	if s.started.Swap(true) {
		return ErrServerStarted
	}
	return s.BuildHTTPServer(addr, opts...).ListenAndServe()
}

// NewHandler creates a handler using the server's resolved options and
// per-integration routing overrides. It does not call Init or register routes.
func NewHandler[
	AccountFields,
	Page,
	WebhookParams,
	WebhookReply,
	WebhookHeaders,
	WebhookNotification,
	ResourceParams,
	Oauth1Extra,
	Oauth2Extra any,
](server *Server, integration IntegrationCore[AccountFields, Page], opts ...HandlerOption) *integrationHandler[
	AccountFields,
	Page,
	WebhookParams,
	WebhookReply,
	WebhookHeaders,
	WebhookNotification,
	ResourceParams,
	Oauth1Extra,
	Oauth2Extra,
] {
	if server == nil {
		panic("fibery: nil server")
	}

	handlerOpts := handlerOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&handlerOpts)
		}
	}

	return &integrationHandler[
		AccountFields,
		Page,
		WebhookParams,
		WebhookReply,
		WebhookHeaders,
		WebhookNotification,
		ResourceParams,
		Oauth1Extra,
		Oauth2Extra,
	]{
		integration: integration,
		serverOpts:  server.opts,
		handlerOpts: handlerOpts,
	}
}

// initConfig builds the InitConfig from the resolved server options.
// If no HTTP client was configured, http.DefaultClient is used.
func initConfig(o serverOptions) InitConfig {
	return InitConfig{
		Env:        o.env,
		HTTPClient: o.httpClient,
		Logger:     slog.Default(),
		MaxBody:    o.maxBody,
	}
}

// AddIntegration initializes and registers an integration with the server.
// It must be called before Serve; it returns ErrServerStarted otherwise.
// Init is invoked once with the resolved InitConfig before routes are registered.
func AddIntegration[
	AccountFields,
	Page,
	WebhookParams,
	WebhookReply,
	WebhookHeaders,
	WebhookNotification,
	ResourceParams,
	Oauth1Extra,
	Oauth2Extra any,
](ctx context.Context, server *Server, integration IntegrationCore[AccountFields, Page], handlerOpts ...HandlerOption) error {
	if server == nil {
		return errors.New("fibery: nil server")
	}
	if server.started.Load() {
		return ErrServerStarted
	}

	handler := NewHandler[
		AccountFields,
		Page,
		WebhookParams,
		WebhookReply,
		WebhookHeaders,
		WebhookNotification,
		ResourceParams,
		Oauth1Extra,
		Oauth2Extra,
	](server, integration, handlerOpts...)

	if handler.handlerOpts.gate.enabled && handler.handlerOpts.gate.validate == nil {
		return errors.New("fibery: WithAccountGate requires a non-empty Secret or a Validate function")
	}

	if err := handler.init(ctx, initConfig(handler.serverOpts)); err != nil {
		return err
	}

	if err := handler.RegisterRoutes(server.mux); err != nil {
		return err
	}

	server.publicPaths = append(server.publicPaths, handler.publicPaths...)
	return nil
}
