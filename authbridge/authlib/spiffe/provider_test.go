package spiffe

import (
	"testing"
)

// TestProvider_NoJWTAudience verifies JWTSource() returns nil when no
// JWT audience is configured. Uses direct struct construction (no
// NewProvider) to avoid requiring a real SPIFFE Workload API socket;
// see option C in the task plan for rationale.
func TestProvider_NoJWTAudience(t *testing.T) {
	p := &Provider{
		cfg:  ProviderConfig{},
		x509: &workloadX509{},
		// jwt is nil, jwtSDK is nil
	}
	if got := p.JWTSource(); got != nil {
		t.Errorf("Provider.JWTSource() = %v, want nil when JWTAudience is empty", got)
	}
	if got := p.X509Source(); got == nil {
		t.Errorf("Provider.X509Source() = nil, want non-nil")
	}
}

// TestProvider_JWTSource_ReturnsNil_TypedNilGuard verifies that when the
// JWT field is unset the JWTSource() method returns an untyped nil
// interface — not a typed-nil that compares != nil. This is the edge
// case the implementation has to guard against because Go's interface
// comparison treats `(JWTSource)(nil)` (typed nil) as != nil.
func TestProvider_JWTSource_ReturnsNil_TypedNilGuard(t *testing.T) {
	p := &Provider{} // jwt is the zero value: a nil *workloadJWT
	got := p.JWTSource()
	if got != nil {
		t.Errorf("Provider.JWTSource() with nil jwt should == nil (untyped); got typed-nil that != nil")
	}
}

// TestProvider_JWTSource_NonNil verifies JWTSource returns the configured
// adapter when one is set.
func TestProvider_JWTSource_NonNil(t *testing.T) {
	p := &Provider{
		jwt: &workloadJWT{audience: "test-audience"},
	}
	got := p.JWTSource()
	if got == nil {
		t.Fatalf("Provider.JWTSource() = nil, want non-nil")
	}
}

// TestProvider_Close_Idempotent verifies Close() can be called multiple
// times without panicking. Uses direct struct construction with all SDK
// fields nil so Close becomes a no-op (no SDK Close calls, no cancel
// func to invoke). The aim is to verify the method's nil-guards rather
// than exercise SDK shutdown.
func TestProvider_Close_Idempotent(t *testing.T) {
	p := &Provider{} // all fields zero — every nil guard exercised

	if err := p.Close(); err != nil {
		t.Errorf("first Close() = %v, want nil", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("second Close() = %v, want nil (idempotent)", err)
	}
}

// TestProvider_Close_RunsCancel verifies Close() invokes the mirror
// cancel function when one is set, even when the SDK fields are nil.
func TestProvider_Close_RunsCancel(t *testing.T) {
	cancelCalled := false
	p := &Provider{
		cancel: func() { cancelCalled = true },
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if !cancelCalled {
		t.Error("Close() did not invoke the mirror cancel function")
	}
}

// Note: Provider.Close calls .Close() directly on the typed SDK fields
// (x509SDK *workloadapi.X509Source, jwtSDK *workloadapi.JWTSource), so
// the SDK Close path is exercised only by the integration test. The
// cancel-and-no-SDK branches above cover all the nil-guards reachable
// from a unit test.
