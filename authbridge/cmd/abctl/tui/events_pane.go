package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"github.com/kagenti/kagenti-extensions/authbridge/authlib/pipeline"
)

// newEventsTable builds an empty events table. Uses the shared tableStyles
// (including the Reverse-based Selected highlight) like the other panes —
// now safe because per-cell ANSI coloring was removed from this table.
func newEventsTable() table.Model {
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "TIME", Width: 12},
			{Title: "DIR", Width: 4},
			{Title: "PHASE", Width: 6},
			{Title: "ACTION", Width: 8},
			{Title: "PLUGIN", Width: 18},
			{Title: "METHOD", Width: 22},
			{Title: "STATUS", Width: 7},
			{Title: "DURATION", Width: 10},
			{Title: "TOKENS", Width: 8},
			{Title: "HOST", Width: 20},
		}),
		table.WithFocused(true),
	)
	t.SetStyles(tableStyles())
	return t
}

// rebuildEventsTable populates the events table from the cache for the
// currently selected session, applying filter + preserving cursor. Also
// resizes the table height to account for the IDENTITY banner — when
// the session has inbound identity, subtract the banner's rendered
// height so it doesn't push rows off-screen; otherwise claim the full
// body height.
func (m *model) rebuildEventsTable() {
	events := m.events[m.selectedSess]

	if m.bodyHeight > 0 {
		h := m.bodyHeight
		if len(distinctInboundIdentities(events)) > 0 {
			h -= identityBannerHeight
		}
		if h < 3 {
			h = 3
		}
		m.eventsTbl.SetHeight(h)
	}

	prevRow := m.eventsTbl.Cursor()
	wasAtEnd := prevRow >= len(m.eventsTbl.Rows())-1

	// Flatten (event, invocation) into row specs up-front so pair-linking
	// and filtering can run against the flat row list. Events without
	// invocations fall back to a single pseudo-row (unusual — the listener
	// only records events that have at least one Invocation or A2A/MCP/
	// Inference extension, but parser-only events can still land here if
	// the parser populated its extension without emitting an Invocation).
	rowSpecs := flattenInvocations(events)

	// Pair request/response rows by (direction, plugin) so each plugin's
	// contribution on the request side connects to its contribution on the
	// response side, independent of other plugins in the same pipeline.
	pairs := pairInvocationRows(rowSpecs)

	rows := make([]table.Row, 0, len(rowSpecs))
	m.visibleRows = m.visibleRows[:0]
	for i, rs := range rowSpecs {
		if m.filter != "" && !matchInvocationRow(rs, m.filter) {
			continue
		}
		phase := shortPhase(rs.event.Phase)
		if rs.event.Phase == pipeline.SessionResponse {
			if _, paired := pairs[i]; paired {
				// └ prefix visually connects the response row to its
				// request row in the same (direction, plugin) pair.
				phase = "└" + phase
			}
		}
		rows = append(rows, table.Row{
			rs.event.At.Format("15:04:05.00"),
			shortDirection(rs.event.Direction),
			phase,
			rs.actionCell(),
			truncStr(rs.pluginCell(), 18),
			eventMethod(*rs.event),
			statusCell(*rs.event),
			durationCell(*rs.event),
			tokensCell(*rs.event),
			truncStr(rs.event.Host, 20),
		})
		m.visibleRows = append(m.visibleRows, rs)
	}
	m.eventsTbl.SetRows(rows)

	// Auto-follow: if user was at the bottom, stay at the bottom. Otherwise
	// preserve position so reading isn't disturbed by new events.
	if wasAtEnd && len(rows) > 0 {
		m.eventsTbl.SetCursor(len(rows) - 1)
	} else if prevRow < len(rows) {
		m.eventsTbl.SetCursor(prevRow)
	}
}

// selectedEvent returns the event at the cursor row, or nil. The cursor
// points into m.visibleRows (the flattened row list), and each row carries
// a reference to its source event.
func (m *model) selectedEvent() *pipeline.SessionEvent {
	if len(m.visibleRows) == 0 {
		return nil
	}
	cur := m.eventsTbl.Cursor()
	if cur < 0 || cur >= len(m.visibleRows) {
		return nil
	}
	return m.visibleRows[cur].event
}

// invocationRow is one table row — the cartesian product of SessionEvent
// × Invocation. An event with N plugin invocations produces N rows; an
// event with no invocations produces one row with an empty invocation.
// Rendering and filtering both work off this flat list.
type invocationRow struct {
	event *pipeline.SessionEvent
	// inv may be nil when the event has no Invocation records. The
	// pseudo-row still renders so the event is reachable in the table.
	inv *pipeline.Invocation
	// direction is the Invocations.{Inbound,Outbound} this row came
	// from, disambiguating when a single event somehow carries both
	// (doesn't happen today but cheap to be explicit).
	direction pipeline.Direction
}

func (r invocationRow) actionCell() string {
	if r.inv == nil {
		return "—"
	}
	return string(r.inv.Action)
}

func (r invocationRow) pluginCell() string {
	if r.inv == nil {
		return "—"
	}
	return r.inv.Plugin
}

// flattenInvocations walks the event slice in order and, for each event,
// emits one invocationRow per Invocation it carries (Inbound then
// Outbound). Events with no Invocations fall back to a single pseudo-row
// so parser-only events (a SessionEvent carrying just MCP or A2A with no
// matching Invocation) remain reachable.
func flattenInvocations(events []pipeline.SessionEvent) []invocationRow {
	out := make([]invocationRow, 0, len(events))
	for i := range events {
		e := &events[i]
		if e.Invocations == nil || (len(e.Invocations.Inbound) == 0 && len(e.Invocations.Outbound) == 0) {
			out = append(out, invocationRow{event: e, direction: e.Direction})
			continue
		}
		for j := range e.Invocations.Inbound {
			out = append(out, invocationRow{
				event:     e,
				inv:       &e.Invocations.Inbound[j],
				direction: pipeline.Inbound,
			})
		}
		for j := range e.Invocations.Outbound {
			out = append(out, invocationRow{
				event:     e,
				inv:       &e.Invocations.Outbound[j],
				direction: pipeline.Outbound,
			})
		}
	}
	return out
}

func shortDirection(d pipeline.Direction) string {
	if d == pipeline.Inbound {
		return "in"
	}
	return "out"
}

func shortPhase(p pipeline.SessionPhase) string {
	switch p {
	case pipeline.SessionRequest:
		return "req"
	case pipeline.SessionResponse:
		return "resp"
	case pipeline.SessionDenied:
		return "deny"
	}
	return "?"
}

// (authCell and responsiblePlugin are gone — their roles moved onto
// invocationRow's actionCell/pluginCell because each row now corresponds
// to exactly one plugin's invocation rather than a whole event.)

func eventMethod(e pipeline.SessionEvent) string {
	switch {
	case e.A2A != nil:
		return truncStr(e.A2A.Method, 22)
	case e.Inference != nil:
		return truncStr(e.Inference.Model, 22)
	case e.MCP != nil:
		return truncStr(e.MCP.Method, 22)
	}
	return ""
}

func statusCell(e pipeline.SessionEvent) string {
	if e.StatusCode == 0 {
		return ""
	}
	return fmt.Sprintf("%d", e.StatusCode)
}

// tokensCell shows the total token count for inference response rows so
// operators can spot expensive calls while scrolling. Blank for every
// other event type (a2a, mcp, inference *request*). Uses the same
// thousands-separator formatter as the sessions-pane totals.
func tokensCell(e pipeline.SessionEvent) string {
	if e.Phase != pipeline.SessionResponse || e.Inference == nil || e.Inference.TotalTokens == 0 {
		return ""
	}
	return formatCount(e.Inference.TotalTokens)
}

func durationCell(e pipeline.SessionEvent) string {
	if e.Duration == 0 {
		return ""
	}
	ms := e.Duration.Milliseconds()
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 2 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// matchInvocationRow does a case-insensitive substring match across every
// string field the operator might reasonably search for — the invocation's
// own fields plus the containing event's protocol extensions. Two prefix
// shortcuts:
//
//   - `deny` alone matches SessionDenied events and any invocation
//     whose Action == ActionDeny — the one-word "show me failures"
//     filter.
//   - `plugin:<name>` matches rows whose escape-hatch Plugins map on
//     the parent event has <name> as a key.
func matchInvocationRow(r invocationRow, q string) bool {
	q = strings.ToLower(q)

	if q == "deny" {
		if r.event.Phase == pipeline.SessionDenied {
			return true
		}
		if r.inv != nil && r.inv.Action == pipeline.ActionDeny {
			return true
		}
		return false
	}

	if after, ok := strings.CutPrefix(q, "plugin:"); ok {
		_, present := r.event.Plugins[after]
		return present
	}

	e := r.event
	hay := []string{e.Host, e.TargetAudience, eventMethod(*e)}
	if r.inv != nil {
		hay = append(hay,
			r.inv.Plugin, string(r.inv.Action), r.inv.Reason, r.inv.Path,
			r.inv.ExpectedIssuer, r.inv.ExpectedAudience, r.inv.TokenSubject,
			r.inv.RouteHost, r.inv.TargetAudience)
	}
	if e.Identity != nil {
		hay = append(hay, e.Identity.Subject, e.Identity.ClientID)
	}
	if e.A2A != nil {
		hay = append(hay, e.A2A.SessionID, e.A2A.MessageID, e.A2A.Role)
		for _, p := range e.A2A.Parts {
			hay = append(hay, p.Content)
		}
	}
	if e.MCP != nil && e.MCP.Err != nil {
		hay = append(hay, e.MCP.Err.Message)
	}
	if e.Inference != nil {
		hay = append(hay, e.Inference.Completion, e.Inference.FinishReason)
	}
	for _, s := range hay {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// pairInvocationRows pairs request-phase rows with their response-phase
// counterparts by (direction, plugin). Each plugin's contribution on the
// request side connects to its own contribution on the response side,
// independent of other plugins in the same pipeline — so a jwt-validation
// request row pairs with a jwt-validation response row even when several
// other plugins fired on the same event.
//
// Sequential pairing is good enough for current traffic: each request
// row is paired with the NEXT response row that shares (direction, plugin)
// and hasn't been claimed.
func pairInvocationRows(rows []invocationRow) map[int]int {
	pairs := make(map[int]int)
	key := func(r invocationRow) (string, pipeline.Direction, bool) {
		if r.inv == nil {
			return "", r.direction, false
		}
		return r.inv.Plugin, r.direction, true
	}
	for i := range rows {
		if rows[i].event.Phase != pipeline.SessionRequest {
			continue
		}
		if _, already := pairs[i]; already {
			continue
		}
		plug, dir, ok := key(rows[i])
		if !ok {
			continue
		}
		for j := i + 1; j < len(rows); j++ {
			if rows[j].event.Phase != pipeline.SessionResponse {
				continue
			}
			if _, taken := pairs[j]; taken {
				continue
			}
			rplug, rdir, rok := key(rows[j])
			if !rok || rplug != plug || rdir != dir {
				continue
			}
			pairs[i] = j
			pairs[j] = i
			break
		}
	}
	return pairs
}

// identityBannerStyle renders the small bordered box above the events
// table. Rounded border matches the outer frame; muted color keeps the
// banner as context rather than competing with the event rows.
var identityBannerStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.AdaptiveColor{Light: "#94A3B8", Dark: "#475569"}).
	Padding(0, 1)

// identityBannerHeight is the rendered height of the banner — four lines
// of content plus two border lines. layout() subtracts this from the
// events-table height so the banner doesn't push rows off-screen.
const identityBannerHeight = 6

// identityBanner renders a compact "IDENTITY" box summarizing the caller
// of this session's inbound events. If callers diverge across the
// session, it reports the count so the operator knows to check detail
// rows. Returns an empty string when no inbound identity is present
// (e.g. outbound-only buckets).
func identityBanner(events []pipeline.SessionEvent) string {
	idents := distinctInboundIdentities(events)
	if len(idents) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("IDENTITY"))
	b.WriteByte('\n')

	if len(idents) == 1 {
		id := idents[0]
		b.WriteString(fmt.Sprintf("subject  %s\n", nonEmpty(id.Subject, "—")))
		b.WriteString(fmt.Sprintf("client   %s\n", nonEmpty(id.ClientID, "—")))
		b.WriteString(fmt.Sprintf("scopes   %s", nonEmpty(truncateScopes(id.Scopes, 3), "—")))
	} else {
		// Multiple distinct callers — surface the count; detail rows
		// carry the full identity for drill-down.
		subjects := make([]string, 0, len(idents))
		for _, id := range idents {
			subjects = append(subjects, nonEmpty(id.Subject, "—"))
		}
		b.WriteString(fmt.Sprintf("subjects  %d distinct: %s\n", len(idents), strings.Join(subjects, ", ")))
		b.WriteString("client    (see individual events)\n")
		b.WriteString("scopes    (see individual events)")
	}
	return identityBannerStyle.Render(b.String())
}

// identityKey is the comparable shape used to dedupe identities in the
// banner. Using a struct avoids string concatenation (and the theoretical
// "|" collision) — subject+clientID are the two fields that define a
// unique caller; scopes can legitimately vary turn-to-turn.
type identityKey struct {
	subject  string
	clientID string
}

// distinctInboundIdentities returns the unique EventIdentity values seen on
// inbound events, in first-seen order.
func distinctInboundIdentities(events []pipeline.SessionEvent) []*pipeline.EventIdentity {
	var out []*pipeline.EventIdentity
	seen := map[identityKey]bool{}
	for i := range events {
		e := &events[i]
		if e.Direction != pipeline.Inbound || e.Identity == nil {
			continue
		}
		k := identityKey{subject: e.Identity.Subject, clientID: e.Identity.ClientID}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e.Identity)
	}
	return out
}

// truncateScopes joins the first n scopes with commas and appends a
// "+N more" suffix if the list was longer. Keeps the identity banner
// from overflowing the terminal when a caller has many scopes.
func truncateScopes(scopes []string, n int) string {
	if len(scopes) == 0 {
		return ""
	}
	if len(scopes) <= n {
		return strings.Join(scopes, ", ")
	}
	return strings.Join(scopes[:n], ", ") + fmt.Sprintf(" +%d more", len(scopes)-n)
}

