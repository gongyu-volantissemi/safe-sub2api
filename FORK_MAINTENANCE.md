# FORK_MAINTENANCE.md

This file is written for whoever (human or AI agent) next maintains this fork —
`gongyu-volantissemi/safe-sub2api`, forked from
[`Wei-Shaw/sub2api`](https://github.com/Wei-Shaw/sub2api). Read this before pulling
upstream changes or touching anything listed under "Safety-critical paths" below.

## 1. Why this fork exists

An audit of upstream `sub2api` (not just its README) found three concrete gaps:

1. **Upstream account credentials are stored in plaintext.** OAuth tokens, API keys, and
   session cookies for every connected provider account live in the `credentials` JSONB
   column on the `account` table (`backend/ent/schema/account.go`), completely
   unencrypted. `TOTP_ENCRYPTION_KEY` encrypts *other* secrets (TOTP 2FA secrets, S3
   access keys, plugin tokens) but never this table. **This fork does not and cannot fix
   this** — it's a data-model fact upstream, not a bug this fork patches. The only real
   mitigations are operational: keep Postgres/Redis bound to localhost or an internal
   network only, and treat admin-level access to this app as equivalent to having the raw
   credentials, since the admin API also returns them in plaintext.
2. **Shipped config defaults were permissive.** `security.url_allowlist.enabled` defaulted
   to `false`, and even when enabled, `allow_private_hosts` and `allow_insecure_http`
   defaulted to `true` — SSRF and plaintext-HTTP protection were opt-in on top of
   opt-in, with no startup warning for the latter two. **This fork does fix this** — see
   §2.
3. **A binary self-update/rollback feature existed with no step-up (2FA) gate.** It
   downloaded and atomically swapped the running binary from GitHub Releases. The
   download path itself was reasonably hardened (SHA-256 checksum against the release's
   own checksums file, HTTPS + host allowlist, zip-slip protection), but it's still an
   admin-triggerable code-execution path, and unlike other sensitive admin routes
   (accounts, proxy, backup), it wasn't gated behind step-up auth. **This fork removes it
   entirely** — see §2.

## 2. What we changed, precisely

| Area | Upstream | This fork | Why |
|---|---|---|---|
| Deployment methods | 4 methods: script install (downloads prebuilt binaries), Docker Compose, Apple container, build from source | Build from source only | No Docker, no downloaded binaries, ever. `deploy/install.sh`, all `docker-compose*.yml`, `deploy/apple-container.sh`, all `Dockerfile*`, `.goreleaser*.yaml`, `.github/workflows/release.yml`, `.github/release-tools/` deleted. |
| Self-update | `backend/internal/service/update_service.go` downloads/verifies/swaps the running binary from GitHub Releases; `POST /admin/system/update`, `/rollback`, `GET /rollback-versions` | Removed entirely. `UpdateService` only reports `CurrentVersion()` + `SecurityWarnings()`. Only `GET /admin/system/version` and `POST /admin/system/restart` remain. | Incompatible with build-from-source-only; there's no release pipeline to check against anyway. |
| Restart route auth | `POST /admin/system/restart` behind plain admin auth only | Behind `stepUpAuth` (2FA), matching accounts/proxy/backup routes | It was the one sensitive system-mutating admin route not already gated this way. |
| `security.url_allowlist.enabled` | Default `false` | Default `true` | SSRF/allowlist checks on by default. |
| `security.url_allowlist.allow_private_hosts` | Default `true` | Default `false` | Blocks requests to localhost/private IPs by default (SSRF). |
| `security.url_allowlist.allow_insecure_http` | Default `true` | Default `false` | Blocks plaintext HTTP upstream URLs by default. |
| `security.url_allowlist.upstream_hosts` | Fixed list of known providers, including several mainland-China-operated ones (Moonshot AI/Kimi, Zhipu AI/GLM, MiniMax) | Same list minus every mainland-China-operated provider (`api.kimi.com`, `api.moonshot.ai`, `api.moonshot.cn`, `open.bigmodel.cn`, `api.minimaxi.com`, `api.minimax.io`), plus `openrouter.ai` and `api.fireworks.ai` (both US-based) added for hybrid-mixing | This fork's operator does not want PRC-operated providers in the default outbound allowlist — their own privacy policies disclose PRC-based data storage/processing. The platform integrations themselves (Kimi/Zhipu/MiniMax/DeepSeek account types, routing, billing) are untouched in code; only the default allowlist changed. Any of these hosts, or any other custom/self-hosted provider, can still be added back explicitly in your own `config.yaml`. |
| Insecure-config visibility | Silent — no admin UI surfaces these flags at all | `slog.Warn` at startup for each insecure flag (`backend/internal/config/config.go`, `Load()`), plus a persistent, non-dismissible banner in the admin dashboard (`security_warnings` field on `GET /admin/system/version`, rendered by `VersionBadge.vue`) | An insecure config must never be silent. |

All three of this fork's changes are still ordinary config keys or deletions — nothing
here is a hard-coded restriction that can't be reasoned about. A self-hoster with a real
reason (e.g. LAN-only testing) can still set `allow_insecure_http: true` etc. explicitly;
they just can't do it by accident, and the UI won't let them forget it's on.

**Everything else is untouched.** All five of the product's core capabilities — OAuth/
session-based subscription-to-API conversion, hybrid upstream mixing (web accounts +
official API keys), composite-group routing with sticky sessions, account pooling with
rate limiting, and the TypeSafe/Jev content-moderation engine (`/v1/systemone`, a text
safety classifier — not a general routing/command-safety layer, despite how it's
sometimes described) — live entirely in the core Go services and were not touched.

## 3. Update procedure (pulling from upstream)

```bash
git remote add upstream https://github.com/Wei-Shaw/sub2api.git   # once, if not already added
git fetch upstream
git log --oneline HEAD..upstream/main -- \
  backend/internal/config/config.go \
  backend/internal/service/update_service.go \
  backend/internal/handler/admin/system_handler.go \
  backend/internal/server/routes/admin.go \
  deploy/ \
  Dockerfile Dockerfile.goreleaser backend/Dockerfile \
  .goreleaser.yaml .goreleaser.simple.yaml \
  .github/workflows/ \
  README.md README_CN.md README_JA.md
git merge upstream/main   # or rebase, per your usual workflow — resolve conflicts below
```

### Safety-critical paths to scrutinize on every merge

- **`backend/internal/config/config.go`** — the `security.url_allowlist.*`
  `viper.SetDefault(...)` block (search for `"security.url_allowlist.enabled"`). If
  upstream changes these defaults or adds new insecure-by-default flags, re-apply §2's
  posture (allowlist on, private-hosts off, insecure-http off) and extend the startup
  `slog.Warn` block to match.
- **Self-update.** If upstream reintroduces or changes `update_service.go`,
  `system_handler.go`'s `CheckUpdates`/`PerformUpdate`/`Rollback`/`GetRollbackVersions`, or
  their routes in `registerSystemRoutes` (`admin.go`) — re-delete them per §2. Also check
  `frontend/src/components/common/VersionBadge.vue`, `frontend/src/api/admin/system.ts`,
  and `frontend/src/stores/app.ts` for the corresponding frontend surface.
- **Deployment tooling.** Any new/changed `Dockerfile*`, `docker-compose*.yml`,
  `deploy/install.sh`, `.goreleaser*.yaml`, or `.github/workflows/release.yml` — re-delete
  per §2. Keep the README's `## Deployment` section single-method ("Build from Source"
  only); re-apply the same trim if upstream reintroduces other methods.
- **`deploy/config.example.yaml`** — keep its `security.url_allowlist` block's comments
  and defaults in sync with `config.go`'s.
- **Credential storage.** If upstream's `backend/ent/schema/account.go` or the admin
  account DTOs (`backend/internal/handler/dto/types.go`) change how `credentials` is
  stored or exposed, re-read §1.1 and update it here — this fork does not currently patch
  that, so the description just needs to stay accurate.

### Post-merge verification checklist

Run these after every merge, before pushing:

```bash
# Secure defaults are still in place
grep -n 'security.url_allowlist.enabled\|allow_private_hosts\|allow_insecure_http' \
  backend/internal/config/config.go
# expect: enabled -> true, allow_private_hosts -> false, allow_insecure_http -> false

# No Docker/binary-install tooling crept back in
find . -iname 'Dockerfile*' -o -iname 'docker-compose*.yml' -o -path '*/deploy/install.sh'
# expect: no output

# Self-update/rollback backend surface is still gone
grep -rn 'PerformUpdate\|RollbackToVersion\|ListRollbackVersions' backend/internal
# expect: no output (aside from this file's own prose, if grepped broadly)

# README documents exactly one deployment method
grep -c 'Method 1\|Method 2\|Method 3' README.md README_CN.md README_JA.md
# expect: 0 for each

# Everything still compiles and passes
cd backend && go build ./... && go vet -tags=unit ./... && go test -tags=unit ./...
cd ../frontend && pnpm install --frozen-lockfile && pnpm run build
```

### Re-verifying the underlying security posture

The checklist above verifies this fork's *own* changes survived the merge. It does **not**
verify that upstream hasn't changed something this fork's hardening doesn't cover. After
any large merge, spot-check:

- Is `credentials` on the `account` ent schema still plaintext JSONB? (§1.1 above assumes
  yes; if upstream ever encrypts it, update §1.1 to say so — that would be worth noting as
  a real upstream improvement, not something to fight.)
- Do the admin account DTOs still return raw `credentials` in API responses? If upstream
  starts masking/redacting them, same as above — update §1.1 to reflect it.

Don't assume §2's changes make credential storage safe on their own — they don't. DB/host
access control remains the operator's job, not something patchable in application code.
