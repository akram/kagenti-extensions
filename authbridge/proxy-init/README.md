# proxy-init

The `proxy-init` container programs iptables rules for an
AuthBridge-injected pod. It runs once at pod startup as a Kubernetes
init container, then exits. It has three modes, selected by the `MODE`
env var:

| `MODE` | Used by | What it does |
|---|---|---|
| `redirect` (default) | `envoy-sidecar` | Transparently **REDIRECT**s pod traffic to the Envoy listeners. |
| `enforce-redirect` | `proxy-sidecar`, `lite` | Fail-closed egress guard that **captures**: REDIRECTs external TCP that bypasses the forward proxy to AuthBridge's transparent listener; DROPs non-TCP external egress. |
| `enforce-drop` | `proxy-sidecar`, `lite` | Fail-closed egress guard that **DROP**s any egress that bypasses the forward proxy. Predates `enforce-redirect`; retained as a no-transparent-listener fallback. |

## `redirect` mode (envoy-sidecar)

`init-iptables.sh` writes iptables rules that:

- **Outbound** — Redirect traffic leaving the workload container to
  AuthBridge's outbound listener (port 15123). Adds an exclusion for
  the AuthBridge sidecar's own UID (1337) so its traffic doesn't loop
  back into itself.
- **Inbound** — Redirect traffic arriving at the workload container's
  service port to AuthBridge's inbound listener (port 15124).
- **Istio ambient coexistence** — Cooperates with ztunnel by
  preserving the Istio fwmark (0x539) and respecting the HBONE port
  (15008). Designed to work alongside `istio.io/dataplane-mode:
  ambient`.
- **Configurable exclusions** — Honors `OUTBOUND_PORTS_EXCLUDE` and
  `INBOUND_PORTS_EXCLUDE` env vars (commonly used to exclude
  Keycloak's port 8080 to avoid token-exchange loops).

## `enforce-redirect` mode (proxy-sidecar)

In `proxy-sidecar` mode the workload is configured with `HTTP_PROXY`
pointing at AuthBridge's forward proxy. On its own that is purely
cooperative — an app that ignores `HTTP_PROXY` (or sets `NO_PROXY`)
egresses directly and bypasses AuthBridge. `enforce-redirect` closes
that gap **by capturing** the bypass traffic instead of dropping it:
external TCP that did not go through the forward proxy is transparently
REDIRECTed to AuthBridge's **transparent listener** (`TRANSPARENT_PORT`,
default 8082), which recovers the original destination via
`SO_ORIGINAL_DST` and tunnels it through the same outbound pipeline.
Because nothing is dropped, agents that ignore `HTTP_PROXY` keep working
— which is what lets enforcement be always-on.

`init-iptables.sh` installs **two** chains, because `REDIRECT` is a
nat-table target but the nat table forbids `DROP` (`iptables` errors with
"the use of DROP is therefore inhibited"):

- **`nat` OUTPUT / `AB_REDIRECT`** (position 1): `RETURN` ztunnel mark
  `0x539`, the proxy UID (`--uid-owner $PROXY_UID`, avoids the loop),
  loopback, and `CLUSTER_CIDRS`; then `REDIRECT` external **TCP** to
  `TRANSPARENT_PORT`.
- **`mangle` OUTPUT / `AB_NOTCP`** (position 1): the same exemptions
  (plus `ESTABLISHED,RELATED` first, so UDP conntrack replies like DNS
  pass), then `-p tcp -j RETURN` (TCP is handled by the nat REDIRECT) and
  a terminal `DROP` for external **non-TCP** (UDP/QUIC), so HTTP/3 cannot
  bypass — well-behaved clients fall back to TCP and get captured.

Because the OUTPUT hook order is `raw → mangle → nat → filter`, the
mangle chain drops non-TCP on its original destination while TCP falls
through to the nat REDIRECT. Both chains are inserted at position 1,
ahead of Istio's appended (`-A`) chains, so they preempt ambient's nat
redirect for external destinations — exactly as `redirect` mode does for
the Envoy path. IPv6 mirrors apply the same rules. See
[`test-enforce-redirect.sh`](./test-enforce-redirect.sh), which proves
the capture, the preemption, and the non-TCP drop via packet counters.

`CLUSTER_CIDRS` has the same Kind-shaped default and OCP/EKS override
requirement as documented under `enforce-drop` below.

## `enforce-drop` mode (proxy-sidecar)

In `proxy-sidecar` mode the workload is configured with `HTTP_PROXY`
pointing at AuthBridge's forward proxy. On its own that is purely
cooperative — an app that ignores `HTTP_PROXY` (or sets `NO_PROXY`)
egresses directly and bypasses AuthBridge. `enforce-drop` closes that
gap **without** transparently redirecting (you cannot REDIRECT raw
traffic into a CONNECT forward proxy): it installs a fail-closed guard
that DROPs any direct egress, forcing all external traffic through the
proxy regardless of whether the app honors `HTTP_PROXY`.

`init-iptables.sh` builds a dedicated `AB_EGRESS` chain hooked from
**`mangle` OUTPUT at position 1**, with this order:

1. `RETURN` ztunnel's own sockets (fwmark `0x539`) — keeps the mesh path working; a no-op when ambient is absent.
2. `RETURN` the proxy's own re-originated egress (`--uid-owner $PROXY_UID`, default 1337).
3. `RETURN` loopback (the app → proxy hop) and in-cluster CIDRs (`CLUSTER_CIDRS`, mesh/DNS).
4. `DROP` everything else — direct external egress, including UDP (QUIC/HTTP-3).

An IPv6 mirror drops external v6 egress (allowing loopback, link-local,
the proxy UID, and `CLUSTER_CIDRS6`).

> **`CLUSTER_CIDRS` is Kind-shaped by default.** The `10.0.0.0/8` default
> covers Kind (pods `10.244.0.0/16` + services `10.96.0.0/16`). Other
> distros differ — **OpenShift** uses services `172.30.0.0/16` and pods
> `10.128.0.0/14`, and `172.30.0.0/16` is **outside** `10/8`, so the
> default would drop in-cluster service traffic. On OCP/EKS/etc. you
> **must** override `CLUSTER_CIDRS` with the cluster's real pod+service
> ranges. The script logs the resolved value at startup, and the
> operator wiring (follow-up PR) sets it from the cluster's CIDRs.

> **`enforce-drop` intentionally ignores `OUTBOUND_PORTS_EXCLUDE`** (a
> `redirect`-mode knob). Any destination previously bypassed that way —
> e.g. a direct LLM endpoint at `host.docker.internal:11434` — is now
> dropped unless it goes through the forward proxy or falls within
> `CLUSTER_CIDRS`. That is the point: `enforce-drop` closes direct-egress
> holes. Operators relying on a bypass must route it through the proxy
> (or, for in-cluster targets, include it in `CLUSTER_CIDRS`).

**Why `mangle` OUTPUT, not `filter`:** when Istio ambient is active it
installs an in-pod `nat OUTPUT` REDIRECT (`ISTIO_OUTPUT` → ztunnel
`:15001`). The netfilter OUTPUT hook order is `raw → mangle → nat →
filter`, so a DROP in `mangle` evaluates the original destination and
fires **before** ambient's nat redirect can rewrite it; a DROP in
`filter` would run after nat and be defeated. `-I 1` also places the
chain ahead of Istio's appended (`-A`) mangle chain. This makes the
guard robust with no ambient, in-pod ambient, or node-level ambient.
See [`test-enforce-drop.sh`](./test-enforce-drop.sh), which proves the
preemption via packet counters.

## iptables backend

The script auto-detects `iptables-legacy` vs `iptables-nft` and uses
whichever the host kernel exposes. Override with `IPTABLES_CMD` (and
`IP6TABLES_CMD`) if needed.

## Environment variables

| Variable | Default | Mode | Purpose |
|---|---|---|---|
| `MODE` | `redirect` | all | `redirect` (envoy-sidecar), `enforce-redirect` or `enforce-drop` (proxy-sidecar) |
| `PROXY_UID` | `1337` | all | UID of the AuthBridge sidecar process; exempted from redirect / drop |
| `PROXY_PORT` | `15123` | redirect | AuthBridge outbound listener port |
| `INBOUND_PROXY_PORT` | `15124` | redirect | AuthBridge inbound listener port |
| `TRANSPARENT_PORT` | `8082` | enforce-redirect | AuthBridge transparent listener port; REDIRECT target for captured external TCP egress |
| `OUTBOUND_PORTS_EXCLUDE` | (empty) | redirect | Comma-separated outbound port list to skip (e.g. `8080`) |
| `INBOUND_PORTS_EXCLUDE` | (empty) | redirect | Comma-separated inbound port list to skip |
| `POD_IP` | (required in `redirect`) | redirect | Set via Downward API; DNAT target for ambient-mesh inbound. Not used by `enforce-*`. |
| `CLUSTER_CIDRS` | `10.0.0.0/8` | enforce-redirect, enforce-drop | Comma-separated in-cluster CIDRs allowed direct (pods/services/DNS) |
| `CLUSTER_CIDRS6` | (empty) | enforce-redirect, enforce-drop | IPv6 in-cluster CIDRs (dual-stack); empty drops all external v6 egress |
| `IPTABLES_CMD` | auto-detected | all | Override iptables binary (`iptables-legacy` / `iptables-nft`) |
| `IP6TABLES_CMD` | derived from `IPTABLES_CMD` | enforce-redirect, enforce-drop | Override ip6tables binary |

## Required Kubernetes capabilities

The container needs `NET_ADMIN` and `NET_RAW` capabilities and runs as
UID 0 — but **not** privileged mode. The kagenti-operator's webhook
sets up the SecurityContext correctly when injecting the init
container.

## Building

```sh
make docker-build-init
make load-image          # load into a kind cluster
```

The image is published from CI as
`ghcr.io/kagenti/kagenti-extensions/proxy-init:<tag>` (build defined
in [`.github/workflows/build.yaml`](../../.github/workflows/build.yaml)).

## Testing

[`test-enforce-redirect.sh`](./test-enforce-redirect.sh) validates
`enforce-redirect` mode in a private network namespace (`unshare --net`):
it asserts the `AB_REDIRECT` rule structure, proves external TCP is
captured to `TRANSPARENT_PORT` while preempting a simulated Istio ambient
`nat OUTPUT` REDIRECT, and proves external UDP is dropped — all via packet
counters. [`test-enforce-drop.sh`](./test-enforce-drop.sh) does the same
for `enforce-drop` (asserts `AB_EGRESS` structure and the `mangle` DROP
preemption). Both require root + iptables-nft on Linux (run on CI; not
macOS):

```sh
sudo ./test-enforce-redirect.sh
sudo ./test-enforce-drop.sh
```

## Where it gets injected

The kagenti-operator's mutating webhook injects the proxy-init
container automatically:

- `redirect` mode (`MODE` unset) when the resolved AuthBridge mode is
  `envoy-sidecar`.
- `enforce-redirect` mode (`MODE=enforce-redirect`) when the resolved
  AuthBridge mode is `proxy-sidecar` / `lite` — the transparent listener
  in those images receives the captured egress. _The operator wiring that
  sets this lands in the companion kagenti-operator PR; this PR adds the
  mode to the image and the transparent listener to the proxy._
- `enforce-drop` mode (`MODE=enforce-drop`) — the drop-based fallback,
  retained for environments without the transparent listener.

See
[`authbridge/demos/weather-agent/demo-ui-advanced.md`](../demos/weather-agent/demo-ui-advanced.md)
for an end-to-end demo and
[`authbridge/demos/token-exchange-routes/README.md`](../demos/token-exchange-routes/README.md)
for the route-config reference.
