# 部署 PurpleMagic

首次部署可先读《[大肥鱼也能看懂的使用指南](BIG_FISH_GUIDE.zh-CN.md)》，包含单机/联机选型、本地证书、地址填写及日常操作。本页保留完整部署命令与维护说明。

本仓提供可构建的 Linux 镜像、Compose 编排和配置准备工具。推荐先在同一台 Linux 主机运行 PlayerGateway、GMService 和 Nginx，接入已有游戏服务；需要跨主机时，沿用相同进程，通过 SSH 转发两条私有链路。

## 部署前准备

- Linux x86-64、Docker Engine + Compose v2、Python 3.10+、OpenSSL、Git。构建阶段下载 Node.js 24 和 Go 1.26.5；镜像内完成构建，宿主机无需安装这两个 SDK。
- 已运行且实现[网关接入契约](GATEWAY_INTEGRATION.md)的游戏服务，以及匹配本服存档结构的 MariaDB。配置中的库名/表前缀可调整，跨版本数据库结构需要仓储适配。
- 批准的 `DNF.exe`、`Script.pvf`，对应玩家 profile 和 RSA 签名私钥。首次建立信任根与 profile 的步骤见[生成登录器](LAUNCHER_INPUTS.md)。
- 两个域名与证书：`gm.example.com` 用于 GM，`players.example.com:8443` 用于玩家 API。证书覆盖对应域名，提供完整链和私钥；`playerCA` 提供可验证玩家证书的 CA 链（自签时用该证书）。
- 游戏服务已有的玩家代理凭据、私有管理 Token、注册库/存档库 DSN。使用非 root 部署用户准备配置。

Compose 使用 **Linux host networking**，让现有数据库和游戏 API 的 `127.0.0.1` 地址保持可用。游戏进程、数据库和原有频道端口由游戏项目管理。

## 1. 准备私有配置

```sh
git clone https://github.com/Cec1c/PurpleMagic.git
cd PurpleMagic
mkdir -p runtime
cp deploy/selfhost/settings.example.json runtime/selfhost-settings.json
chmod 600 runtime/selfhost-settings.json
```

按自己的环境填写 `runtime/selfhost-settings.json`：

| 字段 | 填写内容 |
| --- | --- |
| `gmOrigin` | 管理 HTTPS Origin，如 `https://gm.example.com` |
| `approvedProfile`、`signingPrivateKey` | 同一信任根对应的玩家 profile 和 PKCS8 RSA 私钥路径 |
| `pvfFile` | 与 profile 中 `PvfSha256` 一致的批准 PVF |
| `playerCertificate`、`playerPrivateKey`、`playerCA` | 玩家 HTTPS 证书、私钥和 CA 链 |
| `webCertificate`、`webPrivateKey` | GM 域名证书与私钥 |
| `database` | 注册库、存档库 DSN；账号表、分片数据库清单与表前缀 |
| `game.upstream`、`game.proxySecret` | 游戏账号 HTTP 上游及游戏服务接受的代理凭据 |
| `game.adminUrl`、`game.adminToken` | 私有 `/api/game/admin` 地址及其 Bearer Token |

```sh
python3 deploy/selfhost/prepare.py --settings runtime/selfhost-settings.json --output runtime/selfhost
```

准备工具计算 PVF 全哈希，核对 profile、TLS 证书指纹、证书域名/私钥及签名公钥，生成相互匹配的服务凭据和 GM Token。配置在写入时使用 `0600`，数据目录使用 `0700`；已有输出目录不会覆盖。`runtime/` 被 Git 和 Docker 构建上下文排除。

生成文件包括 `gateway.json`、`service.json`、离线 `indexer.json`、`compose.env`、`nginx.conf`、`admin-token.txt`，以及 PVF、证书和签名材料的部署副本。`compose.env` 只保存路径、UID/GID 和 PVF 摘要；凭据留在文件中。

## 2. 构建、索引与表结构

下方命令均从仓库根目录运行。先备份现有游戏数据库，再初始化新增的管理表。

```sh
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml build
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-pvf
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-items
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-quests
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-professions
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm schema
```

四个离线步骤生成 PVF 目录、物品摘要/详情、任务、职业和副职业六类索引。`schema` 使用仓库提供的增量建表入口；它需要已有游戏表和正确 DSN。索引命令不连接数据库。

## 3. 启动与验收

```sh
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml up -d gateway gm edge
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml ps
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml logs --tail=80 gateway gm edge
```

打开 `https://gm.example.com/console/#query`，使用 `runtime/selfhost/admin-token.txt` 中的 Token 登录。GM 与 Hub 共用本次生成的 Token，公告与下载源页面会建立各自的管理会话。

验证 `https://players.example.com:8443/api/status` 和 `/launcher/v2/bootstrap.json` 返回成功；登录 GM 后查询账号/角色、搜索一个已知物品、打开公告与下载源。再使用[登录器探测器](LAUNCHER_INPUTS.md#构建与检查)验证证书固定、频道身份和游戏端口。

| 监听 | 用途 | 对外开放 |
| --- | --- | --- |
| `443` | Nginx → GM Web；`/admin/` → Hub | 管理域名 |
| `8443` | PlayerGateway TLS：账号、状态、签名发布与下载 | 玩家域名 |
| `127.0.0.1:18444` | GM 事务与 Hub 管理 | 本机 |
| `127.0.0.1:18080` | GMService 会话、网页、查询 | 本机 |
| `127.0.0.1:18081` | PVF 定义服务 | 本机 |

游戏 TCP 连接使用 profile 的 `GameHost`、`BootstrapPort` 和游戏频道表，独立于以上 HTTP 端口。

## 更新、数据保留与双主机

- 日常源码升级：更新仓库、重新 `build`，再 `up -d gateway gm edge`。保留同一份私有配置和持久目录。
- `gm/` 保存网页会话与队列；`hub/` 保存签名发布状态、公告和更新包；永久福利规则/领取回执在游戏数据库。备份这些目录和数据库，保留签名私钥。重建容器不重置规则或发放记录。
- 新 PVF：使用新目录准备和验证索引，核对规则附件及配套 EXE/profile 后切换同一代次。不要覆盖运行中的 PVF，也不要通过重建规则来清空回执。
- 程序回退使用上一镜像并保留当前持久数据；已发布签名序列保持递增，资源回退也走新的发布序列。
- 双主机：游戏侧运行 `gateway`，控制侧运行 `gm`/`edge`；游戏侧通过 SSH 本地转发访问 catalog，控制侧通过反向转发访问 management。配置方向和命令见[网关接入](GATEWAY_INTEGRATION.md#跨主机接入)。

## 配置与源码检查

```sh
python3 scripts/test_deployment_tools.py
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup config --quiet
```

示例域名、IP、凭据占位值用于说明字段。运行时配置、日志、真实地址和密钥留在 `runtime/` 或仓库外；发布仓库时只提交模板。
