/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package injector

import (
	"fmt"
	"strings"
)

// ParseBoolDefault parses a string as bool; empty string returns def.
func ParseBoolDefault(s string, def bool) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return def
	}
	switch s {
	case "true", "1", "yes", "y":
		return true
	case "false", "0", "no", "n":
		return false
	default:
		return def
	}
}

// ComputeKeycloakClientID returns the Keycloak clientId using the same rules as
// AuthBridge/client-registration: SPIFFE JWT sub when SPIRE is enabled, else namespace/workload.
func ComputeKeycloakClientID(spiffeTrustDomain, namespace, workloadName, serviceAccountName string, spireEnabled bool) string {
	if spireEnabled {
		td := spiffeTrustDomain
		if td == "" {
			td = "cluster.local"
		}
		sa := serviceAccountName
		if sa == "" {
			sa = "default"
		}
		return fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", td, namespace, sa)
	}
	return namespace + "/" + workloadName
}
