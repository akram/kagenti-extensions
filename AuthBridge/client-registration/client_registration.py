"""
client_registration.py

Registers a Keycloak client and stores its secret in a file.
Also creates an audience scope for the agent and adds it to
platform clients (e.g., the UI client) so they can reach
AuthBridge-protected agents without manual Keycloak configuration.

Registration modes (KEYCLOAK_REGISTRATION_MODE):
- admin-api (default): Keycloak Admin REST API via KEYCLOAK_ADMIN_USERNAME/PASSWORD.
- dynamic-svid: RFC 7591-style Client Registration Service using the workload JWT-SVID
  as Authorization: Bearer (no admin credentials in the pod). Requires SPIRE and a
  Keycloak configuration that accepts this token for client creation (e.g. token
  exchange to a registration-capable access token, or a realm-specific policy).

Idempotent:
- Creates the client if it does not exist.
- If the client already exists, reuses it (admin path) or treats 409 as success (DCR path).
- Writes client secret when available (empty for federated-jwt when no secret is issued).
"""

from __future__ import annotations

import json
import os
import re
import sys
import ssl
import urllib.error
import urllib.request
from typing import Any

import jwt
from keycloak import KeycloakAdmin, KeycloakPostError


def get_env_var(name: str, default: str | None = None) -> str:
    """
    Fetch an environment variable or return default if provided.
    Raise ValueError if missing and no default is set.
    """
    value = os.environ.get(name)
    if value is not None and value != "":
        return value
    if default is not None:
        return default
    raise ValueError(f"Missing required environment variable: {name}")


def derive_keycloak_config_from_token_url(token_url: str) -> tuple[str | None, str | None]:
    """
    Derive KEYCLOAK_URL and KEYCLOAK_REALM from TOKEN_URL.

    Example:
        TOKEN_URL: http://keycloak-service.keycloak.svc:8080/realms/kagenti/protocol/openid-connect/token
        Returns: ("http://keycloak-service.keycloak.svc:8080", "kagenti")

    Returns (None, None) if parsing fails.
    """
    # Pattern: <base_url>/realms/<realm>/protocol/openid-connect/token
    match = re.match(r"^(https?://[^/]+)/realms/([^/]+)/", token_url)
    if match:
        return match.group(1), match.group(2)
    return None, None


def read_jwt_svid_file(path: str = "/opt/jwt_svid.token") -> str:
    """Read raw JWT-SVID from disk (trimmed)."""
    try:
        with open(path, encoding="utf-8") as f:
            content = f.read().strip()
    except OSError as e:
        raise RuntimeError(f"Cannot read JWT-SVID file {path}: {e}") from e
    if not content:
        raise RuntimeError(f"JWT-SVID file {path} is empty")
    return content


def registration_url(keycloak_url: str, realm: str) -> str:
    base = keycloak_url.rstrip("/")
    return f"{base}/realms/{realm}/clients-registrations/default"


def post_client_registration(
    keycloak_url: str,
    realm: str,
    bearer_token: str,
    client_payload: dict[str, Any],
    *,
    insecure_tls: bool = False,
) -> tuple[int, dict[str, Any] | None, str]:
    """
    POST a Keycloak ClientRepresentation to the Client Registration Service.
    Returns (status_code, parsed_json_or_none, response_body_text).
    """
    url = registration_url(keycloak_url, realm)
    body_bytes = json.dumps(client_payload).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=body_bytes,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Accept": "application/json",
            "Authorization": f"Bearer {bearer_token}",
        },
    )
    ctx = None
    if url.startswith("https://") and insecure_tls:
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    try:
        with urllib.request.urlopen(req, timeout=120, context=ctx) as resp:
            text = resp.read().decode("utf-8", errors="replace")
            if not text.strip():
                return resp.status, None, text
            try:
                return resp.status, json.loads(text), text
            except json.JSONDecodeError:
                return resp.status, None, text
    except urllib.error.HTTPError as e:
        text = e.read().decode("utf-8", errors="replace") if e.fp else ""
        parsed: dict[str, Any] | None = None
        if text.strip():
            try:
                parsed = json.loads(text)
            except json.JSONDecodeError:
                pass
        return e.code, parsed, text


def register_client_dynamic_svid(
    keycloak_url: str,
    realm: str,
    jwt_svid: str,
    client_id: str,
    client_payload: dict[str, Any],
) -> tuple[str, dict[str, Any] | None]:
    """
    Create or ensure client via Client Registration Service using JWT-SVID as Bearer.
    Returns (internal_client_id, registration_response_dict_or_partial).
    """
    insecure = os.environ.get("KEYCLOAK_TLS_INSECURE_SKIP_VERIFY", "").lower() in (
        "1",
        "true",
        "yes",
    )
    status, parsed, raw = post_client_registration(
        keycloak_url, realm, jwt_svid, client_payload, insecure_tls=insecure
    )

    if status in (200, 201) and parsed is not None:
        internal_id = parsed.get("id") or client_id
        print(f'Created or updated client via DCR "{client_id}" (internal id: {internal_id})')
        return str(internal_id), parsed

    if status == 409:
        print(
            f'Client "{client_id}" already exists (HTTP 409 from registration service). '
            "Treating as idempotent success."
        )
        return client_id, parsed

    if status == 401:
        print(
            "Client registration returned HTTP 401. The JWT-SVID was not accepted as a "
            "bearer token for dynamic client registration. Configure Keycloak to issue or "
            "accept a registration-capable token (for example token exchange from the SVID, "
            "or an initial access token via KEYCLOAK_REGISTRATION_INITIAL_ACCESS_TOKEN)."
        )
    else:
        print(f"Client registration failed: HTTP {status}")
    if raw:
        print(f"Response body: {raw[:2000]}")
    raise RuntimeError(f"Dynamic client registration failed with HTTP {status}")


def write_client_secret(
    keycloak_admin: KeycloakAdmin,
    internal_client_id: str,
    client_name: str,
    secret_file_path: str = "secret.txt",
) -> None:
    """
    Retrieve the secret for a Keycloak client and write it to a file.
    """
    try:
        # There will be a value field if client authentication is enabled
        # client authentication is enabled if "publicClient" is False
        secret = keycloak_admin.get_client_secrets(internal_client_id)["value"]
        print(f'Successfully retrieved secret for client "{client_name}".')
    except KeycloakPostError as e:
        print(f"Could not retrieve secret for client '{client_name}': {e}")
        return

    try:
        with open(secret_file_path, "w", encoding="utf-8") as f:
            f.write(secret)
        print(f'Secret written to file: "{secret_file_path}"')
    except OSError as ose:
        print(f"Error writing secret to file: {ose}")


def write_secret_file(secret_file_path: str, value: str) -> None:
    try:
        with open(secret_file_path, "w", encoding="utf-8") as f:
            f.write(value)
        print(f'Wrote credential file: "{secret_file_path}" (length {len(value)})')
    except OSError as ose:
        print(f"Error writing secret file: {ose}")


# TODO: refactor this function so kagenti-client-registration image can use it
def register_client(keycloak_admin: KeycloakAdmin, client_id: str, client_payload: dict[str, Any]) -> str:
    """
    Ensure a Keycloak client exists.
    Returns the internal client ID.
    """
    internal_client_id = keycloak_admin.get_client_id(client_id)
    if internal_client_id:
        print(f'Client "{client_id}" already exists with ID: {internal_client_id}')
        return internal_client_id

    # Create client
    try:
        internal_client_id = keycloak_admin.create_client(client_payload)

        print(f'Created Keycloak client "{client_id}": {internal_client_id}')
        return internal_client_id
    except KeycloakPostError as e:
        if getattr(e, "response_code", None) == 409:
            # Client exists but get_client_id missed it (URL encoding issue
            # with SPIFFE IDs containing ://).  Search all clients instead.
            print(f'Client "{client_id}" already exists (409). Looking up by listing all clients...')
            all_clients = keycloak_admin.get_clients()
            for c in all_clients:
                if c.get("clientId") == client_id:
                    print(f'Found existing client "{client_id}" with ID: {c["id"]}')
                    return c["id"]
        print(f"Could not create client '{client_id}': {e}")
        raise


def get_client_id() -> str:
    """
    Read the SVID JWT from file and extract the client ID from the "sub" claim.
    """
    jwt_file_path = "/opt/jwt_svid.token"
    content = None
    try:
        with open(jwt_file_path, encoding="utf-8") as file:
            content = file.read()

    except FileNotFoundError:
        print(f"Error: The file {jwt_file_path} was not found.")
    except OSError as e:
        print(f"An error occurred: {e}")

    if content is None or content.strip() == "":
        raise OSError("No content read from SVID JWT.")

    # Decode JWT to get client ID
    decoded = jwt.decode(content, options={"verify_signature": False})
    if "sub" not in decoded:
        raise ValueError('SVID JWT does not contain a "sub" claim.')
    return decoded["sub"]


def get_or_create_audience_scope(
    keycloak_admin: KeycloakAdmin, scope_name: str, audience: str
) -> str | None:
    """
    Create a client scope with an audience mapper if it doesn't exist.
    Returns the scope ID, or None on failure.
    """
    scopes = keycloak_admin.get_client_scopes()
    for scope in scopes:
        if scope["name"] == scope_name:
            print(f'Audience scope "{scope_name}" already exists with ID: {scope["id"]}')
            return scope["id"]

    try:
        scope_id = keycloak_admin.create_client_scope(
            {
                "name": scope_name,
                "protocol": "openid-connect",
                "attributes": {
                    "include.in.token.scope": "true",
                    "display.on.consent.screen": "true",
                },
            }
        )
        print(f'Created audience scope "{scope_name}": {scope_id}')
    except KeycloakPostError as e:
        print(f'Could not create audience scope "{scope_name}": {e}')
        return None

    # Add audience mapper to the scope
    mapper_payload = {
        "name": scope_name,
        "protocol": "openid-connect",
        "protocolMapper": "oidc-audience-mapper",
        "consentRequired": False,
        "config": {
            "included.custom.audience": audience,
            "id.token.claim": "false",
            "access.token.claim": "true",
            "userinfo.token.claim": "false",
        },
    }
    try:
        keycloak_admin.add_mapper_to_client_scope(scope_id, mapper_payload)
        print(f'Added audience mapper for "{audience}" to scope "{scope_name}"')
    except Exception as e:
        print(f"Note: Could not add audience mapper (might already exist): {e}")

    return scope_id


def add_scope_to_platform_clients(
    keycloak_admin: KeycloakAdmin,
    scope_id: str,
    scope_name: str,
    platform_client_ids: list[str],
) -> None:
    """
    Add an audience scope as a default client scope on each platform client.
    This ensures existing clients (like the UI) include the agent's audience
    in their tokens without requiring manual Keycloak configuration.
    """
    for platform_client_id in platform_client_ids:
        internal_id = keycloak_admin.get_client_id(platform_client_id)
        if not internal_id:
            print(
                f'Platform client "{platform_client_id}" not found in realm. '
                f"Skipping scope assignment."
            )
            continue
        try:
            keycloak_admin.add_client_default_client_scope(
                internal_id, scope_id, {}
            )
            print(
                f'Added scope "{scope_name}" to platform client "{platform_client_id}".'
            )
        except Exception as e:
            # 409 Conflict means it's already assigned — that's fine
            if "409" in str(e) or "already" in str(e).lower():
                print(
                    f'Scope "{scope_name}" already assigned to "{platform_client_id}".'
                )
            else:
                print(
                    f'Could not add scope "{scope_name}" to "{platform_client_id}": {e}'
                )


client_name = get_env_var("CLIENT_NAME")

# If SPIFFE is enabled, use the client ID from the SVID JWT.
# Otherwise, use the client name as the client ID.
if get_env_var("SPIRE_ENABLED", "false").lower() == "true":
    client_id = get_client_id()
else:
    client_id = client_name

# Try to derive KEYCLOAK_URL and KEYCLOAK_REALM from TOKEN_URL if not directly provided
# This provides backwards compatibility and reduces configuration duplication
TOKEN_URL = os.environ.get("TOKEN_URL")
DERIVED_KEYCLOAK_URL = None
DERIVED_KEYCLOAK_REALM = None
if TOKEN_URL:
    DERIVED_KEYCLOAK_URL, DERIVED_KEYCLOAK_REALM = derive_keycloak_config_from_token_url(TOKEN_URL)
    if DERIVED_KEYCLOAK_URL:
        print(f"Derived KEYCLOAK_URL from TOKEN_URL: {DERIVED_KEYCLOAK_URL}")
    if DERIVED_KEYCLOAK_REALM:
        print(f"Derived KEYCLOAK_REALM from TOKEN_URL: {DERIVED_KEYCLOAK_REALM}")

try:
    # Try explicit env var first, then fall back to derived value from TOKEN_URL
    KEYCLOAK_URL = get_env_var("KEYCLOAK_URL", DERIVED_KEYCLOAK_URL)
    KEYCLOAK_REALM = get_env_var("KEYCLOAK_REALM", DERIVED_KEYCLOAK_REALM)
    KEYCLOAK_TOKEN_EXCHANGE_ENABLED = (
        get_env_var("KEYCLOAK_TOKEN_EXCHANGE_ENABLED", "true").lower() == "true"
    )
    KEYCLOAK_CLIENT_REGISTRATION_ENABLED = (
        get_env_var("KEYCLOAK_CLIENT_REGISTRATION_ENABLED", "true").lower() == "true"
    )
    # CLIENT_AUTH_TYPE controls how the client authenticates to Keycloak:
    # - "client-secret": Traditional client_secret authentication (default)
    # - "federated-jwt": JWT-SVID authentication via SPIFFE identity provider
    CLIENT_AUTH_TYPE = get_env_var("CLIENT_AUTH_TYPE", "client-secret")
    SPIRE_ENABLED = get_env_var("SPIRE_ENABLED", "false").lower() == "true"
except ValueError as e:
    print(
        f"Expected environment variable missing. Skipping client registration of {client_id}."
    )
    print(e)
    sys.exit(1)

# KEYCLOAK_REGISTRATION_MODE: admin-api | dynamic-svid | (unset = auto)
_reg_mode_raw = os.environ.get("KEYCLOAK_REGISTRATION_MODE", "").strip().lower()
if _reg_mode_raw == "dynamic-svid":
    USE_DYNAMIC_SVID_REGISTRATION = True
elif _reg_mode_raw == "admin-api":
    USE_DYNAMIC_SVID_REGISTRATION = False
else:
    USE_DYNAMIC_SVID_REGISTRATION = SPIRE_ENABLED and CLIENT_AUTH_TYPE == "federated-jwt"

if not KEYCLOAK_CLIENT_REGISTRATION_ENABLED:
    print(
        f"Client registration (KEYCLOAK_CLIENT_REGISTRATION_ENABLED=false) disabled. Skipping registration of {client_id}."
    )
    sys.exit(0)

if USE_DYNAMIC_SVID_REGISTRATION and not SPIRE_ENABLED:
    print("KEYCLOAK_REGISTRATION_MODE=dynamic-svid requires SPIRE_ENABLED=true.")
    sys.exit(1)

try:
    secret_file_path = get_env_var("SECRET_FILE_PATH")
except ValueError:
    secret_file_path = "/shared/client-secret.txt"

# Build client payload based on authentication type (shared admin + DCR)
client_payload: dict[str, Any] = {
    "name": client_name,
    "clientId": client_id,
    "standardFlowEnabled": True,
    "directAccessGrantsEnabled": True,
    "serviceAccountsEnabled": True,  # Required for client_credentials grant
    "fullScopeAllowed": False,
    "publicClient": False,  # Enable client authentication
    "attributes": {
        "standard.token.exchange.enabled": str(
            KEYCLOAK_TOKEN_EXCHANGE_ENABLED
        ).lower(),
    },
}

if CLIENT_AUTH_TYPE == "federated-jwt":
    print("Configuring client for JWT-SVID authentication (federated-jwt)")
    client_payload["clientAuthenticatorType"] = "federated-jwt"
    spiffe_idp_alias = get_env_var("SPIFFE_IDP_ALIAS", "spire-spiffe")
    client_payload["attributes"].update(
        {
            "jwt.credential.issuer": spiffe_idp_alias,
            "jwt.credential.sub": client_id,
        }
    )
else:
    print("Configuring client for client-secret authentication")
    client_payload["clientAuthenticatorType"] = "client-secret"

if USE_DYNAMIC_SVID_REGISTRATION:
    print(
        "Using dynamic client registration (JWT-SVID bearer) — Keycloak admin API credentials are not used."
    )
    # Optional: use a Keycloak initial access token instead of the raw SVID (still not admin password).
    iat = os.environ.get("KEYCLOAK_REGISTRATION_INITIAL_ACCESS_TOKEN", "").strip()
    bearer = iat if iat else read_jwt_svid_file()

    internal_client_id, reg_response = register_client_dynamic_svid(
        KEYCLOAK_URL,
        KEYCLOAK_REALM,
        bearer,
        client_id,
        client_payload,
    )

    secret_val = ""
    if reg_response and isinstance(reg_response.get("secret"), str):
        secret_val = reg_response["secret"]
    elif reg_response and reg_response.get("credentials"):
        creds = reg_response["credentials"]
        if isinstance(creds, list) and creds and isinstance(creds[0], dict):
            secret_val = str(creds[0].get("value") or "")

    if CLIENT_AUTH_TYPE == "federated-jwt" and not secret_val:
        print("No client secret in registration response (expected for federated-jwt). Writing empty secret file.")
    write_secret_file(secret_file_path, secret_val)

    # Audience scope management requires Admin API — skip in dynamic-svid mode.
    aud_env = os.environ.get("KEYCLOAK_AUDIENCE_SCOPE_ENABLED", "false").lower()
    if aud_env == "true":
        print(
            "Warning: KEYCLOAK_AUDIENCE_SCOPE_ENABLED=true is not supported with dynamic-svid "
            "registration (requires admin API). Skipping audience scope setup."
        )
    print("Client registration complete.")
    sys.exit(0)

# --- Admin API path (legacy) ---
keycloak_admin = KeycloakAdmin(
    server_url=KEYCLOAK_URL,
    username=get_env_var("KEYCLOAK_ADMIN_USERNAME"),
    password=get_env_var("KEYCLOAK_ADMIN_PASSWORD"),
    realm_name=KEYCLOAK_REALM,
    user_realm_name="master",
)

internal_client_id = register_client(
    keycloak_admin,
    client_id,
    client_payload,
)

print(
    f'Writing secret for client ID: "{client_id}" (internal client ID: "{internal_client_id}") to file: "{secret_file_path}"'
)
write_client_secret(
    keycloak_admin,
    internal_client_id,
    client_name,
    secret_file_path=secret_file_path,
)

# --- Audience scope management ---
AUDIENCE_SCOPE_ENABLED = (
    get_env_var("KEYCLOAK_AUDIENCE_SCOPE_ENABLED", "true").lower() == "true"
)

if AUDIENCE_SCOPE_ENABLED:
    scope_name = "agent-" + client_name.replace("/", "-") + "-aud"

    print(f'\n--- Audience scope management for "{scope_name}" ---')

    scope_id = get_or_create_audience_scope(keycloak_admin, scope_name, client_id)

    if scope_id:
        try:
            keycloak_admin.add_default_default_client_scope(scope_id)
            print(f'Added "{scope_name}" as realm default scope.')
        except Exception as e:
            print(f'Note: Could not add "{scope_name}" as realm default (might already exist): {e}')

        platform_clients_raw = get_env_var("PLATFORM_CLIENT_IDS", "kagenti")
        platform_client_ids = [
            c.strip() for c in platform_clients_raw.split(",") if c.strip()
        ]
        if platform_client_ids:
            print(f"Adding scope to platform clients: {platform_client_ids}")
            add_scope_to_platform_clients(
                keycloak_admin, scope_id, scope_name, platform_client_ids
            )
    else:
        print(
            f'Warning: Could not create audience scope "{scope_name}". '
            f"Platform clients will not automatically include this agent's audience."
        )

print("Client registration complete.")
