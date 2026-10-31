# 项目治理（GOVERNANCE）

## 发起人与著作权

SkinNexus / 肤枢 由 **YMZS（[GitHub @YimingZhanshen](https://github.com/YimingZhanshen)）** 发起并享有原始著作权（参见 `LICENSE` 版权声明与 `CITATION.cff`）。本项目为 blessing-skin-server 的重写替代，采用 AGPL-3.0 开源。

## 决策模式（BDFL）

- **发起人是项目的最终决策者**：技术路线、Yggdrasil 协议兼容基线、数据迁移策略、发版节奏等重大决策由发起人拍板。
- 重大决策采用「多 AI 评析 + 人工综合」流程：产出自包含的评析提示词 → 多方独立评审 → 对齐分歧 → 发起人终裁。评审结论以精简形式沉淀于 `docs/`。
- 日常改动走 Pull Request；社团成员与外部贡献者一视同仁，通过 PR 向**本主仓**贡献，合并权归发起人。

## 仓库拓扑

- **本仓库（`YimingZhanshen/skin-nexus`）是唯一 canonical 仓库**：全部 commit、issue、PR、release 都在这里。
- 协作组织下的 `skin-nexus` 为 fork 镜像（显示 `forked from` 回链），仅作组织内浏览/同步用，**不在 fork 上开发**。
- 贡献归属以主仓 commit 历史为准；发起人的原始著作权不因后续贡献而稀释（AGPL-3.0 §11 版权-专利条款 + 本文件声明共同保障）。

## 贡献者认可

- 所有合并贡献进入 `git shortlog` 永久记录；
- 重大贡献者在 `docs/CHANGELOG.md` 与对应 GitHub Release 说明中致谢；
- 项目对外引用格式见 `CITATION.cff`。

## 与社团的关系

- SkinNexus 是发起人的个人开源项目；社团作为**主要用户与协作方**参与测试、部署与插件生态。
- 社团部署实例与上游保持同步；社团提出的定制需求进入主仓 issue 统一排期，不私改 fork。
