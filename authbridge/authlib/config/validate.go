package config

import (
	"encoding/json"
	"fmt"
)

// Validate checks the top-level runtime config: mode, listener combo,
// and that the pipeline composition is populated. Plugin-specific
// validation (issuer, token URL, identity type) lives inside each
// plugin's Configure and runs at pipeline build time.
//
// Empty pipelines are rejected. Under the per-plugin config shape,
// a valid runtime config always names at least one inbound plugin
// (jwt-validation) and one outbound plugin (token-exchange). Silently
// accepting empty pipelines caused the whole point of authbridge to
// disappear — inbound traffic passing without JWT validation, outbound
// passing without token exchange. Operators upgrading from the old
// top-level-block schema ("inbound:", "outbound:", etc.) whose YAML
// does not yet include a pipeline section fail loudly here rather
// than shipping an open proxy. See the schema migration note in
// cmd/authbridge/README.md.
func Validate(cfg *Config) error {
	switch cfg.Mode {
	case ModeEnvoySidecar, ModeWaypoint, ModeProxySidecar:
		// valid
	case "":
		return fmt.Errorf("mode is required (envoy-sidecar, waypoint, or proxy-sidecar)")
	default:
		return fmt.Errorf("unknown mode %q (valid: envoy-sidecar, waypoint, proxy-sidecar)", cfg.Mode)
	}
	if err := validateListeners(cfg); err != nil {
		return err
	}
	if err := validatePipeline(cfg); err != nil {
		return err
	}
	return validateCrossBlock(cfg)
}

// validateCrossBlock enforces invariants that span more than one
// top-level config block. These can't live inside a single plugin's
// Configure (which only sees its own subtree) or inside SPIFFEConfig.Validate
// (which doesn't know which plugins are configured).
//
// Currently the only invariant is: token-exchange identity.type=spiffe
// requires spiffe.jwt_audience. The audience is consumed by the
// framework SPIFFE Provider when fetching a JWT-SVID for the client
// assertion; without it the Provider has nothing to ask SPIRE for.
// Catching this at startup avoids a confusing runtime failure on the
// first outbound exchange.
func validateCrossBlock(cfg *Config) error {
	if !anyTokenExchangeUsesSPIFFE(cfg) {
		return nil
	}
	if cfg.SPIFFE == nil || cfg.SPIFFE.JWTAudience == "" {
		return fmt.Errorf("token-exchange identity.type=spiffe requires top-level spiffe.jwt_audience to be set")
	}
	return nil
}

// anyTokenExchangeUsesSPIFFE walks both pipeline stages looking for a
// token-exchange entry whose identity.type is "spiffe". Loose-decode
// against an inline struct so this validator stays in the config layer
// without importing the plugin package.
//
// NOTE: this walker does NOT filter on e.OnError == ErrorPolicyOff. An
// entry that is gated off via on_error still triggers the cross-block
// check that requires top-level spiffe.jwt_audience. Rationale:
// dead-config validation catches typos and forgotten flag-flips at
// startup. If an operator wants to leave a token-exchange entry in
// YAML with identity.type: spiffe but no audience, they must remove
// the entry rather than just disabling it. This is the strict-but-
// simple choice; loosen if it bites.
func anyTokenExchangeUsesSPIFFE(cfg *Config) bool {
	stages := [][]PluginEntry{
		cfg.Pipeline.Inbound.Plugins,
		cfg.Pipeline.Outbound.Plugins,
	}
	for _, stage := range stages {
		for _, e := range stage {
			if e.Name != "token-exchange" {
				continue
			}
			if len(e.Config) == 0 {
				continue
			}
			var probe struct {
				Identity struct {
					Type string `json:"type"`
				} `json:"identity"`
			}
			if err := json.Unmarshal(e.Config, &probe); err != nil {
				// Malformed config is the plugin's problem to report
				// at Configure time; cross-block validation just skips it.
				continue
			}
			if probe.Identity.Type == "spiffe" {
				return true
			}
		}
	}
	return false
}

func validatePipeline(cfg *Config) error {
	if len(cfg.Pipeline.Inbound.Plugins) == 0 {
		return fmt.Errorf("pipeline.inbound.plugins is empty; specify at least one plugin " +
			"(typically jwt-validation) — see cmd/authbridge/README.md. " +
			"If you see this after an upgrade, your config.yaml is using the old top-level shape " +
			"(inbound:, outbound:, identity:, bypass:, routes:) — move those settings under " +
			"pipeline.*.plugins[].config")
	}
	if len(cfg.Pipeline.Outbound.Plugins) == 0 {
		return fmt.Errorf("pipeline.outbound.plugins is empty; specify at least one plugin " +
			"(typically token-exchange) — see cmd/authbridge/README.md")
	}
	return nil
}

func validateListeners(cfg *Config) error {
	switch cfg.Mode {
	case ModeEnvoySidecar:
		if cfg.Listener.ReverseProxyAddr != "" {
			return fmt.Errorf("envoy-sidecar mode does not support reverse_proxy_addr (use proxy-sidecar mode)")
		}
		if cfg.Listener.ExtAuthzAddr != "" {
			return fmt.Errorf("envoy-sidecar mode does not support ext_authz_addr (use waypoint mode)")
		}
	case ModeWaypoint:
		if cfg.Listener.ExtProcAddr != "" {
			return fmt.Errorf("waypoint mode does not support ext_proc_addr (use envoy-sidecar mode)")
		}
		if cfg.Listener.ReverseProxyAddr != "" {
			return fmt.Errorf("waypoint mode does not support reverse_proxy_addr")
		}
	case ModeProxySidecar:
		if cfg.Listener.ExtProcAddr != "" {
			return fmt.Errorf("proxy-sidecar mode does not support ext_proc_addr (use envoy-sidecar mode)")
		}
		if cfg.Listener.ExtAuthzAddr != "" {
			return fmt.Errorf("proxy-sidecar mode does not support ext_authz_addr (use waypoint mode)")
		}
		if cfg.Listener.ReverseProxyBackend == "" {
			return fmt.Errorf("proxy-sidecar mode requires listener.reverse_proxy_backend")
		}
	}
	return nil
}
