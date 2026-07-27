package fibery

import (
	"errors"
	"testing"
	"time"
)

func TestOSEnvString(t *testing.T) {
	const key = "FIBERY_TEST_STRING"
	const value = "hello"

	t.Setenv(key, value)
	env := OSEnv{}

	got, ok := env.String(key)
	if !ok {
		t.Fatalf("expected value to be present")
	}
	if got != value {
		t.Fatalf("expected %q, got %q", value, got)
	}

	missing, ok := env.String("FIBERY_TEST_MISSING")
	if ok {
		t.Fatalf("expected missing key to be false, got true with %q", missing)
	}
}

func TestOSEnvRequiredString(t *testing.T) {
	const key = "FIBERY_TEST_REQUIRED_STRING"
	const value = "required"

	t.Setenv(key, value)
	env := OSEnv{}

	got, err := env.RequiredString(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != value {
		t.Fatalf("expected %q, got %q", value, got)
	}

	if _, err := env.RequiredString("FIBERY_TEST_REQUIRED_MISSING"); err == nil {
		t.Fatalf("expected error for missing key")
	}

	t.Setenv("FIBERY_TEST_REQUIRED_EMPTY", "")
	if _, err := env.RequiredString("FIBERY_TEST_REQUIRED_EMPTY"); err == nil {
		t.Fatalf("expected error for empty value")
	}
}

func TestOSEnvInt(t *testing.T) {
	const key = "FIBERY_TEST_INT"

	t.Setenv(key, "123")
	env := OSEnv{}

	got, ok := env.Int(key)
	if !ok {
		t.Fatalf("expected int to be present")
	}
	if got != 123 {
		t.Fatalf("expected 123, got %d", got)
	}

	t.Setenv(key, "notint")
	got, ok = env.Int(key)
	if ok {
		t.Fatalf("expected invalid int to be false, got %d", got)
	}

	missing, ok := env.Int("FIBERY_TEST_INT_MISSING")
	if ok {
		t.Fatalf("expected missing int to be false, got %d", missing)
	}
}

func TestOSEnvRequiredInt(t *testing.T) {
	const key = "FIBERY_TEST_REQUIRED_INT"

	t.Setenv(key, "42")
	env := OSEnv{}

	got, err := env.RequiredInt(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}

	t.Setenv(key, "bad")
	if _, err := env.RequiredInt(key); err == nil {
		t.Fatalf("expected error for invalid int")
	}

	if _, err := env.RequiredInt("FIBERY_TEST_REQUIRED_INT_MISSING"); err == nil {
		t.Fatalf("expected error for missing int")
	}
}

func TestOSEnvBool(t *testing.T) {
	const key = "FIBERY_TEST_BOOL"
	env := OSEnv{}

	t.Setenv(key, "true")
	got, ok := env.Bool(key)
	if !ok || got != true {
		t.Fatalf("expected true, got %v (ok=%v)", got, ok)
	}

	t.Setenv(key, "false")
	got, ok = env.Bool(key)
	if !ok || got != false {
		t.Fatalf("expected false, got %v (ok=%v)", got, ok)
	}

	t.Setenv(key, "notbool")
	got, ok = env.Bool(key)
	if ok {
		t.Fatalf("expected invalid bool to be false, got %v", got)
	}

	missing, ok := env.Bool("FIBERY_TEST_BOOL_MISSING")
	if ok {
		t.Fatalf("expected missing bool to be false, got %v", missing)
	}
}

func TestOSEnvDuration(t *testing.T) {
	const key = "FIBERY_TEST_DURATION"
	env := OSEnv{}

	t.Setenv(key, "5s")
	got, ok := env.Duration(key)
	if !ok || got != 5*time.Second {
		t.Fatalf("expected 5s, got %v (ok=%v)", got, ok)
	}

	t.Setenv(key, "notdur")
	got, ok = env.Duration(key)
	if ok {
		t.Fatalf("expected invalid duration to be false, got %v", got)
	}

	missing, ok := env.Duration("FIBERY_TEST_DURATION_MISSING")
	if ok {
		t.Fatalf("expected missing duration to be false, got %v", missing)
	}
}

type customEnv struct{}

func (customEnv) String(key string) (string, bool) { return "custom", true }
func (customEnv) RequiredString(key string) (string, error) {
	return "custom", nil
}
func (customEnv) Int(key string) (int, bool) { return 7, true }
func (customEnv) RequiredInt(key string) (int, error) {
	return 7, nil
}
func (customEnv) Bool(key string) (bool, bool)          { return true, true }
func (customEnv) RequiredBool(key string) (bool, error) { return true, nil }

func (customEnv) Duration(key string) (time.Duration, bool) {
	return time.Second, true
}
func (customEnv) RequiredDuration(key string) (time.Duration, error) {
	return time.Second, nil
}

func TestSetDefaultEnvProvider(t *testing.T) {
	SetDefaultEnvProvider(customEnv{})
	provider := packageEnvProvider()
	if provider == nil {
		t.Fatalf("expected provider to be set")
	}

	value, ok := provider.String("any")
	if !ok || value != "custom" {
		t.Fatalf("expected custom provider, got %q", value)
	}

	SetDefaultEnvProvider(nil)
	provider = packageEnvProvider()
	if provider == nil {
		t.Fatalf("expected fallback provider")
	}

	if _, ok := provider.(OSEnv); !ok {
		t.Fatalf("expected fallback to OSEnv")
	}
}

func TestSetDefaultEnvProviderNilDoesNotPanic(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("unexpected panic: %v", recovered)
		}
	}()

	SetDefaultEnvProvider(nil)
}

func TestRequiredStringErrorMessage(t *testing.T) {
	env := OSEnv{}
	_, err := env.RequiredString("FIBERY_TEST_REQUIRED_STRING_MISSING")
	if err == nil {
		t.Fatalf("expected error")
	}
	if errors.Is(err, nil) {
		t.Fatalf("expected non-nil error")
	}
}
