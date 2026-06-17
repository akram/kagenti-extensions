package tokenexchange

import "strings"

// keycloakProvider derives endpoints from Keycloak's URL conventions.
//
// Config example:
//
//	provider: keycloak
//	provider_url: https://keycloak.example.com
//	provider_realm: my-realm
type keycloakProvider struct{}

func (keycloakProvider) Name() string { return "keycloak" }

func (keycloakProvider) TokenEndpoint(providerURL, providerRealm string) string {
	base := strings.TrimRight(providerURL, "/")
	if base == "" || providerRealm == "" {
		return ""
	}
	return base + "/realms/" + providerRealm + "/protocol/openid-connect/token"
}

func (keycloakProvider) JWKSEndpoint(providerURL, providerRealm string) string {
	base := strings.TrimRight(providerURL, "/")
	if base == "" || providerRealm == "" {
		return ""
	}
	return base + "/realms/" + providerRealm + "/protocol/openid-connect/certs"
}

func init() { RegisterProvider(keycloakProvider{}) }
