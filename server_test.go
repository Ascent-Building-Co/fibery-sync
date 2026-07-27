package fibery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testEnvProvider struct {
	name string
}

func (t testEnvProvider) String(string) (string, bool)                   { return t.name, true }
func (t testEnvProvider) RequiredString(string) (string, error)          { return t.name, nil }
func (t testEnvProvider) Int(string) (int, bool)                         { return 0, false }
func (t testEnvProvider) RequiredInt(string) (int, error)                { return 0, nil }
func (t testEnvProvider) Bool(string) (bool, bool)                       { return false, false }
func (t testEnvProvider) RequiredBool(string) (bool, error)              { return false, nil }
func (t testEnvProvider) Duration(string) (time.Duration, bool)          { return 0, false }
func (t testEnvProvider) RequiredDuration(string) (time.Duration, error) { return 0, nil }

type minimalIntegration struct{}

func (minimalIntegration) Init(_ context.Context, _ InitConfig) error { return nil }
func (minimalIntegration) Config(context.Context) (Config, *IntegrationError) {
	return Config{ID: "id", Name: "name", Version: "1"}, nil
}
func (minimalIntegration) Validate(context.Context, ValidateRequest[testAccountFields]) (*testAccountFields, *IntegrationError) {
	return nil, nil
}
func (minimalIntegration) SyncConfig(context.Context, SyncMetaRequest[testAccountFields]) (SyncConfig, *IntegrationError) {
	return SyncConfig{}, nil
}
func (minimalIntegration) SyncSchema(context.Context, SyncMetaRequest[testAccountFields]) (SyncSchema, *IntegrationError) {
	return SyncSchema{}, nil
}
func (minimalIntegration) SyncData(context.Context, DataRequest[testAccountFields, testPage]) (DataResponse[testPage], *IntegrationError) {
	return DataResponse[testPage]{}, nil
}

type countingIntegration struct {
	calls atomic.Int32
}

func (c *countingIntegration) Init(_ context.Context, _ InitConfig) error {
	c.calls.Add(1)
	return nil
}

func (*countingIntegration) Config(context.Context) (Config, *IntegrationError) {
	return Config{ID: "id", Name: "name", Version: "1"}, nil
}

func (*countingIntegration) Validate(context.Context, ValidateRequest[testAccountFields]) (*testAccountFields, *IntegrationError) {
	return nil, nil
}

func (*countingIntegration) SyncConfig(context.Context, SyncMetaRequest[testAccountFields]) (SyncConfig, *IntegrationError) {
	return SyncConfig{}, nil
}

func (*countingIntegration) SyncSchema(context.Context, SyncMetaRequest[testAccountFields]) (SyncSchema, *IntegrationError) {
	return SyncSchema{}, nil
}

func (*countingIntegration) SyncData(context.Context, DataRequest[testAccountFields, testPage]) (DataResponse[testPage], *IntegrationError) {
	return DataResponse[testPage]{}, nil
}

func TestServerOptionsDefaults(t *testing.T) {
	server := NewServer()
	if server.opts.env == nil {
		t.Fatalf("expected default env provider")
	}
	if _, ok := server.opts.env.(OSEnv); !ok {
		t.Fatalf("expected OSEnv default")
	}
	if server.opts.httpClient != http.DefaultClient {
		t.Fatalf("expected http.DefaultClient as default")
	}
}

func TestWithHTTPClient(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	server := NewServer(WithHTTPClient(client))
	if server.opts.httpClient != client {
		t.Fatalf("expected custom http client to be set")
	}
}

func TestInitConfigHTTPClientDefault(t *testing.T) {
	opts := defaultServerOptions()
	cfg := initConfig(opts)
	if cfg.HTTPClient != http.DefaultClient {
		t.Fatalf("expected http.DefaultClient when none configured")
	}
}

func TestInitConfigHTTPClientCustom(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	opts := defaultServerOptions()
	opts.httpClient = client
	cfg := initConfig(opts)
	if cfg.HTTPClient != client {
		t.Fatalf("expected custom http client in InitConfig")
	}
}

func TestWithEnvNilResetsToDefault(t *testing.T) {
	customEnv := testEnvProvider{name: "custom"}
	server := NewServer(
		WithEnv(customEnv),
		WithEnv(nil),
	)
	if _, ok := server.opts.env.(OSEnv); !ok {
		t.Fatalf("expected env to reset to OSEnv default after nil")
	}
}

func TestHandlerInitOnce(t *testing.T) {
	integration := &countingIntegration{}
	server := NewServer()
	handler := NewHandler[testAccountFields, testPage,
		struct{}, struct{}, struct{}, struct{}, struct{}, struct{}, struct{},
	](server, integration)
	cfg := InitConfig{Env: OSEnv{}, HTTPClient: http.DefaultClient}
	if err := handler.init(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := handler.init(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error on second init: %v", err)
	}
	if got := integration.calls.Load(); got != 1 {
		t.Fatalf("expected init to be called once, got %d", got)
	}
}

func TestWithEnv(t *testing.T) {
	customEnv := testEnvProvider{name: "custom"}
	server := NewServer(WithEnv(customEnv))
	if !reflect.DeepEqual(server.opts.env, customEnv) {
		t.Fatalf("expected custom env provider")
	}
}

func TestBuildHTTPServerOptions(t *testing.T) {
	server := NewServer()
	readTimeout := 5 * time.Second
	writeTimeout := 10 * time.Second
	idleTimeout := 15 * time.Second
	readHeaderTimeout := 3 * time.Second
	maxHeaderBytes := int64(1024)
	baseContext := func(net.Listener) context.Context {
		return context.Background()
	}

	httpServer := server.BuildHTTPServer(":0",
		WithReadTimeout(readTimeout),
		WithWriteTimeout(writeTimeout),
		WithIdleTimeout(idleTimeout),
		WithReadHeaderTimeout(readHeaderTimeout),
		WithMaxHeaderBytes(maxHeaderBytes),
		WithBaseContext(baseContext),
	)

	if httpServer.ReadTimeout != readTimeout {
		t.Fatalf("expected ReadTimeout %v, got %v", readTimeout, httpServer.ReadTimeout)
	}
	if httpServer.WriteTimeout != writeTimeout {
		t.Fatalf("expected WriteTimeout %v, got %v", writeTimeout, httpServer.WriteTimeout)
	}
	if httpServer.IdleTimeout != idleTimeout {
		t.Fatalf("expected IdleTimeout %v, got %v", idleTimeout, httpServer.IdleTimeout)
	}
	if httpServer.ReadHeaderTimeout != readHeaderTimeout {
		t.Fatalf("expected ReadHeaderTimeout %v, got %v", readHeaderTimeout, httpServer.ReadHeaderTimeout)
	}
	if httpServer.MaxHeaderBytes != int(maxHeaderBytes) {
		t.Fatalf("expected MaxHeaderBytes %v, got %v", maxHeaderBytes, httpServer.MaxHeaderBytes)
	}
	if httpServer.BaseContext == nil {
		t.Fatalf("expected BaseContext to be set")
	}
}

func TestBuildHTTPServerMiddlewareOrder(t *testing.T) {
	server := NewServer()
	order := []string{}

	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":before")
				next.ServeHTTP(w, r)
				order = append(order, name+":after")
			})
		}
	}

	server.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusOK)
	})

	httpServer := server.BuildHTTPServer(":0", WithServeMiddleware(mw("mw1"), mw("mw2")))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(res, req)

	expected := []string{"mw1:before", "mw2:before", "handler", "mw2:after", "mw1:after"}
	if !reflect.DeepEqual(order, expected) {
		t.Fatalf("unexpected middleware order: %v", order)
	}
}

func TestServeReturnsStartedError(t *testing.T) {
	server := NewServer()
	server.started.Store(true)

	if err := server.Serve(":0"); err != ErrServerStarted {
		t.Fatalf("expected ErrServerStarted, got %v", err)
	}
}

func TestAddIntegrationReturnsStartedError(t *testing.T) {
	server := NewServer()
	server.started.Store(true)

	err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](context.Background(), server, minimalIntegration{})
	if err != ErrServerStarted {
		t.Fatalf("expected ErrServerStarted, got %v", err)
	}
}

type routesIntegration struct {
	minimalIntegration
	routes []CustomRoute
}

func (r *routesIntegration) Routes() []CustomRoute {
	return r.routes
}

func TestAddIntegrationCustomRoute(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	server := NewServer()
	integ := &routesIntegration{
		routes: []CustomRoute{
			{Method: http.MethodPost, Path: "/relay", Handler: handler},
		},
	}

	err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](
		context.Background(), server, integ, WithIntegrationID("qb"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/qb/relay", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if !called {
		t.Fatalf("expected custom route handler to be called")
	}
	if res.Result().StatusCode != http.StatusTeapot {
		t.Fatalf("expected 418, got %d", res.Result().StatusCode)
	}
}

func TestAddIntegrationCustomRouteMiddlewareOrder(t *testing.T) {
	order := []string{}

	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":before")
				next.ServeHTTP(w, r)
				order = append(order, name+":after")
			})
		}
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusOK)
	})

	server := NewServer()
	integ := &routesIntegration{
		routes: []CustomRoute{
			{
				Method:     http.MethodPost,
				Path:       "/relay",
				Handler:    handler,
				Middleware: []Middleware{mw("a"), mw("b")},
			},
		},
	}

	err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](
		context.Background(), server, integ, WithIntegrationID("qb"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/qb/relay", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	expected := []string{"a:before", "b:before", "handler", "b:after", "a:after"}
	if !reflect.DeepEqual(order, expected) {
		t.Fatalf("unexpected middleware order: %v", order)
	}
}

func TestAddIntegrationCustomRouteCollision(t *testing.T) {
	cases := []struct {
		name  string
		route CustomRoute
		want  string
	}{
		{
			name:  "framework collision",
			route: CustomRoute{Method: http.MethodPost, Path: "/validate", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})},
			want:  "conflicts with framework-reserved route",
		},
		{
			name:  "empty method",
			route: CustomRoute{Method: "", Path: "/x", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})},
			want:  "method is empty",
		},
		{
			name:  "empty path",
			route: CustomRoute{Method: http.MethodPost, Path: "", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})},
			want:  "path is empty",
		},
		{
			name:  "nil handler",
			route: CustomRoute{Method: http.MethodPost, Path: "/x", Handler: nil},
			want:  "handler is nil",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer()
			integ := &routesIntegration{routes: []CustomRoute{tt.route}}

			err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](
				context.Background(), server, integ, WithIntegrationID("qb"),
			)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func TestAddIntegrationDifferentMethodSamePath(t *testing.T) {
	// Strictly correct: GET on a framework POST path is not a collision.
	called := false
	server := NewServer()
	integ := &routesIntegration{
		routes: []CustomRoute{
			{
				Method: http.MethodGet,
				Path:   "/validate",
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusOK)
				}),
			},
		},
	}

	err := AddIntegration[testAccountFields, testPage, testWebhookParams, testWebhookReply, testWebhookHeaders, testWebhookNotification, testResourceParams, struct{}, struct{}](
		context.Background(), server, integ, WithIntegrationID("qb"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/qb/validate", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)

	if !called {
		t.Fatalf("expected GET handler to be called")
	}
}
