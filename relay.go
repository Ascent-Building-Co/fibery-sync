package fibery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// relayTimeout bounds a single outbound relay forwarding attempt.
const relayTimeout = 15 * time.Second

// hopByHopHeaders are connection-scoped headers that must not be forwarded when
// relaying a request or mirroring a response (RFC 7230 §6.1).
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func isHopByHop(name string) bool {
	_, ok := hopByHopHeaders[http.CanonicalHeaderKey(name)]
	return ok
}

// handleWebhookRelay forwards an inbound webhook to the target resolved by the
// integration and mirrors the upstream response. The framework owns all the
// mechanics; the integration only decides the destination.
func (i *integrationHandler[AccountFields, Page, WebhookParams, WebhookReply, WebhookHeaders, WebhookNotification, ResourceParams, Oauth1Extra, Oauth2Extra]) handleWebhookRelay(relay IntegrationWithWebhookRelay) http.HandlerFunc {
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

		target, relayErr := relay.WebhookRelay(r.Context(), rawBody, r.Header)
		if relayErr != nil {
			respondWithError(w, relayErr)
			return
		}
		if target.URL == "" {
			respondWithError(w, InternalError("relay: empty target URL"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), relayTimeout)
		defer cancel()

		out, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(rawBody))
		if err != nil {
			respondWithError(w, InternalError("relay: unable to build outbound request"))
			return
		}

		// Forward inbound headers minus hop-by-hop, then apply target overrides.
		for name, values := range r.Header {
			if isHopByHop(name) {
				continue
			}
			out.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
		if target.ContentType != "" {
			out.Header.Set("Content-Type", target.ContentType)
		}
		for name, values := range target.Header {
			out.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}

		resp, err := i.serverOpts.httpClient.Do(out)
		if err != nil {
			respondWithError(w, BadGateway("relay: outbound request failed"))
			return
		}
		defer resp.Body.Close()

		for name, values := range resp.Header {
			if isHopByHop(name) {
				continue
			}
			w.Header()[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
		w.WriteHeader(resp.StatusCode)
		if _, copyErr := io.Copy(w, resp.Body); copyErr != nil {
			i.logError("relay response copy failed", copyErr)
		}
	}
}
