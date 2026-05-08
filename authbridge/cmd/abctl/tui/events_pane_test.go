package tui

import (
	"encoding/json"
	"testing"

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
