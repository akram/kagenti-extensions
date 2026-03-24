/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package registrar

import (
	"testing"
)

func TestWorkloadOAuthSecretName_Deterministic(t *testing.T) {
	a := WorkloadOAuthSecretName("ns1", "wl", "spiffe://td/ns/ns1/sa/wl")
	b := WorkloadOAuthSecretName("ns1", "wl", "spiffe://td/ns/ns1/sa/wl")
	if a != b {
		t.Fatalf("expected stable name, got %q vs %q", a, b)
	}
	if len(a) > 63 {
		t.Fatalf("secret name too long: %d", len(a))
	}
	c := WorkloadOAuthSecretName("ns2", "wl", "spiffe://td/ns/ns1/sa/wl")
	if a == c {
		t.Fatal("expected different names for different namespaces")
	}
}
