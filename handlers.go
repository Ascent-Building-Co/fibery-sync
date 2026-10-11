package fibsync

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

type integrationHandler[
	AccountFields,
	Page,
	WebhookParams,
	WebhookReply,
	WebhookHeaders,
	WebhookNotification,
	ResourceParams,
	Oauth1Extra,
	Oauth2Extra any,
] struct {
	integration IntegrationCore[AccountFields, Page]
	serverOpts  serverOptions
	handlerOpts handlerOptions

	// publicPaths holds fully-prefixed route paths the framework registers that
	// must bypass the account-facing IP allowlist (e.g. the webhook relay, which
	// receives requests from the upstream provider rather than Fibery). Populated
	// during RegisterRoutes and lifted onto the Server by AddIntegration.
	publicPaths []string

	initOnce  sync.Once
	initError error
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) init(ctx context.Context, cfg InitConfig) error {
	i.initOnce.Do(func() {
		i.initError = i.integration.Init(ctx, cfg)
	})
	return i.initError
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleConfig(w http.ResponseWriter, r *http.Request) {
	resp, err := i.integration.Config(r.Context())
	if err != nil {
		respondWithError(w, err)
		return
	}

	if i.handlerOpts.gate.enabled {
		field := AuthField{
			ID:          accountGateFieldID,
			Title:       cmp.Or(i.handlerOpts.gate.title, "Access Password"),
			Type:        ConfigPasswordField,
			Description: i.handlerOpts.gate.description,
		}
		// Copy rather than append in place: the integration may return a
		// cached Config, and mutating its slices would duplicate the gate
		// field on every request.
		auths := make([]Authentication, len(resp.Authentication))
		copy(auths, resp.Authentication)
		for j := range auths {
			fields := make([]AuthField, len(auths[j].Fields), len(auths[j].Fields)+1)
			copy(fields, auths[j].Fields)
			auths[j].Fields = append(fields, field)
		}
		resp.Authentication = auths
	}

	respondWithJSON(w, http.StatusOK, resp)
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleValidate(w http.ResponseWriter, r *http.Request) {
	var req ValidateRequest[AccountFields]
	if !i.decode(w, r, &req) {
		return
	}
	if !i.authorize(w, req.Fields) {
		return
	}

	resp, err := i.integration.Validate(r.Context(), req)
	if err != nil {
		respondWithError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, resp)
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleSyncConfig(w http.ResponseWriter, r *http.Request) {
	var req SyncMetaRequest[AccountFields]
	if !i.decode(w, r, &req) {
		return
	}
	if !i.authorize(w, req.Account) {
		return
	}

	resp, err := i.integration.SyncConfig(r.Context(), req)
	if err != nil {
		respondWithError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, resp)
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleSyncSchema(w http.ResponseWriter, r *http.Request) {
	var req SyncMetaRequest[AccountFields]
	if !i.decode(w, r, &req) {
		return
	}
	if !i.authorize(w, req.Account) {
		return
	}

	resp, err := i.integration.SyncSchema(r.Context(), req)
	if err != nil {
		respondWithError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, resp)
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleSyncData(w http.ResponseWriter, r *http.Request) {
	var req DataRequest[AccountFields, Page]
	if !i.decode(w, r, &req) {
		return
	}
	if !i.authorize(w, req.Account) {
		return
	}

	resp, err := i.integration.SyncData(r.Context(), req)
	if err != nil {
		respondWithError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, resp)
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleWebhookInit(webhooks IntegrationWithWebhooks[WebhookParams, WebhookReply, AccountFields, WebhookHeaders, WebhookNotification]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req WebhookInitRequest[AccountFields, WebhookParams]
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorize(w, req.Account) {
			return
		}

		resp, err := webhooks.WebhookInit(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleWebhookPreProcess(webhooks IntegrationWithWebhooksPreProcess[WebhookHeaders, WebhookNotification, WebhookReply]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			respondWithError(w, BadRequest("missing request body"))
			return
		}
		if i.serverOpts.maxBody > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, i.serverOpts.maxBody)
		}
		defer r.Body.Close()
		rawBody, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](readErr); ok {
				respondWithError(w, BadRequest("request body too large"))
				return
			}
			respondWithError(w, InternalError("unable to read request body"))
			return
		}
		if len(rawBody) == 0 {
			respondWithError(w, BadRequest("empty request body"))
			return
		}

		var notification WebhookNotification
		if err := DecodeJSON(rawBody, &notification, i.serverOpts.strictJSON); err != nil {
			respondWithError(w, err)
			return
		}

		headers, err := i.decodeWebhookHeaders(r)
		if err != nil {
			respondWithError(w, err)
			return
		}

		resp, err := webhooks.WebhookPreProcess(r.Context(), rawBody, headers, notification)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleWebhookTransform(webhooks IntegrationWithWebhooks[WebhookParams, WebhookReply, AccountFields, WebhookHeaders, WebhookNotification]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req WebhookTransformRequest[WebhookHeaders, WebhookNotification, AccountFields]
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorize(w, req.Account) {
			return
		}

		resp, err := webhooks.WebhookTransform(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleOauth1Authorize(oauth1 IntegrationWithOAuth1[AccountFields, Oauth1Extra]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Oauth1AuthRequest
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorizeFields(w, req.Fields) {
			return
		}

		resp, err := oauth1.Oauth1Authorize(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleOauth1Token(oauth1 IntegrationWithOAuth1[AccountFields, Oauth1Extra]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Oauth1TokenRequest[Oauth1Extra]
		if !i.decode(w, r, &req) {
			return
		}

		resp, err := oauth1.Oauth1Token(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleOauth2Authorize(oauth2 IntegrationWithOAuth2[AccountFields, Oauth2Extra]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Oauth2AuthRequest
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorizeFields(w, req.Fields) {
			return
		}

		resp, err := oauth2.Oauth2Authorize(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleOauth2Token(oauth2 IntegrationWithOAuth2[AccountFields, Oauth2Extra]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Oauth2TokenRequest[Oauth2Extra]
		if !i.decode(w, r, &req) {
			return
		}

		resp, err := oauth2.Oauth2TokenRequest(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleLogo(logo IntegrationWithLogo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := logo.Logo(r.Context())
		if err != nil {
			// Per the Fibery custom-app spec, any failure here should fall
			// back to the default logo via 204. Log the cause so operators
			// can diagnose, but never surface 4xx/5xx to Fibery.
			args := []any{"status", err.Status, "message", err.Message}
			if i.handlerOpts.integrationID != "" {
				args = append(args, "integration_id", i.handlerOpts.integrationID)
			}
			slog.Warn("logo handler error", args...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if resp == nil || (resp.Body == nil && resp.URL == nil) {
			// No logo configured.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if resp.Body == nil {
			http.Redirect(w, r, resp.URL.String(), http.StatusFound)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(http.StatusOK)
		if _, copyErr := io.Copy(w, resp.Body); copyErr != nil {
			i.logError("logo stream copy failed", copyErr)
		}
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleDatalist(datalist IntegrationWithDatalist[AccountFields]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req DatalistRequest[AccountFields]
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorize(w, req.Account) {
			return
		}

		resp, err := datalist.SyncDatalist(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleFilterValidate(filter IntegrationWithFilter[AccountFields]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req FilterValidateRequest[AccountFields]
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorize(w, req.Account) {
			return
		}

		if err := filter.SyncFilterValidate(r.Context(), req); err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, struct{}{})
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleResource(resource IntegrationWithResource[AccountFields, ResourceParams]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ResourceRequest[AccountFields, ResourceParams]
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorize(w, req.Account) {
			return
		}

		resp, err := resource.SyncResource(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}
		if resp.Body == nil {
			respondWithError(w, InternalError("missing resource body"))
			return
		}
		defer resp.Body.Close()

		if resp.ContentType != "" {
			w.Header().Set("Content-Type", resp.ContentType)
		}
		if resp.Length != nil {
			w.Header().Set("Content-Length", strconv.FormatInt(*resp.Length, 10))
		}
		if resp.Filename != "" {
			w.Header().Set("Content-Disposition", "attachment; filename=\""+resp.Filename+"\"")
		}
		w.WriteHeader(http.StatusOK)
		if _, copyErr := io.Copy(w, resp.Body); copyErr != nil {
			i.logError("resource stream copy failed", copyErr)
		}
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleAction(actions IntegrationWithAction[AccountFields]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ActionRequest[AccountFields]
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorize(w, req.Account) {
			return
		}

		resp, err := actions.Action(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleExecute(execute IntegrationWithExecute[AccountFields]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ExecuteRequest[AccountFields]
		if !i.decode(w, r, &req) {
			return
		}
		if !i.authorize(w, req.Account) {
			return
		}

		resp, err := execute.Execute(r.Context(), req)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, resp)
	}
}

// handleAccountDeletion decodes the bare account payload Fibery sends when an
// account is deleted. Any non-2xx from here interrupts the deletion, so the
// gate still applies: the gate value is a custom auth field and arrives with
// the rest of the account.
func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleAccountDeletion(deletion IntegrationWithAccountDeletion[AccountFields]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var account Account[AccountFields]
		if !i.decode(w, r, &account) {
			return
		}
		if !i.authorize(w, account) {
			return
		}

		if err := deletion.DeleteAccount(r.Context(), account); err != nil {
			respondWithError(w, err)
			return
		}

		respondWithJSON(w, http.StatusOK, nil)
	}
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) RegisterRoutes(mux *http.ServeMux) error {
	reserved := make(map[string]struct{})
	register := func(method, pattern string, h http.Handler) {
		key := method + " " + pattern
		reserved[key] = struct{}{}
		mux.Handle(key, h)
	}
	registerFunc := func(method, path string, h http.HandlerFunc) {
		register(method, i.route(path), h)
	}

	configPath := i.route("/") // "/quickbooks/"
	register(http.MethodGet, configPath, http.HandlerFunc(i.handleConfig))
	if strings.HasSuffix(configPath, "/") && len(configPath) > 1 {
		bare := strings.TrimSuffix(configPath, "/") // "/quickbooks"
		register(http.MethodGet, bare, http.HandlerFunc(i.handleConfig))
	}
	registerFunc(http.MethodPost, "/validate", i.handleValidate)
	registerFunc(http.MethodPost, "/api/v1/synchronizer/config", i.handleSyncConfig)
	registerFunc(http.MethodPost, "/api/v1/synchronizer/schema", i.handleSyncSchema)
	registerFunc(http.MethodPost, "/api/v1/synchronizer/data", i.handleSyncData)

	preProcessRegistered := false
	if preProcess, ok := i.integration.(IntegrationWithWebhooksPreProcess[WebhookHeaders, WebhookNotification, WebhookReply]); ok {
		registerFunc(http.MethodPost, "/api/v1/synchronizer/webhooks/pre-process", i.handleWebhookPreProcess(preProcess))
		preProcessRegistered = true
	}

	if preProcessRegistered {
		if webhooks, ok := i.integration.(IntegrationWithWebhooks[WebhookParams, WebhookReply, AccountFields, WebhookHeaders, WebhookNotification]); ok {
			registerFunc(http.MethodPost, "/api/v1/synchronizer/webhooks", i.handleWebhookInit(webhooks))
			registerFunc(http.MethodPost, "/api/v1/synchronizer/webhooks/transform", i.handleWebhookTransform(webhooks))
		}
	}

	if oauth1, ok := i.integration.(IntegrationWithOAuth1[AccountFields, Oauth1Extra]); ok {
		registerFunc(http.MethodPost, "/oauth1/v1/authorize", i.handleOauth1Authorize(oauth1))
		registerFunc(http.MethodPost, "/oauth1/v1/access_token", i.handleOauth1Token(oauth1))
	}

	if oauth2, ok := i.integration.(IntegrationWithOAuth2[AccountFields, Oauth2Extra]); ok {
		registerFunc(http.MethodPost, "/oauth2/v1/authorize", i.handleOauth2Authorize(oauth2))
		registerFunc(http.MethodPost, "/oauth2/v1/access_token", i.handleOauth2Token(oauth2))
	}

	if logo, ok := i.integration.(IntegrationWithLogo); ok {
		registerFunc(http.MethodGet, "/logo", i.handleLogo(logo))
	} else {
		registerFunc(http.MethodGet, "/logo", func(w http.ResponseWriter, r *http.Request) {
			respondWithJSON(w, http.StatusNoContent, nil)
		})
	}

	if datalist, ok := i.integration.(IntegrationWithDatalist[AccountFields]); ok {
		registerFunc(http.MethodPost, "/api/v1/synchronizer/datalist", i.handleDatalist(datalist))
	}

	// Fibery probes filter/validate unconditionally regardless of whether the
	// integration declares filter support. Always register the route: dispatch
	// to the real handler when IntegrationWithFilter is implemented, otherwise
	// register a 200 no-op so we don't return 405.
	if filter, ok := i.integration.(IntegrationWithFilter[AccountFields]); ok {
		registerFunc(http.MethodPost, "/api/v1/synchronizer/filter/validate", i.handleFilterValidate(filter))
	} else {
		slog.Debug("filter/validate registered as no-op (integration does not implement IntegrationWithFilter)",
			"integration_id", i.handlerOpts.integrationID,
		)
		registerFunc(http.MethodPost, "/api/v1/synchronizer/filter/validate", func(w http.ResponseWriter, r *http.Request) {
			respondWithJSON(w, http.StatusOK, nil)
		})
	}

	if resource, ok := i.integration.(IntegrationWithResource[AccountFields, ResourceParams]); ok {
		registerFunc(http.MethodPost, "/api/v1/synchronizer/resource", i.handleResource(resource))
	}

	if actions, ok := i.integration.(IntegrationWithAction[AccountFields]); ok {
		registerFunc(http.MethodPost, "/api/v1/automations/action/execute", i.handleAction(actions))
	}

	if execute, ok := i.integration.(IntegrationWithExecute[AccountFields]); ok {
		registerFunc(http.MethodPost, "/api/v1/synchronizer/execute", i.handleExecute(execute))
	}

	if deletion, ok := i.integration.(IntegrationWithAccountDeletion[AccountFields]); ok {
		registerFunc(http.MethodDelete, "/api/v1/account", i.handleAccountDeletion(deletion))
	}

	if relay, ok := i.integration.(IntegrationWithWebhookRelay); ok {
		registerFunc(http.MethodPost, "/webhooks", i.handleWebhookRelay(relay))
		i.publicPaths = append(i.publicPaths, i.route("/webhooks"))
	}

	if custom, ok := i.integration.(IntegrationWithRoutes); ok {
		for idx, route := range custom.Routes() {
			if route.Method == "" {
				return fmt.Errorf("custom route %d: method is empty", idx)
			}
			if route.Path == "" {
				return fmt.Errorf("custom route %d: path is empty", idx)
			}
			if route.Handler == nil {
				return fmt.Errorf("custom route %d (%s %s): handler is nil",
					idx, route.Method, route.Path)
			}
			pattern := i.route(route.Path)
			key := route.Method + " " + pattern
			if _, conflict := reserved[key]; conflict {
				return fmt.Errorf("custom route %d (%s): conflicts with framework-reserved route",
					idx, key)
			}
			reserved[key] = struct{}{}
			handler := applyMiddleware(route.Handler, route.Middleware...)
			mux.Handle(key, handler)
		}
	}

	return nil
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) authorize(w http.ResponseWriter, account Account[AccountFields]) bool {
	return i.checkGate(w, account.gate)
}

// authorizeFields gates an OAuth authorize request, which carries the auth
// form's values rather than an account. Gating here stops a wrong password
// before the user is sent to the provider's consent screen.
func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) authorizeFields(w http.ResponseWriter, fields map[string]any) bool {
	presented, _ := fields[accountGateFieldID].(string)
	return i.checkGate(w, presented)
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) checkGate(w http.ResponseWriter, presented string) bool {
	g := i.handlerOpts.gate
	if !g.enabled {
		return true
	}
	if g.validate == nil || !g.validate(presented) {
		respondWithError(w, Unauthorized("invalid access password"))
		return false
	}
	return true
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := readJSON(w, r, v, i.serverOpts.maxBody, i.serverOpts.strictJSON); err != nil {
		respondWithError(w, err)
		return false
	}

	return true
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) decodeWebhookHeaders(r *http.Request) (WebhookHeaders, *IntegrationError) {
	var headers WebhookHeaders
	value := reflect.ValueOf(&headers).Elem()
	typeInfo := value.Type()

	switch value.Kind() {
	case reflect.Map:
		if typeInfo.Key().Kind() != reflect.String {
			return headers, InternalError("webhook headers map key must be string")
		}
		if value.IsNil() {
			value.Set(reflect.MakeMap(typeInfo))
		}
		elemKind := typeInfo.Elem().Kind()
		for name, values := range r.Header {
			key := http.CanonicalHeaderKey(name)
			var mappedValue reflect.Value
			switch elemKind {
			case reflect.String:
				mappedValue = reflect.ValueOf(strings.Join(values, ","))
			case reflect.Slice:
				if typeInfo.Elem().Elem().Kind() != reflect.String {
					return headers, InternalError("webhook headers map value must be []string")
				}
				mappedValue = reflect.ValueOf(values)
			case reflect.Interface:
				mappedValue = reflect.ValueOf(values)
			default:
				return headers, InternalError("unsupported webhook headers map value type")
			}
			value.SetMapIndex(reflect.ValueOf(key), mappedValue)
		}
		return headers, nil
	case reflect.Struct:
		for idx := 0; idx < value.NumField(); idx++ {
			field := value.Field(idx)
			fieldType := typeInfo.Field(idx)
			if !field.CanSet() {
				continue
			}
			tag := fieldType.Tag.Get("header")
			if tag == "" || tag == "-" {
				continue
			}
			values := r.Header.Values(tag)
			if len(values) == 0 {
				continue
			}

			if err := setHeaderField(field, values); err != nil {
				return headers, err
			}
		}
		return headers, nil
	default:
		return headers, InternalError("unsupported webhook headers type")
	}
}

func setHeaderField(field reflect.Value, values []string) *IntegrationError {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		return setHeaderField(field.Elem(), values)
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(values[0])
	case reflect.Slice:
		if field.Type().Elem().Kind() == reflect.Uint8 {
			field.SetBytes([]byte(values[0]))
			return nil
		}
		if field.Type().Elem().Kind() != reflect.String {
			return InternalError("unsupported webhook header field type")
		}
		field.Set(reflect.ValueOf(values))
	default:
		return InternalError("unsupported webhook header field type")
	}

	return nil
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) route(path string) string {
	base := strings.TrimSuffix(i.handlerOpts.basePath, "/")
	if base == "/" {
		base = ""
	}
	id := strings.Trim(i.handlerOpts.integrationID, "/")

	prefix := ""
	switch {
	case base == "" && id == "":
		prefix = ""
	case base == "" && id != "":
		prefix = "/" + id
	case base != "" && id == "":
		if !strings.HasPrefix(base, "/") {
			base = "/" + base
		}
		prefix = base
	default:
		if !strings.HasPrefix(base, "/") {
			base = "/" + base
		}
		prefix = base + "/" + id
	}

	if prefix == "" {
		return path
	}
	if path == "/" {
		return prefix + "/"
	}
	if strings.HasPrefix(path, "/") {
		return prefix + path
	}
	return prefix + "/" + path
}

func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) logError(message string, err error) {
	if err == nil {
		return
	}
	if i.handlerOpts.integrationID != "" {
		slog.Error(message, "error", err, "integration_id", i.handlerOpts.integrationID)
		return
	}
	slog.Error(message, "error", err)
}
