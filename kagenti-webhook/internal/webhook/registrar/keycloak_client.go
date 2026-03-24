/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package registrar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout = 45 * time.Second
)

// keycloakREST wraps Keycloak Admin REST calls used for client registration.
type keycloakREST struct {
	baseURL    string
	realm      string
	httpClient *http.Client
	token      string
}

func newKeycloakREST(baseURL, realm string) *keycloakREST {
	b := strings.TrimRight(baseURL, "/")
	return &keycloakREST{
		baseURL: b,
		realm:   realm,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

func (k *keycloakREST) obtainToken(ctx context.Context, adminUser, adminPass string) error {
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", "admin-cli")
	form.Set("username", adminUser)
	form.Set("password", adminPass)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		k.baseURL+"/realms/master/protocol/openid-connect/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("keycloak token: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("empty access_token from Keycloak")
	}
	k.token = tr.AccessToken
	return nil
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func (k *keycloakREST) authHeader() string {
	return "Bearer " + k.token
}

// --- Clients ---

type kcClientRep struct {
	ID       string `json:"id"`
	ClientID string `json:"clientId"`
}

func (k *keycloakREST) findClientUUID(ctx context.Context, clientID string) (string, error) {
	u := fmt.Sprintf("%s/admin/realms/%s/clients", k.baseURL, url.PathEscape(k.realm))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("clientId", clientID)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", k.authHeader())

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list clients: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	var list []kcClientRep
	if err := json.Unmarshal(body, &list); err != nil {
		return "", err
	}
	for i := range list {
		if list[i].ClientID == clientID {
			return list[i].ID, nil
		}
	}
	return "", nil
}

func (k *keycloakREST) listAllClients(ctx context.Context) ([]kcClientRep, error) {
	u := fmt.Sprintf("%s/admin/realms/%s/clients", k.baseURL, url.PathEscape(k.realm))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", k.authHeader())

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list all clients: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	var list []kcClientRep
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (k *keycloakREST) createClient(ctx context.Context, payload map[string]any) (string, error) {
	u := fmt.Sprintf("%s/admin/realms/%s/clients", k.baseURL, url.PathEscape(k.realm))
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", k.authHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return "", ErrConflict
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create client: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	// Location header may contain UUID
	loc := resp.Header.Get("Location")
	if loc != "" {
		parts := strings.Split(strings.TrimRight(loc, "/"), "/")
		if len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
	}
	return "", fmt.Errorf("create client: missing Location header")
}

var errConflict = fmt.Errorf("conflict")

func (k *keycloakREST) getClientSecret(ctx context.Context, internalUUID string) (string, error) {
	u := fmt.Sprintf("%s/admin/realms/%s/clients/%s/client-secret", k.baseURL, url.PathEscape(k.realm), url.PathEscape(internalUUID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", k.authHeader())

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get client secret: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// --- Client scopes ---

type kcScopeRep struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (k *keycloakREST) listClientScopes(ctx context.Context) ([]kcScopeRep, error) {
	u := fmt.Sprintf("%s/admin/realms/%s/client-scopes", k.baseURL, url.PathEscape(k.realm))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", k.authHeader())

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list client scopes: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	var list []kcScopeRep
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (k *keycloakREST) createClientScope(ctx context.Context, name string) (string, error) {
	payload := map[string]any{
		"name":     name,
		"protocol": "openid-connect",
		"attributes": map[string]string{
			"include.in.token.scope":    "true",
			"display.on.consent.screen": "true",
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("%s/admin/realms/%s/client-scopes", k.baseURL, url.PathEscape(k.realm))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", k.authHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create client scope: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	loc := resp.Header.Get("Location")
	if loc != "" {
		parts := strings.Split(strings.TrimRight(loc, "/"), "/")
		if len(parts) > 0 {
			return parts[len(parts)-1], nil
		}
	}
	return "", fmt.Errorf("create client scope: missing Location")
}

func (k *keycloakREST) addAudienceMapper(ctx context.Context, scopeID, scopeName, audience string) error {
	mapper := map[string]any{
		"name":            scopeName,
		"protocol":        "openid-connect",
		"protocolMapper":  "oidc-audience-mapper",
		"consentRequired": false,
		"config": map[string]string{
			"included.custom.audience": audience,
			"id.token.claim":           "false",
			"access.token.claim":       "true",
			"userinfo.token.claim":     "false",
		},
	}
	b, err := json.Marshal(mapper)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/admin/realms/%s/client-scopes/%s/protocol-mappers/models",
		k.baseURL, url.PathEscape(k.realm), url.PathEscape(scopeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", k.authHeader())
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add audience mapper: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	return nil
}

func (k *keycloakREST) addRealmDefaultDefaultClientScope(ctx context.Context, scopeID string) error {
	u := fmt.Sprintf("%s/admin/realms/%s/default-default-client-scopes/%s",
		k.baseURL, url.PathEscape(k.realm), url.PathEscape(scopeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", k.authHeader())

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add realm default scope: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	return nil
}

func (k *keycloakREST) addClientDefaultClientScope(ctx context.Context, platformInternalID, scopeID string) error {
	u := fmt.Sprintf("%s/admin/realms/%s/clients/%s/default-client-scopes/%s",
		k.baseURL, url.PathEscape(k.realm), url.PathEscape(platformInternalID), url.PathEscape(scopeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", k.authHeader())

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("add client default scope: status %d: %s", resp.StatusCode, truncate(body, 512))
	}
	return nil
}
