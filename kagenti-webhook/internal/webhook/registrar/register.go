/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package registrar

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Register performs idempotent Keycloak client registration and audience scope
// setup equivalent to AuthBridge/client-registration/client_registration.py.
func Register(ctx context.Context, in RegisterInput) (*Result, error) {
	if in.KeycloakURL == "" || in.Realm == "" {
		return nil, fmt.Errorf("KEYCLOAK_URL and KEYCLOAK_REALM are required")
	}
	if in.AdminUsername == "" || in.AdminPassword == "" {
		return nil, fmt.Errorf("registrar Keycloak admin credentials are empty")
	}
	if in.ClientID == "" || in.ClientName == "" {
		return nil, fmt.Errorf("client id and client name are required")
	}

	kc := newKeycloakREST(in.KeycloakURL, in.Realm)
	if err := kc.obtainToken(ctx, in.AdminUsername, in.AdminPassword); err != nil {
		return nil, fmt.Errorf("keycloak auth: %w", err)
	}

	authType := in.ClientAuthType
	if authType == "" {
		authType = "client-secret"
	}

	attrs := map[string]any{
		"standard.token.exchange.enabled": strings.ToLower(fmt.Sprintf("%t", in.TokenExchangeEnabled)),
	}
	payload := map[string]any{
		"name":                      in.ClientName,
		"clientId":                  in.ClientID,
		"standardFlowEnabled":       true,
		"directAccessGrantsEnabled": true,
		"serviceAccountsEnabled":    true,
		"fullScopeAllowed":          false,
		"publicClient":              false,
		"attributes":                attrs,
	}

	if authType == "federated-jwt" {
		payload["clientAuthenticatorType"] = "federated-jwt"
		idp := in.SpiffeIdpAlias
		if idp == "" {
			idp = "spire-spiffe"
		}
		attrs["jwt.credential.issuer"] = idp
		attrs["jwt.credential.sub"] = in.ClientID
	} else {
		payload["clientAuthenticatorType"] = "client-secret"
	}

	internalID, err := kc.findClientUUID(ctx, in.ClientID)
	if err != nil {
		return nil, err
	}
	if internalID == "" {
		newID, cerr := kc.createClient(ctx, payload)
		switch {
		case errors.Is(cerr, ErrConflict):
			internalID, err = kc.findClientUUIDAfterConflict(ctx, in.ClientID)
			if err != nil {
				return nil, err
			}
		case cerr != nil:
			return nil, cerr
		default:
			internalID = newID
		}
	}
	if internalID == "" {
		return nil, fmt.Errorf("could not resolve internal client id for %q", in.ClientID)
	}

	secret, err := kc.getClientSecret(ctx, internalID)
	if err != nil {
		return nil, fmt.Errorf("get client secret: %w", err)
	}

	if in.AudienceScopeEnabled {
		scopeName := "agent-" + strings.ReplaceAll(in.ClientName, "/", "-") + "-aud"
		if err := ensureAudienceScope(ctx, kc, scopeName, in.ClientID, in.PlatformClientIDs); err != nil {
			return nil, err
		}
	}

	return &Result{
		ClientID:     in.ClientID,
		ClientSecret: secret,
	}, nil
}

func (k *keycloakREST) findClientUUIDAfterConflict(ctx context.Context, clientID string) (string, error) {
	all, err := k.listAllClients(ctx)
	if err != nil {
		return "", err
	}
	for i := range all {
		if all[i].ClientID == clientID {
			return all[i].ID, nil
		}
	}
	return "", fmt.Errorf("client %q not found after conflict", clientID)
}

func ensureAudienceScope(ctx context.Context, kc *keycloakREST, scopeName, audience, platformIDs string) error {
	scopes, err := kc.listClientScopes(ctx)
	if err != nil {
		return err
	}
	var scopeID string
	for i := range scopes {
		if scopes[i].Name == scopeName {
			scopeID = scopes[i].ID
			break
		}
	}
	if scopeID == "" {
		scopeID, err = kc.createClientScope(ctx, scopeName)
		if err != nil {
			return fmt.Errorf("audience scope: %w", err)
		}
	}
	// Mapper is idempotent enough if Keycloak returns conflict on duplicate mapper name.
	_ = kc.addAudienceMapper(ctx, scopeID, scopeName, audience)

	if err := kc.addRealmDefaultDefaultClientScope(ctx, scopeID); err != nil {
		// Best-effort like Python (realm may already have scope).
		_ = err
	}

	for _, raw := range strings.Split(platformIDs, ",") {
		pc := strings.TrimSpace(raw)
		if pc == "" {
			continue
		}
		pid, err := kc.findClientUUID(ctx, pc)
		if err != nil {
			return err
		}
		if pid == "" {
			continue
		}
		_ = kc.addClientDefaultClientScope(ctx, pid, scopeID)
	}
	return nil
}
