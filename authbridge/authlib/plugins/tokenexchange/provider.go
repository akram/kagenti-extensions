package tokenexchange

import (
	"fmt"
	"sync"
)

// IdPProvider defines the contract for an Identity Provider backend.
// Each IdP (Keycloak, Entra ID, Okta, etc.) implements this interface
// to provide endpoint derivation from its configuration conventions.
//
// Adding a new IdP:
//   1. Create a new file (e.g. provider_okta.go)
//   2. Implement IdPProvider
//   3. Call RegisterProvider() in an init() function
//
// The init() auto-registration pattern means any provider file that
// is compiled into the binary is automatically available — no central
// list to maintain.
type IdPProvider interface {
	// Name returns the provider identifier used in config
	// (e.g. "keycloak", "entra-id", "okta").
	Name() string

	// TokenEndpoint derives the OAuth token endpoint URL from the
	// provider base URL and realm/tenant. Returns "" if the inputs
	// are insufficient (caller must supply explicit token_url).
	TokenEndpoint(providerURL, providerRealm string) string

	// JWKSEndpoint derives the JWKS endpoint URL from the provider
	// base URL and realm/tenant. Returns "" if the inputs are
	// insufficient (caller must supply explicit jwks_url).
	JWKSEndpoint(providerURL, providerRealm string) string
}

var (
	providersMu sync.RWMutex
	providers   = map[string]IdPProvider{}
)

// RegisterProvider registers an IdP provider. Called from init() in
// each provider's file. Panics on duplicate names.
func RegisterProvider(p IdPProvider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	name := p.Name()
	if _, exists := providers[name]; exists {
		panic(fmt.Sprintf("token-exchange: duplicate IdP provider registration: %q", name))
	}
	providers[name] = p
}

// LookupProvider returns the registered provider for the given name,
// or nil if not found.
func LookupProvider(name string) IdPProvider {
	providersMu.RLock()
	defer providersMu.RUnlock()
	return providers[name]
}
