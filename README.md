<div align="center">

# SkinNexus / 肤枢

[中文](README.zh.md)

<img src="docs/assets/logo.svg" width="140" alt="SkinNexus pixel hub N mark">

> A website program for managing Minecraft character looks — self-hosted, so accounts, skins and passwords all stay on your own machine.

[![CI](https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml/badge.svg)](https://github.com/YimingZhanshen/skin-nexus/actions/workflows/ci.yml) [![release](https://img.shields.io/badge/release-v0.6.0-blue)](https://github.com/YimingZhanshen/skin-nexus/releases/tag/v0.6.0) [![license](https://img.shields.io/badge/license-AGPL--3.0-green)](LICENSE)

[What is this](#what-is-this) · [Why switch](#why-switch) · [Deploy](#deploy) · [Features](#features) · [Docs](https://docs.skin.yimingzhanshen.cn/) · [License](#license)

</div>

---

<p align="center">
  <img src="https://img.shields.io/badge/-move_from_bs--lose_nothing-2ea44f?style=for-the-badge" alt="Move from bs: lose nothing">
  <img src="https://img.shields.io/badge/-security_fixes--ship_same_day-b0453a?style=for-the-badge" alt="Security fixes ship the same day">
  <img src="https://img.shields.io/badge/-launcher_login--no_password-1d76db?style=for-the-badge" alt="Launcher login without passwords">
  <img src="https://img.shields.io/badge/-easy_deploy--safe_upgrades-6f42c1?style=for-the-badge" alt="Easy deploys, safe upgrades">
</p>

## What is this

**SkinNexus is a website where Minecraft players manage their character looks.** Players upload skins and capes in the browser, dress up their characters, browse a shared skin library, then join game servers through their launcher — no password typing. The site runs on your own server, so player accounts, skins and passwords all live on your own machine.

**blessing-skin-server (nicknamed "bs" in the community) is the oldest program of this kind.** The skin site you play on is very likely built with it. It has the biggest ecosystem and the most tutorials, but its foundation is ten-year-old PHP — when a security hole appears, users often wait forever for an official fix, and features have barely moved for years.

**SkinNexus is a modern replacement, built for people coming from the old site.** The most important thing: moving over loses **nothing** — accounts, skins and capes, in-game identities and old passwords all carry over, and players will barely notice the switch. After the move, here is what actually changes.

## Why switch

| Pain you may know | Old site (blessing-skin-server) | SkinNexus |
|---|---|---|
| Aging tech, nobody fixes holes | Ten-year-old PHP, waiting on upstream | Modern rewrite — we fix and ship ourselves |
| Fear of losing data on migration | — | Accounts / skins / identities / old passwords preserved |
| Launcher passwordless login is a chore | Needs an extra login add-on | Built in — works right after setup |
| One bad skin hurts everyone | Delete-and-pray, closets break too | Reference count first, evidence kept, then delete |
| Players can't find your site | Pages unreadable to search engines | Readable by search engines and AI assistants |
| Deploy and maintenance are heavy | A pile of PHP plumbing | Two commands to run, upgrades never touch data |

Measured on a small 2-core / 3.5 GB server: skin images answered 5,182 requests per second, the landing page 600 per second, zero errors. Per-cell comparison evidence: [competitor analysis](https://docs.skin.yimingzhanshen.cn/competitor-analysis).

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

Open `http://<host>:8080/setup` and fill in the site name, admin account, skin domains and the `SKIN_SETUP_TOKEN` above — on an empty database whoever submits that form becomes the super admin, so the step asks for a value only you have. Upgrades are `docker compose pull && docker compose up -d`; data is never touched.

Two things before going live:

1. If you use a reverse proxy (Nginx, BaoTa, …), forward `X-Forwarded-Proto` and `X-Forwarded-For` (copy the config from the [deploy docs](https://docs.skin.yimingzhanshen.cn/deploy)) — otherwise address detection and login protection misbehave.
2. Back up the signing key (`/data/sign-key.pem` inside the `skin-data` volume). Lose it and every skin image players have downloaded fails verification.

More options (external databases, bare-metal, migrating from an old site) live at [docs.skin.yimingzhanshen.cn](https://docs.skin.yimingzhanshen.cn/deploy). Coming from blessing-skin-server? See the [migration guide](https://docs.skin.yimingzhanshen.cn/migration) — check first, rehearse, then switch, with rollback.

## Features

- **For players** — upload skins/capes in the browser (drag-and-drop, 3D preview), dress up characters, browse the skin library, keep favourites, report bad content, manage login devices.
- **For site owners** — manage players and content, publish announcements, restyle the site with one click (theme packs, live install/uninstall), ban troublemakers and block password guessing, password reset / email verification, a full activity trail. Player login credentials are stored as fingerprints only — stealing the database does not hand over usable logins.
- **For game servers** — works out of the box with authlib-injector launchers; passwordless login built in (no extra add-ons); join the MUA Union to interoperate with other skin sites; premium-account fallback, legacy formats, batch skin import.
- **Front door & extensions** — Chinese/English, build-free theming, plugin platform, search-engine and AI optimization (players can actually find you).

Under the hood: one Go binary + Vue 3, with a choice of MySQL / MariaDB / PostgreSQL / SQLite.

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

Current release v0.6.0; iteration history in [`docs/CHANGELOG.md`](docs/CHANGELOG.md). Most detailed documentation is Chinese-first.

## Frozen compatibility contracts (for developers)

Before changing any of these, read the iron rules in [`AGENTS.md`](AGENTS.md) and run the matching contract tests:

1. The original semantics of `/api/yggdrasil/**` (204 lists, `{error,errorMessage}` bodies, millisecond timestamps, strict PEM)
2. Zero player-UUID drift (the `OfflinePlayer:` md5 variant; renaming never recomputes)
3. Persisted data stays isomorphic to blessing-skin-server storage conventions and directly migratable
4. The `internal/union` wire contract and the `internal/plugin` hook contract are frozen

## License

AGPL-3.0 (see [`LICENSE`](LICENSE)). `Copyright (c) 2026 YMZS (YimingZhanshen) and SkinNexus contributors`.
Translations, theme packs, code and documentation contributions are welcome — see [`CONTRIBUTING.md`](CONTRIBUTING.md). Governance is BDFL and changes go through PRs.
