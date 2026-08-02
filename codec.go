package fibsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// DecodeJSON decodes JSON from raw bytes into v using the same rules as the
// framework's internal request decoder: UseNumber, optional strict mode
// (disallow unknown fields), and single-value enforcement. Max-body size is
// not re-applied because it is already enforced when the bytes were read from
// the request. This is intended for use inside WebhookPreProcess implementations
// where the raw body is available for signature verification and typed decoding
// is also required.
func DecodeJSON(data []byte, v any, strict bool) *IntegrationError {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if strict {
		dec.DisallowUnknownFields()
	}

	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return BadRequest("empty request body")
		}

		var synErr *json.SyntaxError
		if errors.As(err, &synErr) {
			return BadRequest("malformed json body")
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return BadRequest("malformed json body")
		}

		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			if typeErr.Field != "" {
				return BadRequest("invalid value for field " + typeErr.Field)
			}
			return BadRequest("invalid json value type")
		}

		var inv *json.InvalidUnmarshalError
		if errors.As(err, &inv) {
			return InternalError("server error decoding json")
		}

		return BadRequest("invalid json body")
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return BadRequest("only one json value allowed")
	}

	return nil
}

func readJSON(w http.ResponseWriter, r *http.Request, v any, maxBytes int64, strict bool) *IntegrationError {
	if r.Body == nil {
		return BadRequest("missing request body")
	}
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	}
	defer r.Body.Close()

	dec := json.NewDecoder(r.Body)
	dec.UseNumber() // optional: preserves numbers when decoding into interface/map
	if strict {
		dec.DisallowUnknownFields()
	}

	if err := dec.Decode(v); err != nil {
		// empty body
		if errors.Is(err, io.EOF) {
			return BadRequest("empty request body")
		}

		// body too large (from MaxBytesReader)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return BadRequest("request body too large")
		}

		// malformed JSON
		var synErr *json.SyntaxError
		if errors.As(err, &synErr) {
			return BadRequest("malformed json body")
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return BadRequest("malformed json body")
		}

		// wrong JSON type for a field
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			if typeErr.Field != "" {
				return BadRequest("invalid value for field " + typeErr.Field)
			}
			return BadRequest("invalid json value type")
		}

		// programmer error: v must be a non-nil pointer
		var inv *json.InvalidUnmarshalError
		if errors.As(err, &inv) {
			// choose how your project represents internal errors
			return InternalError("server error decoding json")
		}

		return BadRequest("invalid json body")
	}

	// Disallow multiple JSON values
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return BadRequest("only one json value allowed")
	}

	return nil
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	dat, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Error marshalling json", "StatusCode", 500)
		w.WriteHeader(500)
		return
	}
	w.WriteHeader(code)
	w.Write(dat)
}

func respondWithError(w http.ResponseWriter, err *IntegrationError) {
	status := err.Status

	if err.Status >= 500 {
		slog.Error(err.Error(), "StatusCode", err.Status)
	}

	if err.Status == 0 {
		status = http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", "application/json")
	respondWithJSON(w, status, err)
}
