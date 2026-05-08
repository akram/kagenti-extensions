package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kagenti/kagenti-extensions/authbridge/authlib/pipeline"
)

// TestShortPhase_Denied locks the abctl rendering string for the new
// denied phase — changing this silently would ripple into the events
// table and teatest snapshots.
func TestShortPhase_Denied(t *testing.T) {
	if got := shortPhase(pipeline.SessionDenied); got != "deny" {
		t.Errorf("shortPhase(SessionDenied) = %q, want deny", got)
	}
}

// TestAuthCell covers the column renderer for every shape an event's
// Auth extension can take: nil (common), inbound-only (jwt-validation
// decisions), outbound-only (token-exchange actions), last-wins for
// chained plugins.
func TestAuthCell(t *testing.T) {
	cases := []struct {
		name string
		ev   pipeline.SessionEvent
		want string
	}{
		{
			name: "no auth extension",
			ev:   pipeline.SessionEvent{},
			want: "—",
		},
		{
			name: "inbound allow",
			ev: pipeline.SessionEvent{Auth: &pipeline.AuthExtension{
				Inbound: []pipeline.InboundAuth{{Decision: "allow"}},
			}},
			want: "allow",
		},
		{
			name: "inbound deny",
			ev: pipeline.SessionEvent{Auth: &pipeline.AuthExtension{
				Inbound: []pipeline.InboundAuth{{Decision: "deny"}},
			}},
			want: "deny",
		},
		{
			name: "inbound bypass",
			ev: pipeline.SessionEvent{Auth: &pipeline.AuthExtension{
				Inbound: []pipeline.InboundAuth{{Decision: "bypass"}},
			}},
			want: "bypass",
		},
		{
			name: "outbound exchange",
			ev: pipeline.SessionEvent{Auth: &pipeline.AuthExtension{
				Outbound: []pipeline.OutboundAuth{{Action: "exchange"}},
			}},
			want: "exchange",
		},
		{
			name: "outbound denied",
			ev: pipeline.SessionEvent{Auth: &pipeline.AuthExtension{
				Outbound: []pipeline.OutboundAuth{{Action: "denied"}},
			}},
			want: "denied",
		},
		{
			name: "last-wins on chain",
			ev: pipeline.SessionEvent{Auth: &pipeline.AuthExtension{
				Inbound: []pipeline.InboundAuth{
					{Plugin: "jwt-validation", Decision: "allow"},
					{Plugin: "mtls-verifier", Decision: "deny"},
				},
			}},
			want: "deny", // most recent decision shown; detail pane shows the full slice
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := authCell(tc.ev); got != tc.want {
				t.Errorf("authCell = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMatchEvent_DenyShortcut verifies that typing "deny" in the filter
// box surfaces both the new SessionDenied phase AND outbound failures
// (which fall under Auth.Outbound[].Action="denied"). This is the
// one-word way to answer "what's failing auth?" from abctl.
func TestMatchEvent_DenyShortcut(t *testing.T) {
	denied := pipeline.SessionEvent{Phase: pipeline.SessionDenied}
	if !matchEvent(denied, "deny") {
		t.Error("SessionDenied event should match the `deny` shortcut")
	}

	inboundDeny := pipeline.SessionEvent{
		Phase: pipeline.SessionRequest,
		Auth: &pipeline.AuthExtension{Inbound: []pipeline.InboundAuth{{
			Decision: "deny",
		}}},
	}
	if !matchEvent(inboundDeny, "deny") {
		t.Error("inbound-deny event should match the `deny` shortcut")
	}

	outboundDenied := pipeline.SessionEvent{
		Phase: pipeline.SessionRequest,
		Auth: &pipeline.AuthExtension{Outbound: []pipeline.OutboundAuth{{
			Action: "denied",
		}}},
	}
	if !matchEvent(outboundDenied, "deny") {
		t.Error("outbound-denied event should match the `deny` shortcut")
	}

	clean := pipeline.SessionEvent{
		Phase: pipeline.SessionRequest,
		Auth: &pipeline.AuthExtension{Inbound: []pipeline.InboundAuth{{
			Decision: "allow",
		}}},
	}
	if matchEvent(clean, "deny") {
		t.Error("allow event should NOT match the `deny` shortcut")
	}
}

// TestAuthOnlyRequestResponsePairing covers the auth-only case:
// when a pipeline runs jwt-validation (or any Auth-only plugin) with no
// body parser, the listener records BOTH a request and a response
// event. Neither carries A2A/MCP/Inference — both populate Auth only.
// Verify that:
//
//  1. The pairing function pairs them (sequential, matching on
//     responsiblePlugin which falls back to the auth plugin name).
//  2. authCell surfaces the auth decision on the response row too
//     (recordInboundResponseSession snapshots Auth onto the event).
//  3. statusCell and durationCell render the response metadata.
func TestAuthOnlyRequestResponsePairing(t *testing.T) {
	now := time.Date(2026, 5, 8, 14, 22, 5, 0, time.UTC)
	auth := &pipeline.AuthExtension{
		Inbound: []pipeline.InboundAuth{{Plugin: "jwt-validation", Decision: "allow"}},
	}
	events := []pipeline.SessionEvent{
		{At: now, Direction: pipeline.Inbound, Phase: pipeline.SessionRequest, Auth: auth, Host: "weather-agent"},
		{At: now.Add(12 * time.Millisecond), Direction: pipeline.Inbound, Phase: pipeline.SessionResponse, Auth: auth, Host: "weather-agent", StatusCode: 200, Duration: 12 * time.Millisecond},
	}

	pairs := pairRequestsAndResponses(events)
	if pairs[0] != 1 || pairs[1] != 0 {
		t.Fatalf("expected auth-only req/resp to pair sequentially, got %v", pairs)
	}

	if got := authCell(events[1]); got != "allow" {
		t.Errorf("authCell(response) = %q, want allow", got)
	}
	if got := statusCell(events[1]); got != "200" {
		t.Errorf("statusCell = %q, want 200", got)
	}
	if got := durationCell(events[1]); got != "12ms" {
		t.Errorf("durationCell = %q, want 12ms", got)
	}

	// Auth-only events have no parser; responsiblePlugin falls back
	// to the auth plugin name on both sides — the identity the
	// pairing function matches on.
	if got := responsiblePlugin(events[0]); got != "jwt-validation" {
		t.Errorf("responsiblePlugin(request) = %q, want jwt-validation", got)
	}
	if responsiblePlugin(events[0]) != responsiblePlugin(events[1]) {
		t.Errorf("auth-only req/resp disagree on responsiblePlugin: %q vs %q",
			responsiblePlugin(events[0]), responsiblePlugin(events[1]))
	}
}

// TestResponsiblePlugin_Naming locks the PLUGIN column attribution:
// every plugin that attached data is named (joined with "+"), with
// parsers listed before auth plugins. Plugins map is the last-resort
// escape hatch when no parser and no auth plugin ran.
func TestResponsiblePlugin_Naming(t *testing.T) {
	cases := []struct {
		name string
		ev   pipeline.SessionEvent
		want string
	}{
		{
			name: "a2a parser and jwt-validation both named",
			ev: pipeline.SessionEvent{
				A2A: &pipeline.A2AExtension{Method: "message/stream"},
				Auth: &pipeline.AuthExtension{Inbound: []pipeline.InboundAuth{
					{Plugin: "jwt-validation", Decision: "allow"},
				}},
			},
			want: "a2a-parser+jwt-validation",
		},
		{
			name: "inference wins over mcp false positive",
			ev: pipeline.SessionEvent{
				MCP:       &pipeline.MCPExtension{Method: ""},
				Inference: &pipeline.InferenceExtension{Model: "llama3"},
			},
			want: "inference-parser",
		},
		{
			name: "mcp with method claims the row",
			ev:   pipeline.SessionEvent{MCP: &pipeline.MCPExtension{Method: "tools/call"}},
			want: "mcp-parser",
		},
		{
			name: "auth fallback picks last inbound plugin",
			ev: pipeline.SessionEvent{Auth: &pipeline.AuthExtension{Inbound: []pipeline.InboundAuth{
				{Plugin: "jwt-validation", Decision: "allow"},
				{Plugin: "mtls-verifier", Decision: "deny"},
			}}},
			want: "mtls-verifier",
		},
		{
			name: "auth fallback picks outbound when inbound empty",
			ev: pipeline.SessionEvent{Auth: &pipeline.AuthExtension{Outbound: []pipeline.OutboundAuth{
				{Plugin: "token-exchange", Action: "exchange"},
			}}},
			want: "token-exchange",
		},
		{
			name: "plugins map is the last-resort fallback",
			ev: pipeline.SessionEvent{Plugins: map[string]json.RawMessage{
				"rate-limiter": json.RawMessage(`{"allowed":true}`),
			}},
			want: "rate-limiter",
		},
		{
			name: "empty event renders dash",
			ev:   pipeline.SessionEvent{},
			want: "—",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := responsiblePlugin(tc.ev); got != tc.want {
				t.Errorf("responsiblePlugin = %q, want %q", got, tc.want)
			}
		})
	}
}


// TestMatchEvent_PluginShortcut filters by plugin key in the escape-hatch
// Plugins map — `plugin:rate-limiter` shows only events carrying that
// plugin's event entries.
func TestMatchEvent_PluginShortcut(t *testing.T) {
	withPlugin := pipeline.SessionEvent{
		Plugins: map[string]json.RawMessage{
			"rate-limiter": json.RawMessage(`{"allowed":true}`),
		},
	}
	if !matchEvent(withPlugin, "plugin:rate-limiter") {
		t.Error("expected match on plugin:rate-limiter")
	}
	if matchEvent(withPlugin, "plugin:nonexistent") {
		t.Error("expected no match for a plugin not in the map")
	}
	bare := pipeline.SessionEvent{}
	if matchEvent(bare, "plugin:rate-limiter") {
		t.Error("event without Plugins map should not match")
	}
}
