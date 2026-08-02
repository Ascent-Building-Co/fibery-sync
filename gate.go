package fibsync

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"sync"
)

// accountGateFieldID is the reserved auth-field ID the framework injects into
// every declared authentication method when an account gate is enabled. It is
// an internal wire key; end users never see it. The value submitted under this
// key is extracted during Account decoding (see Account.UnmarshalJSON).
const accountGateFieldID = "_integration_gate"

// AccountGate configures a shared-secret gate applied to every account-bearing
// endpoint. When enabled, the framework injects a password field into all
// authentication methods returned by Config and rejects any account-bearing
// request whose submitted value does not satisfy the gate.
//
// Provide exactly one of Secret or Validate. Secret installs a constant-time
// check against a single static value. Validate supplies a custom check (for
// example a rotating SecretSet) and takes precedence over Secret when both are
// set.
//
// The gate does not protect endpoints that carry no account: Config, logo,
// OAuth, and webhook pre-process. Protect webhooks with signature verification
// in WebhookPreProcess instead.
type AccountGate struct {
	Secret      string
	Validate    func(presented string) bool
	Title       string
	Description string
}

// accountGate is the resolved, internal form of AccountGate. Both Secret and
// Validate normalize to a single validate func so enforcement is uniform.
type accountGate struct {
	enabled     bool
	validate    func(presented string) bool
	title       string
	description string
}

// WithAccountGate enables the account gate for an integration. It requires a
// non-empty Secret or a non-nil Validate; AddIntegration fails closed with an
// error otherwise.
func WithAccountGate(gate AccountGate) HandlerOption {
	return func(o *handlerOptions) {
		resolved := accountGate{
			enabled:     true,
			validate:    gate.Validate,
			title:       gate.Title,
			description: gate.Description,
		}
		if resolved.validate == nil && gate.Secret != "" {
			resolved.validate = staticValidator(gate.Secret)
		}
		o.gate = resolved
	}
}

// staticValidator returns a constant-time membership check for a single
// secret. It compares SHA-256 digests so the presented value's length is not
// revealed through timing.
func staticValidator(secret string) func(string) bool {
	want := sha256.Sum256([]byte(secret))
	return func(presented string) bool {
		got := sha256.Sum256([]byte(presented))
		return subtle.ConstantTimeCompare(got[:], want[:]) == 1
	}
}

// SecretSet is a constant-time membership check over a set of valid secrets,
// safe for concurrent use. It supports rotation: swap the active set with Set
// (for example on a SIGHUP-driven config reload) without restarting the
// server. Pass SecretSet.Valid as AccountGate.Validate.
//
// Comparisons use SHA-256 digests and never short-circuit, so neither the
// length of a candidate secret nor the position or number of matches leaks
// through timing. An empty set rejects everything (fails closed).
type SecretSet struct {
	mu      sync.RWMutex
	digests [][32]byte
}

// NewSecretSet builds a SecretSet from the given secrets. Empty strings are
// ignored.
func NewSecretSet(secrets ...string) *SecretSet {
	s := &SecretSet{}
	s.Set(secrets...)
	return s
}

// Set atomically replaces the active set of valid secrets. Empty strings are
// ignored. In-flight Valid calls are unaffected.
func (s *SecretSet) Set(secrets ...string) {
	digests := make([][32]byte, 0, len(secrets))
	for _, sec := range secrets {
		if sec != "" {
			digests = append(digests, sha256.Sum256([]byte(sec)))
		}
	}
	s.mu.Lock()
	s.digests = digests
	s.mu.Unlock()
}

// Valid reports whether presented matches any secret in the active set.
func (s *SecretSet) Valid(presented string) bool {
	sum := sha256.Sum256([]byte(presented))
	s.mu.RLock()
	digests := s.digests
	s.mu.RUnlock()
	var match int
	for _, d := range digests {
		match |= subtle.ConstantTimeCompare(sum[:], d[:])
	}
	return match == 1
}

// GenerateSecret returns a cryptographically random, URL-safe secret carrying
// nBytes of entropy. Values below 1 default to 32 bytes (256 bits). The result
// is unpadded base64url, so it is safe to copy-paste into the Fibery auth
// field.
func GenerateSecret(nBytes int) (string, error) {
	if nBytes <= 0 {
		nBytes = 32
	}
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
