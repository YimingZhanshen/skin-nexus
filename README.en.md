# SkinNexus / 肤枢

<p align="center"><img src="docs/assets/logo.svg" width="140" alt="SkinNexus pixel hub N mark"></p>

<p align="center">
  <a href="https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml"><img src="https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/YimingZhanshen/skin-nexus/releases/tag/v0.5.0"><img src="https://img.shields.io/badge/release-v0.5.0-blue" alt="v0.5.0"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-green" alt="AGPL-3.0"></a>
</p>

A self-hosted Minecraft skin station. Players upload and manage skins and capes in the browser and join servers through launcher passwordless login; the operator runs it themselves — accounts, texture files and the signing key stay on their own machine.

One Go binary + Vue 3, on MySQL, MariaDB, PostgreSQL or SQLite — AGPL-3.0. The protocol layer is compatible with [blessing-skin-server](https://github.com/bs-community/blessing-skin-server) and its plugin ecosystem, so a legacy site can be migrated losslessly: primary keys preserved, player UUIDs unchanged, old password hashes still verifiable. A real production site has been migrated and cut over — 67 users / 61 textures, zero rows lost.

Current release v0.5.0 (2026-11-01); `main` keeps moving past it, one slice per commit.
Iteration history lives in [`docs/CHANGELOG.md`](docs/CHANGELOG.md) (Chinese).

## Deploy

Images are on GHCR (public, no `docker login` needed). This does not require cloning the source:

```bash
mkdir skin-nexus && cd skin-nexus
curl -fsSLO https://raw.githubusercontent.com/YimingZhanshen/skin-nexus/main/docker-compose.quickstart.yml
cat > .env <<EOF
SKIN_BASE_URL=https://skin.example.com
SKIN_DB_PASSWORD=$(openssl rand -hex 16)
SKIN_DB_ROOT_PASSWORD=$(openssl rand -hex 16)
SKIN_ADMIN_TOKEN=$(openssl rand -hex 32)
SKIN_SETUP_TOKEN=$(openssl rand -hex 16)
EOF
docker compose -f docker-compose.quickstart.yml up -d
```

Open `http://<host>:8080/setup` and fill in the site name, admin account, skin domains and the
`SKIN_SETUP_TOKEN` above — on an empty database whoever submits that form becomes the super admin, so
the step asks for a value only the operator has. Leave it unset and the site mints a fresh one on every
start and prints it on the `docker compose logs skin-nexus` install-token line. Upgrades are
`docker compose pull && docker compose up -d`; data volumes are never touched.

An external database (MySQL / MariaDB / PostgreSQL), a single-file SQLite database, a bare binary or a source build: [`docs/deploy.md`](docs/deploy.md) and [`docs/build.md`](docs/build.md).
Migrating from a legacy site: [`docs/migration.md`](docs/migration.md) (`preflight → dry-run → import`, rollback available).

Two things are required before going live:

1. The reverse proxy must forward `X-Forwarded-Proto` and `X-Forwarded-For`, otherwise scheme detection breaks and the whole site shares one rate-limit bucket.
2. Back up the signing key (in the `skin-data` volume at `/data/sign-key.pem`). Losing it invalidates the signatures of every texture already served.

## Features

**Players**: registration and login (invite-code gating optional), skin and cape uploads (drag-and-drop, progress, 3D preview, HD skins keep native resolution), texture name and provenance (original / derivative / repost, the last two require the upstream URL), Markdown descriptions, private uploads, closet and character management, set-as-avatar, skin library with filters and a shareable detail page, likes that add to the closet, reports, an in-site inbox for moderation and account decisions, OAuth app and device management, account deletion.

**Operators**: admin panel (overview, users / characters / textures, invite codes, site settings, runtime status, self-diagnostics), announcements and custom CSS/JS, theme packs and a theme market (Ed25519-verified, install and uninstall take effect without a restart), local theme uploads, configurable footer, bans enforced at three layers (login / authenticate / `hasJoined`), authentication rate limiting with lockout, SMTP password reset and email verification (single-use link token plus a configurable grace period before the join gate), an in-site notification centre (report outcomes, texture takedowns and lifted verdicts, bans and un-bans), login and server communication logs (kept for 90 days; a super admin can open a temporary diagnostic window of up to 24 hours, which writes one extra detail row for the same request — status code, duration, allow-listed request headers, a body excerpt and the token itself), audit trail. Sessions and launcher tokens are stored in the database as digests only, so reading the database is not the same as holding a usable login; the token text inside a diagnostic detail row is the one exception to that rule, and it is wiped on its own fixed 24-hour clock rather than the 90-day log retention.

**Protocols and integration**: full Yggdrasil endpoint set (authlib-injector works without configuration changes), built-in OIDC passwordless login (device code flow + refresh rotation, no Janus sidecar), MUA Union member-site module (three-header signature verification inbound, player lifecycle sync, cross-site binding and UUID repair, peer site names and a security-level badge, self-diagnosis split by the two legs — our own outbound TLS, certificate and clock offset versus what the hub reported when probing us; off by default = zero surface), CustomSkinAPI / USM / legacy raw PNG / public read-only API, Mojang premium fallback (origin query when local lookup misses, with outbound address protection and a hot toggle in the panel), server-side avatar and preview rendering, a launcher Bearer API (`launcher-api` plugin), skin import (`skin-importer` plugin, single and batch from Mojang or a legacy site).

**Content and extensions**: zh-CN / en (language packs are extensible), theme pack system (no build step: `theme.json` + CSS + optional JS), signed plugin platform (goja host: config/secrets, namespaced data, three route tiers, events and guards, iframe UI), SEO (server-rendered landing and texture detail pages, `sitemap.xml`, `robots.txt`, `llms.txt`, per-page meta/OG and JSON-LD).

## Screenshots

<p align="center">
  <img src="docs/assets/showcase/landing.jpg" alt="Landing page" width="100%">
</p>

<p align="center">
  <img src="docs/assets/showcase/skinlib.jpg" alt="Skin library" width="49%">
  <img src="docs/assets/showcase/market.jpg" alt="Plugin market" width="49%">
</p>

> Screenshots come from a real production instance; site name and sensitive configuration are replaced, skins and nicknames come from the public library.

## How it differs

Server-rendered SEO, shareable texture detail URLs, a closed texture lifecycle, and an error contract enforced by the build — none of the four comparison projects has all four. Per-cell evidence (`file:line` in their repositories, snapshot 2026-11-01) is in [`docs/competitor-analysis.md`](docs/competitor-analysis.md); this is only the summary:

<details>
<summary><b>Against blessing-skin-server / element-skin / vSkin</b></summary>

| Capability | SkinNexus | blessing | element-skin | vSkin |
|---|---|---|---|---|
| SEO (sitemap / robots / per-page meta·OG·JSON-LD) | ✅ | ⚠️ title only | ❌ | ❌ |
| Server-rendered content pages (indexable without JS) | ✅ landing + detail | ⚠️ Twig shell | ❌ SPA | ❌ SPA |
| Skin library detail page (shareable URL) | ✅ | ✅ | ❌ modal | ❌ |
| OIDC passwordless login | ✅ built in | ⚠️ needs Janus | ⚠️ discovery under-announces | ⚠️ weak details |
| Texture lifecycle (pixels decide model / violations block re-publish / audited delete with evidence) | ✅ | ⚠️ report delete skips the reference count | ❌ files stay forever | ❌ delete does not count |
| Error contract (stable code + build gate) | ✅ | ❌ coarse int codes | ⚠️ codes, no gate | ❌ prose only |
| CI gates (coverage / multi-engine integration matrix) | ⚠️ floor 45% + MySQL/MariaDB/PG/SQLite, six combinations | ⚠️ Codecov, no threshold | ✅ hard 90% | ❌ no CI |
| Deployment shape | Go binary / Docker | PHP 8.1 (EOL) + Laravel 10 | Go + PostgreSQL + Redis | FastAPI + SQLite |

</details>

Measured on one 2-core / 3.5 GB production VPS: landing ~600 RPS, texture delivery 5182 RPS, Yggdrasil metadata 2365 RPS, zero errors.

## Documentation

- Deployment and operations [`docs/deploy.md`](docs/deploy.md) · build and CI [`docs/build.md`](docs/build.md) · migration [`docs/migration.md`](docs/migration.md)
- Compatibility baseline [`docs/compat-matrix.md`](docs/compat-matrix.md) · launcher API [`docs/launcher-api.md`](docs/launcher-api.md)
- Theme packs [`docs/theme-spec.md`](docs/theme-spec.md) · plugins [`docs/plugin-spec.md`](docs/plugin-spec.md) · publishing and signing [`docs/plugin-publishing.md`](docs/plugin-publishing.md)
- Competitor evidence [`docs/competitor-analysis.md`](docs/competitor-analysis.md) · iteration history [`docs/CHANGELOG.md`](docs/CHANGELOG.md)
- Online wiki (mirror of the docs generated in this repository, with architecture and protocol deep dives) [YimingZhanshen/skin-nexus/wiki](https://github.com/YimingZhanshen/skin-nexus/wiki)
- Contributing [`CONTRIBUTING.md`](CONTRIBUTING.md) · governance [`GOVERNANCE.md`](GOVERNANCE.md) · vulnerability reporting [`SECURITY.md`](SECURITY.md) · code of conduct [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) · accessibility [`ACCESSIBILITY.md`](ACCESSIBILITY.md) · repository collaboration notes [`AGENTS.md`](AGENTS.md)

Most detailed documentation is still Chinese-first.

## Frozen compatibility contracts

Before changing any of these, read the iron rules in [`AGENTS.md`](AGENTS.md) and run the matching contract tests:

1. The original semantics of `/api/yggdrasil/**` (204 list, `{error,errorMessage}` bodies, millisecond timestamps, strict PEM format)
2. Zero player-UUID drift (the `OfflinePlayer:` md5 variant; renaming never recomputes)
3. Persisted data stays isomorphic to blessing-skin-server storage conventions and directly migratable
4. The `internal/union` wire contract and the `internal/plugin` hook contract are frozen

## License

AGPL-3.0 (see [`LICENSE`](LICENSE)). `Copyright (c) 2026 YMZS (YimingZhanshen) and SkinNexus contributors`.
Translations, theme packs, code and documentation contributions are welcome; governance is BDFL and changes go through PRs.
