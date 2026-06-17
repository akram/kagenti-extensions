# IdP-Agnostic Token Exchange Plugin Contract

> **Status:** Draft — Phase 1 of RHAIENG-5681 / kagenti-extensions#481
>
> This document defines the contract that an Identity Provider (IdP)
> plugin must satisfy for the AuthBridge token exchange pipeline.
> Contributors implementing Entra ID, Okta, or other IdP support should
> use this as their specification.

## Overview

The token exchange plugin (`token-exchange`) implements RFC 8693 token
exchange for outbound requests. The core pipeline is IdP-agnostic:

```
Request → Route resolver → Token cache → RFC 8693 exchange → Inject token
```

IdP-specific behavior is confined to two extension points:

1. **Token endpoint resolution** — how to derive the OAuth token endpoint URL
2. **Client authentication** — how the workload authenticates to the IdP

Everything else (route matching, caching, token injection, error
handling) is generic and shared.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                token-exchange plugin             │
│                                                  │
│  ┌──────────────┐  ┌─────────────────────────┐  │
│  │ Route        │  │ exchange.Client          │  │
│  │ Resolver     │  │ (RFC 8693, IdP-agnostic) │  │
│  │              │  │                          │  │
│  │ host → aud   │  │  ┌───────────────────┐   │  │
│  │ host → scope │  │  │ ClientAuth        │   │  │
│  │ host → url   │──│  │ (IdP-specific)    │   │  │
│  │              │  │  │                   │   │  │
│  └──────────────┘  │  │ • ClientSecretAuth│   │  │
│                    │  │ • JWTAssertionAuth│   │  │
│                    │  │ • CertificateAuth │   │  │
│                    │  │   (future)        │   │  │
│                    │  └───────────────────┘   │  │
│                    └─────────────────────────┘  │
└─────────────────────────────────────────────────┘
```

## Extension Point 1: Token Endpoint Resolution

### Current state (Keycloak-specific)

```go
// plugin.go:122-126
if c.TokenURL == "" && c.KeycloakURL != "" && c.KeycloakRealm != "" {
    base := strings.TrimRight(c.KeycloakURL, "/") + "/realms/" + c.KeycloakRealm
    c.TokenURL = base + "/protocol/openid-connect/token"
}
```

### Proposed contract

The plugin resolves the token endpoint URL via a **resolution chain**:

1. **Explicit `token_url`** — always wins (works for any IdP)
2. **Provider-specific derivation** — when `provider` is set and `token_url` is empty
3. **Per-route `token_url` override** — in `routes.yaml`, per-host

#### Configuration

```yaml
token-exchange:
  # Explicit URL (works for any IdP, recommended for production)
  token_url: "https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token"

  # OR: provider-assisted derivation (convenience for supported IdPs)
  provider: "keycloak"       # keycloak | entra-id | okta | generic
  provider_url: "https://keycloak.example.com"
  provider_realm: "my-realm" # keycloak-specific, ignored by other providers

  identity:
    type: "client-secret"    # client-secret | spiffe | certificate (future)
    client_id: "my-agent"
    client_secret: "..."
```

#### Provider URL derivation patterns

| Provider | `token_url` derivation | `jwks_url` derivation |
|----------|----------------------|---------------------|
| `keycloak` | `{provider_url}/realms/{provider_realm}/protocol/openid-connect/token` | `{provider_url}/realms/{provider_realm}/protocol/openid-connect/certs` |
| `entra-id` | `https://login.microsoftonline.com/{provider_realm}/oauth2/v2.0/token` | `https://login.microsoftonline.com/{provider_realm}/discovery/v2.0/keys` |
| `okta` | `{provider_url}/oauth2/v1/token` | `{provider_url}/oauth2/v1/keys` |
| `generic` | **must** supply explicit `token_url` | **must** supply explicit `jwks_url` |

`provider_realm` is overloaded per IdP:
- **Keycloak:** realm name (e.g., `kagenti`)
- **Entra ID:** tenant ID or domain (e.g., `contoso.onmicrosoft.com`)
- **Okta:** authorization server ID (optional, omit for org-level)

#### Backward compatibility

`keycloak_url` and `keycloak_realm` continue to work. When present
and `provider` is not set, the plugin infers `provider: "keycloak"`.
A deprecation warning is logged suggesting migration to `provider_url`
/ `provider_realm`.

```
WARN token-exchange: keycloak_url/keycloak_realm are deprecated;
     use provider=keycloak + provider_url + provider_realm instead
```

### Interface (Go)

No new Go interface is needed for URL derivation — it is a pure
function of (`provider`, `provider_url`, `provider_realm`) →
(`token_url`, `jwks_url`). Implemented as a switch in
`applyDefaults()`.

```go
// resolveEndpoints derives token_url and jwks_url from provider config.
// Returns ("", "") when explicit URLs should be required.
func resolveEndpoints(provider, providerURL, providerRealm string) (tokenURL, jwksURL string) {
    base := strings.TrimRight(providerURL, "/")
    switch provider {
    case "keycloak":
        realmBase := base + "/realms/" + providerRealm
        return realmBase + "/protocol/openid-connect/token",
               realmBase + "/protocol/openid-connect/certs"
    case "entra-id":
        tenant := providerRealm // tenant ID
        return "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/token",
               "https://login.microsoftonline.com/" + tenant + "/discovery/v2.0/keys"
    case "okta":
        if providerRealm != "" {
            return base + "/oauth2/" + providerRealm + "/v1/token",
                   base + "/oauth2/" + providerRealm + "/v1/keys"
        }
        return base + "/oauth2/v1/token",
               base + "/oauth2/v1/keys"
    case "generic", "":
        return "", "" // must supply explicit URLs
    default:
        return "", "" // unknown provider
    }
}
```

## Extension Point 2: Client Authentication

### Current state

The `exchange.ClientAuth` interface is already IdP-agnostic:

```go
// exchange/auth.go
type ClientAuth interface {
    Apply(req *http.Request) error
}
```

Two implementations exist:
- `ClientSecretAuth` — `client_id` + `client_secret` in request body
- `JWTAssertionAuth` — JWT client assertion (`client_assertion_type` + `client_assertion`)

### Proposed additions for IdP coverage

| IdP | Supported auth methods | Implementation |
|-----|----------------------|----------------|
| **Keycloak** | `client-secret` ✅, `spiffe` (JWT assertion) ✅ | Already implemented |
| **Entra ID** | `client-secret` ✅, `certificate` ❌ (new) | Needs `CertificateAuth` |
| **Okta** | `client-secret` ✅, `jwt-bearer` ❌ (new assertion type) | Needs configurable assertion type |

#### New identity type: `certificate` (future, for Entra ID)

```yaml
identity:
  type: "certificate"
  client_id: "app-client-id"
  certificate_file: "/certs/client.pem"
  private_key_file: "/certs/client.key"
```

Implements `ClientAuth` by constructing a self-signed JWT assertion
using the X.509 certificate thumbprint (`x5t` header claim), signed
with the private key. This is the standard Entra ID confidential
client authentication flow.

#### Configurable assertion type (for Okta)

The `spiffe` identity type hardcodes the assertion type URN:

```go
// Current (Keycloak-specific)
AssertionType: "urn:ietf:params:oauth:client-assertion-type:jwt-spiffe"
```

This should be configurable:

```yaml
identity:
  type: "spiffe"
  jwt_audience: "https://okta.example.com"
  # Override the default assertion type (default: jwt-spiffe)
  assertion_type: "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
```

| Assertion type | IdP support |
|---------------|-------------|
| `urn:ietf:params:oauth:client-assertion-type:jwt-spiffe` | Keycloak ✅, Okta ❌, Entra ID ❌ |
| `urn:ietf:params:oauth:client-assertion-type:jwt-bearer` | Keycloak ✅, Okta ✅, Entra ID ❌ |

## Extension Point 3: SchemaProvider (config introspection)

The plugin already implements `pipeline.SchemaProvider`:

```go
func (p *TokenExchange) ConfigSchema() []pipeline.FieldSchema {
    return pipeline.SchemaOf(tokenExchangeConfig{})
}
```

New fields (`provider`, `provider_url`, `provider_realm`,
`assertion_type`) are automatically surfaced via struct tags. No
framework changes needed.

## What does NOT change

The following are IdP-agnostic and require no modifications:

- **RFC 8693 token exchange parameters** (`grant_type`, `subject_token`,
  `requested_token_type`, `audience`, `scope`) — standard across all IdPs
- **Route resolver** — host-to-audience matching, per-route `token_url` override
- **Token cache** — SHA-256 keyed, IdP-agnostic
- **Plugin registry** — `plugins.RegisterPlugin("token-exchange", ...)` unchanged
- **SPIFFE provider injection** — `SetSPIFFEProvider` / `plugins.BuildWithSPIFFE`
- **Credential file handling** — `/shared/client-id.txt`, `/shared/client-secret.txt`
- **Error handling** — standard OAuth error response parsing (RFC 6749)

## Implementation phases

### Phase 1: Config generalization (this PR)
- Add `provider`, `provider_url`, `provider_realm` fields
- Deprecate `keycloak_url`, `keycloak_realm` (backward compat)
- Implement `resolveEndpoints()` for keycloak, entra-id, okta, generic
- Make `assertion_type` configurable on spiffe identity
- Update Capabilities description
- Document the contract (this file)

### Phase 2: Entra ID plugin (separate PR)
- Implement `CertificateAuth` (`exchange.ClientAuth`)
- Add `identity.type: "certificate"` support
- Test with Entra ID token endpoint

### Phase 3: Okta plugin (separate PR)
- Test `jwt-bearer` assertion type with Okta
- Add Okta-specific integration test
- Document Okta-specific configuration

## Testing strategy

Each IdP integration should include:
1. **Unit tests** — mock token endpoint, verify correct parameters
2. **Config validation tests** — ensure required fields are enforced
3. **URL derivation tests** — verify per-provider endpoint patterns
4. **Integration tests** (optional) — against a real or emulated IdP

The existing `exchange/client_test.go` provides the pattern for mock
server tests.
