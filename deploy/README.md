# Sub2API Deployment Files

This fork supports exactly one deployment method: **build from source and run under
systemd**. See the "Build from Source" section in the top-level [README.md](../README.md)
for the build steps. This directory holds the files that support running the resulting
binary in production, plus a few guides that apply regardless of deployment method.

## Files

| File | Description |
|------|-------------|
| `config.example.yaml` | Example configuration file — copy to `backend/config.yaml` |
| `sub2api.service` | systemd unit file for the main app |
| `sub2api-datamanagementd.service` | systemd unit file for the optional `datamanagementd` helper |
| `install-datamanagementd.sh` | Installer for `datamanagementd`, works against a source-built binary (`--source`) |
| `DATAMANAGEMENTD_CN.md` | datamanagementd 部署与联动说明（中文） |
| `EDGE_SECURITY.md` | Reverse proxy, CDN/WAF, trusted proxy, and ingress hardening guide |
| `Caddyfile` | Example reverse-proxy config (paired with `test-caddyfile-cache.sh`) |
| `codex-instructions.md.tmpl` | Template consumed by `gateway.forced_codex_instructions_template_file` |
| `Makefile` | `go build`/`go test` targets used in CI and local development |

---

## Running the built binary under systemd

1. Build per the top-level README ("Build from Source"), producing `sub2api` and copying
   `config.example.yaml` to your config path.
2. Copy `sub2api.service` to `/etc/systemd/system/`, edit `ExecStart`/`WorkingDirectory`/
   `Environment=` lines to match where you placed the binary and config, then:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now sub2api
   ```
3. Open the Setup Wizard in your browser to complete first-run configuration.

### Service management

```bash
sudo systemctl start sub2api
sudo systemctl stop sub2api
sudo systemctl restart sub2api
sudo systemctl status sub2api
sudo journalctl -u sub2api -f
```

### Updating

Updates are git-based only — see [`FORK_MAINTENANCE.md`](../FORK_MAINTENANCE.md) at the
repo root for the pull → rebuild → restart procedure and the safety checklist to re-run
after every upstream sync. This fork does not download or run prebuilt binaries.

### Server address and port

During installation you'll configure the listen address/port; these are stored as
environment variables in the systemd unit. To change them afterward:

```bash
sudo systemctl edit sub2api
```

```ini
[Service]
Environment=SERVER_HOST=0.0.0.0
Environment=SERVER_PORT=3000
```

```bash
sudo systemctl daemon-reload
sudo systemctl restart sub2api
```

### Prerequisites

- Linux server (Ubuntu 20.04+, Debian 11+, CentOS 8+, etc.)
- PostgreSQL 15+
- Redis 7+
- systemd
- Go 1.21+ and Node.js 18+ (build-time only)

### Directory structure

```
/opt/sub2api/
├── sub2api              # Binary you built
└── data/                # Runtime data

/etc/sub2api/
└── config.yaml          # Configuration file
```

### datamanagementd（数据管理）联动

如需启用管理后台"数据管理"功能，请额外部署宿主机 `datamanagementd`：

- 主进程固定探测 `/tmp/sub2api-datamanagement.sock`
- 详细步骤见：`deploy/DATAMANAGEMENTD_CN.md`

---

## Startup and database recovery

Sub2API applies database migrations during application startup. PostgreSQL can remain in
its recovery/startup phase briefly after a host restart. The application retries transient
PostgreSQL startup and connection errors with bounded exponential backoff, then starts
automatically once the database becomes ready. Authentication errors, migration checksum
mismatches, SQL errors, and other permanent configuration/data errors fail immediately.

Keep `Restart=always` and `RestartSec` configured in `sub2api.service`; the application
retry covers transient database startup, while systemd remains the supervisor for
permanent process exits.

## Database migration notes (PostgreSQL)

- Migrations are applied in lexicographic order (e.g. `001_...sql`, `002_...sql`).
- `schema_migrations` tracks applied migrations (filename + checksum).
- Migrations are forward-only; rollback requires a DB backup restore or a manual
  compensating SQL script.

**Verify `users.allowed_groups` → `user_allowed_groups` backfill**

During the incremental GORM→Ent migration, `users.allowed_groups` (legacy `BIGINT[]`) is
being replaced by a normalized join table `user_allowed_groups(user_id, group_id)`.

Run this query to compare the legacy data vs the join table:

```sql
WITH old_pairs AS (
  SELECT DISTINCT u.id AS user_id, x.group_id
  FROM users u
  CROSS JOIN LATERAL unnest(u.allowed_groups) AS x(group_id)
  WHERE u.allowed_groups IS NOT NULL
)
SELECT
  (SELECT COUNT(*) FROM old_pairs)           AS old_pair_count,
  (SELECT COUNT(*) FROM user_allowed_groups) AS new_pair_count;
```

---

## Troubleshooting

```bash
# Check service status
sudo systemctl status sub2api

# View recent logs
sudo journalctl -u sub2api -n 50

# Check config file
sudo cat /etc/sub2api/config.yaml

# Check PostgreSQL / Redis
sudo systemctl status postgresql
sudo systemctl status redis
```

**Common issues**
1. **Port already in use**: change `SERVER_PORT` in the systemd unit.
2. **Database connection failed**: check PostgreSQL is running and credentials are correct.
3. **Redis connection failed**: check Redis is running and password is correct.
4. **Permission denied**: ensure proper file ownership for the binary and config paths.

---

## Gemini OAuth configuration

Sub2API supports three methods to connect to Gemini:

### Method 1: Code Assist OAuth (recommended for GCP users)

**No configuration needed** — always uses the built-in Gemini CLI OAuth client (public).

1. Leave `GEMINI_OAUTH_CLIENT_ID` and `GEMINI_OAUTH_CLIENT_SECRET` empty
2. In the Admin UI, create a Gemini OAuth account and select **"Code Assist"** type
3. Complete the OAuth flow in your browser

> Note: Even if you configure `GEMINI_OAUTH_CLIENT_ID` / `GEMINI_OAUTH_CLIENT_SECRET` for AI Studio OAuth,
> Code Assist OAuth will still use the built-in Gemini CLI client.

**Requirements:**
- Google account with access to Google Cloud Platform
- A GCP project (auto-detected or manually specified)

**How to get Project ID (if auto-detection fails):**
1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Click the project dropdown at the top of the page
3. Copy the Project ID (not the project name) from the list
4. Common formats: `my-project-123456` or `cloud-ai-companion-xxxxx`

### Method 2: AI Studio OAuth (for regular Google accounts)

Requires your own OAuth client credentials.

**Step 1: Create OAuth Client in Google Cloud Console**

1. Go to [Google Cloud Console - Credentials](https://console.cloud.google.com/apis/credentials)
2. Create a new project or select an existing one
3. **Enable the Generative Language API:**
   - Go to "APIs & Services" → "Library"
   - Search for "Generative Language API"
   - Click "Enable"
4. **Configure OAuth Consent Screen** (if not done):
   - Go to "APIs & Services" → "OAuth consent screen"
   - Choose "External" user type
   - Fill in app name, user support email, developer contact
   - Add scopes: `https://www.googleapis.com/auth/generative-language.retriever` (and optionally `https://www.googleapis.com/auth/cloud-platform`)
   - Add test users (your Google account email)
5. **Create OAuth 2.0 credentials:**
   - Go to "APIs & Services" → "Credentials"
   - Click "Create Credentials" → "OAuth client ID"
   - Application type: **Web application** (or **Desktop app**)
   - Name: e.g., "Sub2API Gemini"
   - Authorized redirect URIs: Add `http://localhost:1455/auth/callback`
6. Copy the **Client ID** and **Client Secret**
7. **⚠️ Publish to Production (IMPORTANT):**
   - Go to "APIs & Services" → "OAuth consent screen"
   - Click "PUBLISH APP" to move from Testing to Production
   - **Testing mode limitations:**
     - Only manually added test users can authenticate (max 100 users)
     - Refresh tokens expire after 7 days
     - Users must be re-added periodically
   - **Production mode:** Any Google user can authenticate, tokens don't expire
   - Note: For sensitive scopes, Google may require verification (demo video, privacy policy)

**Step 2: Configure Environment Variables**

```bash
GEMINI_OAUTH_CLIENT_ID=your-client-id.apps.googleusercontent.com
GEMINI_OAUTH_CLIENT_SECRET=GOCSPX-your-client-secret

# 可选：如需使用 Gemini CLI 内置 OAuth Client（Code Assist / Google One）
# 安全说明：本仓库不会内置该 client_secret，请在运行环境通过环境变量注入。
# GEMINI_CLI_OAUTH_CLIENT_SECRET=GOCSPX-your-built-in-secret
```

**Step 3: Create Account in Admin UI**

1. Create a Gemini OAuth account and select **"AI Studio"** type
2. Complete the OAuth flow
   - After consent, your browser will be redirected to `http://localhost:1455/auth/callback?code=...&state=...`
   - Copy the full callback URL (recommended) or just the `code` and paste it back into the Admin UI

### Method 3: API Key (simplest)

1. Go to [Google AI Studio](https://aistudio.google.com/app/apikey)
2. Click "Create API key"
3. In Admin UI, create a Gemini **API Key** account
4. Paste your API key (starts with `AIza...`)

### Comparison table

| Feature | Code Assist OAuth | AI Studio OAuth | API Key |
|---------|-------------------|-----------------|---------|
| Setup Complexity | Easy (no config) | Medium (OAuth client) | Easy |
| GCP Project Required | Yes | No | No |
| Custom OAuth Client | No (built-in) | Yes (required) | N/A |
| Rate Limits | GCP quota | Standard | Standard |
| Best For | GCP developers | Regular users needing OAuth | Quick testing |

---

## TLS Fingerprint Configuration

Sub2API supports TLS fingerprint simulation to make requests appear as if they come from the official Claude CLI (Node.js client).

> **💡 Tip:** Visit **[tls.sub2api.org](https://tls.sub2api.org/)** to get TLS fingerprint information for different devices and browsers.

### Default Behavior

- Built-in `claude_cli_v2` profile simulates Node.js 20.x + OpenSSL 3.x
- JA3 Hash: `1a28e69016765d92e3b381168d68922c`
- JA4: `t13d5911h1_a33745022dd6_1f22a2ca17c4`
- Profile selection: `accountID % profileCount`

### Configuration

```yaml
gateway:
  tls_fingerprint:
    enabled: true  # Global switch
    profiles:
      # Simple profile (uses default cipher suites)
      profile_1:
        name: "Profile 1"

      # Profile with custom cipher suites (use compact array format)
      profile_2:
        name: "Profile 2"
        cipher_suites: [4866, 4867, 4865, 49199, 49195, 49200, 49196]
        curves: [29, 23, 24]
        point_formats: 0

      # Another custom profile
      profile_3:
        name: "Profile 3"
        cipher_suites: [4865, 4866, 4867, 49199, 49200]
        curves: [29, 23, 24, 25]
```

### Profile Fields

| Field | Type | Description |
|-------|------|--------------|
| `name` | string | Display name (required) |
| `cipher_suites` | []uint16 | Cipher suites in decimal. Empty = default |
| `curves` | []uint16 | Elliptic curves in decimal. Empty = default |
| `point_formats` | []uint8 | EC point formats. Empty = default |

### Common Values Reference

**Cipher Suites (TLS 1.3):** `4865` (AES_128_GCM), `4866` (AES_256_GCM), `4867` (CHACHA20)

**Cipher Suites (TLS 1.2):** `49195`, `49196`, `49199`, `49200` (ECDHE variants)

**Curves:** `29` (X25519), `23` (P-256), `24` (P-384), `25` (P-521)
