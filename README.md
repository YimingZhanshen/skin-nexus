<div align="center">

# SkinNexus / 肤枢

[中文](README.zh.md)

<img src="docs/assets/logo.svg" width="140" alt="SkinNexus pixel hub N mark">

> A self-hosted Minecraft skin server. Accounts, texture files and the signing key stay on your own machine.

[![CI](https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml/badge.svg)](https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml) [![release](https://img.shields.io/badge/release-v0.6.0-blue)](https://github.com/YimingZhanshen/skin-nexus/releases/tag/v0.6.0) [![license](https://img.shields.io/badge/license-AGPL--3.0-green)](LICENSE)

[Deploy](#deploy) · [Features](#features) · [Docs](https://docs.skin.yimingzhanshen.cn/) · [Contributing](CONTRIBUTING.md) · [License](#license)

</div>

---

One Go binary + Vue 3, on MySQL / MariaDB / PostgreSQL / SQLite. Players upload and manage skins and capes in the browser and join servers through launcher passwordless login — no Mojang account required.

Built as a **blessing-skin-server alternative**: the protocol layer stays compatible with [blessing-skin-server](https://github.com/bs-community/blessing-skin-server) and its plugin ecosystem, so a legacy site migrates losslessly — primary keys preserved, player UUIDs unchanged, old password hashes still verifiable. A real production site has been migrated and cut over: 67 users / 61 textures, zero rows lost.

**→ Everything else lives at [docs.skin.yimingzhanshen.cn](https://docs.skin.yimingzhanshen.cn/)** — deploy, migration, plugin/theme specs, compatibility matrix, launcher API. This README is the doorway, the docs site is the house.

Current release v0.6.0 (2026-11-12); `main` holds the completed next cycle and tagging is the operator's call. Iteration history: [`docs/CHANGELOG.md`](docs/CHANGELOG.md).

## Deploy

Images are on GHCR (public, no `docker login` needed). No source checkout required:

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

Open `http://<host>:8080/setup` and fill in the site name, admin account, skin domains and the `SKIN_SETUP_TOKEN` above — on an empty database whoever submits that form becomes the super admin, so the step asks for a value only the operator has. Upgrades are `docker compose pull && docker compose up -d`; data volumes are never touched.

Beyond the quickstart: external databases, bare binary, reverse proxy, backup — [deploy docs](https://docs.skin.yimingzhanshen.cn/deploy). Migrating from blessing-skin-server — [migration guide](https://docs.skin.yimingzhanshen.cn/migration) (`preflight → dry-run → import`, rollback available).

Two things before going live:

1. The reverse proxy must forward `X-Forwarded-Proto` and `X-Forwarded-For` — otherwise scheme detection breaks and the whole site shares one rate-limit bucket.
2. Back up the signing key (`skin-data` volume, `/data/sign-key.pem`). Losing it invalidates the signature of every texture already served.

## Features

- **For players** — skin & cape uploads (drag-and-drop, 3D preview, HD skins keep native resolution), provenance (original / derivative / repost), private uploads, closet & character management, shareable skin-library detail pages, reports with an in-site inbox, OAuth app and device management.
- **For operators** — admin panel (users / characters / textures / invite codes / runtime status / self-diagnostics), announcements, custom CSS/JS, theme packs and a theme market (Ed25519-verified, hot install/uninstall), bans at three layers, auth rate limiting with lockout, SMTP password reset + email verification, audit trail. Sessions and launcher tokens are stored as digests — reading the database is not holding a usable login.
- **Protocols** — full Yggdrasil endpoint set (authlib-injector works out of the box), built-in OIDC passwordless login (device code flow, no sidecar), MUA Union member-site module (off by default = zero surface), CustomSkinAPI / USM / legacy routes, Mojang premium fallback, server-rendered avatars & previews, launcher Bearer API, skin import (Mojang / legacy site, single and batch).
- **Content & extensions** — zh-CN / en, zero-build theme packs (`theme.json` + CSS), signed plugin platform (goja host: config, namespaced data, route tiers, events, guards, iframe UI), SEO (server-rendered pages, sitemap, robots, llms.txt, per-page meta + JSON-LD).

Measured on a 2-core / 3.5 GB production VPS: landing ~600 RPS, texture delivery 5182 RPS, Yggdrasil metadata 2365 RPS, zero errors.

## Why this one

Server-rendered SEO, shareable texture detail URLs, a closed texture lifecycle, and an error contract enforced by the build — no comparison project has all four. Per-cell source evidence (`file:line`, snapshot 2026-11-01): [`docs/competitor-analysis.md`](https://docs.skin.yimingzhanshen.cn/competitor-analysis).

## Documentation

| I want to… | Go to |
|---|---|
| Deploy, env vars, reverse proxy, backup | [docs.skin.yimingzhanshen.cn/deploy](https://docs.skin.yimingzhanshen.cn/deploy) |
| Migrate from blessing-skin-server | [docs.skin.yimingzhanshen.cn/migration](https://docs.skin.yimingzhanshen.cn/migration) |
| Build from source / understand CI | [docs.skin.yimingzhanshen.cn/build](https://docs.skin.yimingzhanshen.cn/build) |
| Check protocol compatibility | [docs.skin.yimingzhanshen.cn/compat-matrix](https://docs.skin.yimingzhanshen.cn/compat-matrix) |
| Write a plugin / theme | [plugin-spec](https://docs.skin.yimingzhanshen.cn/plugin-spec) · [theme-spec](https://docs.skin.yimingzhanshen.cn/theme-spec) |
| Publish & sign extensions | [docs.skin.yimingzhanshen.cn/plugin-publishing](https://docs.skin.yimingzhanshen.cn/plugin-publishing) |
| Integrate a launcher | [docs.skin.yimingzhanshen.cn/launcher-api](https://docs.skin.yimingzhanshen.cn/launcher-api) |
| Browse the knowledge handbook | [docs.skin.yimingzhanshen.cn/handbook](https://docs.skin.yimingzhanshen.cn/handbook/) |

Repo-level docs: [`docs/deploy.md`](docs/deploy.md) · [`docs/build.md`](docs/build.md) · [`docs/migration.md`](docs/migration.md) · [`docs/CHANGELOG.md`](docs/CHANGELOG.md). Most detailed documentation is Chinese-first.

## Frozen compatibility contracts

Before changing any of these, read the iron rules in [`AGENTS.md`](AGENTS.md) and run the matching contract tests:

1. The original semantics of `/api/yggdrasil/**` (204 lists, `{error,errorMessage}` bodies, millisecond timestamps, strict PEM)
2. Zero player-UUID drift (the `OfflinePlayer:` md5 variant; renaming never recomputes)
3. Persisted data stays isomorphic to blessing-skin-server storage conventions and directly migratable
4. The `internal/union` wire contract and the `internal/plugin` hook contract are frozen

## License

AGPL-3.0 (see [`LICENSE`](LICENSE)). `Copyright (c) 2026 YMZS (YimingZhanshen) and SkinNexus contributors`.
Translations, theme packs, code and documentation contributions are welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md). Governance is BDFL and changes go through PRs.
