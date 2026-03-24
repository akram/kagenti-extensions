/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package registrar

import "errors"

// ErrConflict indicates the Keycloak entity already exists (HTTP 409).
var ErrConflict = errors.New("keycloak conflict")

// RegisterInput mirrors the inputs used by AuthBridge/client-registration/client_registration.py.
type RegisterInput struct {
	KeycloakURL string
	Realm       string

	AdminUsername string
	AdminPassword string

	// ClientName is namespace/workload (CLIENT_NAME).
	ClientName string
	// ClientID is the Keycloak clientId (SPIFFE ID or ClientName when SPIRE is off).
	ClientID string

	TokenExchangeEnabled bool
	AudienceScopeEnabled bool
	PlatformClientIDs    string
	ClientAuthType       string // "client-secret" or "federated-jwt"
	SpiffeIdpAlias       string
}

// Result holds OAuth client material stored in the workload Secret.
type Result struct {
	ClientID     string
	ClientSecret string
}
