package fibery

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type EnvProvider interface {
	String(key string) (string, bool)
	RequiredString(key string) (string, error)
	Int(key string) (int, bool)
	RequiredInt(key string) (int, error)
	Bool(key string) (bool, bool)
	RequiredBool(key string) (bool, error)
	Duration(key string) (time.Duration, bool)
	RequiredDuration(key string) (time.Duration, error)
}

type OSEnv struct{}

func (OSEnv) String(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	return v, ok
}

func (e OSEnv) RequiredString(key string) (string, error) {
	v, ok := e.String(key)
	if !ok || v == "" {
		return "", fmt.Errorf("missing required env var %s", key)
	}
	return v, nil
}

func (OSEnv) Int(key string) (int, bool) {
	if v, ok := os.LookupEnv(key); ok {
		i, err := strconv.Atoi(v)
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func (e OSEnv) RequiredInt(key string) (int, error) {
	i, ok := e.Int(key)
	if !ok {
		return 0, fmt.Errorf("missing or invalid int env var %s", key)
	}
	return i, nil
}

func (OSEnv) Bool(key string) (bool, bool) {
	if v, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b, true
		}
	}
	return false, false
}

func (e OSEnv) RequiredBool(key string) (bool, error) {
	b, ok := e.Bool(key)
	if !ok {
		return false, fmt.Errorf("missing or invalid int env var %s", key)
	}
	return b, nil
}

func (OSEnv) Duration(key string) (time.Duration, bool) {
	if v, ok := os.LookupEnv(key); ok {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d, true
		}
	}
	return 0, false
}

func (e OSEnv) RequiredDuration(key string) (time.Duration, error) {
	d, ok := e.Duration(key)
	if !ok {
		return 0, fmt.Errorf("missing or invalid int env var %s", key)
	}
	return d, nil
}

var defaultEnvProvider EnvProvider = OSEnv{}

// SetDefaultEnvProvider configures the package-level environment provider.
// Passing nil resets to the default OSEnv provider.
func SetDefaultEnvProvider(provider EnvProvider) {
	if provider == nil {
		defaultEnvProvider = OSEnv{}
		return
	}
	defaultEnvProvider = provider
}

func packageEnvProvider() EnvProvider {
	if defaultEnvProvider == nil {
		return OSEnv{}
	}
	return defaultEnvProvider
}
