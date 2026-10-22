# 大肥鱼也能看懂的使用指南

**自己一个人玩，和叫朋友一起玩，用的是同一套 PurpleMagic。** 都需要游戏服务、数据库、网关和登录器；变化的是谁能连到你的机器，以及登录器里填什么地址。本指南使用仓库现成的 Docker Compose，**至少准备一套能运行 Linux 容器的 Docker 环境**。

PurpleMagic 负责登录、更新和 GM 管理。游戏服务、游戏数据库以及配套客户端由你准备；Compose 会启动 `gateway`、`gm`、`edge` 三个常驻容器，分别负责玩家网关、GM/PVF 和管理网页 HTTPS。

第一次部署按下面的顺序走。已有可用服务，只想学操作，可以直接看[日常使用](#daily)。

| 你要做什么 | 从这里开始 |
| --- | --- |
| 一台电脑自己玩 | [选部署方式](#where) → [准备东西](#prepare) → [启动服务](#start) |
| 同一局域网内和朋友玩 | 完成单机部署，再看[联机地址与端口](#multiplayer) |
| 让外网朋友进服 | 同上，配置公网入口、证书及游戏频道地址 |
| 给自己的服做登录器 | [生成与使用登录器](#launcher) |
| 发道具、配长期福利 | [日常使用](#daily) |
| 连不上、登录失败或物品搜不到 | [常见问题](#troubleshooting) |

<a id="where"></a>
## 1. 先决定：东西放在哪儿

### 单机：Windows 玩游戏，Linux 跑服务

一台 Windows 电脑也可以完成：Windows 运行游戏和登录器，电脑里的 Linux 虚拟机运行 Docker，以及你已有的游戏服务和数据库。建议给虚拟机配置桥接网络和固定局域网地址，例如 `192.168.1.50`。先确认 Windows 可以访问这个地址。

本仓 Compose 使用 **Linux host networking**，配置准备工具也在 Linux 运行。按这份指南走，Windows 用户先准备 Linux 虚拟机；后面的 `sh` 命令全部在该虚拟机中执行。已有 Linux 服务器时直接使用它。Docker 安装入口见 [Docker Engine 官方文档](https://docs.docker.com/engine/install/)，安装时同时启用 Compose 插件。

“至少一个 Docker”指一套 Docker 运行环境，里面可以同时跑多个容器。只下载 ZIP、双击登录器，不会自动安装游戏服务。

### 联机：让其他电脑也能到达同一套服务

| 场景 | 游戏与 Docker 可以放哪儿 | 玩家使用的地址 |
| --- | --- | --- |
| 单机 | 本机 Linux 虚拟机 | Windows 能访问的虚拟机 IP |
| 局域网联机 | 同一台虚拟机、家用服务器或局域网 Linux 主机 | 所有人都能访问的固定局域网 IP |
| 互联网联机 | 公网 Linux 服务器，或有公网入口的家用主机 | 公网 IP/域名；游戏频道也公布玩家可访问的地址 |

**`127.0.0.1` 永远是“发起连接的那台机器自己”。** Windows 里的它不是 Linux 虚拟机，朋友电脑里的它也不是你的服务器。本指南的容器采用 host 网络，因此容器内的 `127.0.0.1` 才能指向同一台 Linux 主机；如果数据库在普通 bridge 容器里，需要先把相应端口发布到 Linux 主机，或使用实际可达地址。

单机和联机都沿用下面的步骤。默认先把 PurpleMagic 的三个容器放在同一台 Linux 主机；拆成多台机器的配置见[跨主机接入](GATEWAY_INTEGRATION.md#跨主机接入)。

<a id="prepare"></a>
## 2. 先备齐这几样东西

| 准备项 | 具体是什么 |
| --- | --- |
| Docker 与基础工具 | Linux x86-64、Docker Engine、Compose v2、Git、Python 3.10+、OpenSSL |
| 游戏服务 | 已能运行的 DNF90/ServerS5A3 兼容服务，提供账号接口和私有管理接口 |
| 数据库 | 现有 MariaDB/MySQL 地址、账号密码、注册库/存档库名称与表映射 |
| 游戏侧凭据 | 已配置在游戏服务里的玩家代理密钥、管理 Token；各至少 32 字符 |
| 配套客户端 | 你要实际使用的客户端，至少用其中的 `DNF.exe`、`Script.pvf` 生成摘要 |
| 地址与证书 | 一个 GM 域名、一个玩家域名，以及覆盖它们的 TLS 证书；单机可用下面的本地证书做法 |

现有适配器对应本项目的存档结构。其他版本即便也用 MySQL，仍需核对[游戏 API 与数据库接入契约](GATEWAY_INTEGRATION.md)。`schema` 步骤只增加管理附加表，游戏建库和游戏程序安装由游戏服务自己的部署流程完成。

在 Linux 终端确认工具可用，Docker 命令应能由当前普通部署用户执行：

```sh
docker version
docker compose version
python3 --version
openssl version
git --version
```

`docker version` 应同时显示 Client 和 Server。然后下载源码：

```sh
git clone https://github.com/Cec1c/PurpleMagic.git
cd PurpleMagic
umask 077
mkdir -p runtime/launcher-input runtime/client-input
cp launcher/server.example.json runtime/launcher-input/server.json
cp deploy/selfhost/settings.example.json runtime/selfhost-settings.json
```

把配套客户端中的 `DNF.exe` 和 `Script.pvf` **复制**到 `runtime/client-input/`，保留原客户端。`runtime/` 用来存私有配置与数据，已被 Git 忽略。

<a id="addresses"></a>
## 3. 分清四个地址，再填连接文件

| 名称 | 谁访问 | 示例与用途 |
| --- | --- | --- |
| GM 网页 `gmOrigin` | 管理员浏览器 | `https://gm.example.com`，用于 Token 登录和管理 |
| 玩家 API `ApiUrl` | 登录器 | `https://players.example.com:8443`，用于注册、登录、更新清单 |
| 游戏 `GameHost` + `BootstrapPort` | 游戏/登录器 | 游戏 IP 与原生频道入口端口，实时游戏默认直连 |
| 更新源 | 登录器下载文件 | 提供签名清单与文件的 HTTPS 服务，详见[更新源](#updates) |

游戏侧 `game.upstream` 是网关访问的内部账号 HTTP 地址，`game.adminUrl` 是私有管理 API；它们填写在服务配置里。玩家使用前表中的 `ApiUrl`。

### 单机、局域网的具体例子

假设 Linux 主机 IP 是 `192.168.1.50`，游戏服务也在这里，实际频道入口是 `10000`，区服编号是 `1`。这些是示例值，端口和区服编号以你的游戏服务配置为准。

在每台使用登录器或 GM 的 Windows 电脑上，以管理员身份编辑 `C:\Windows\System32\drivers\etc\hosts`，加入：

```text
192.168.1.50 gm.purplemagic.test players.purplemagic.test
```

Linux 主机的 `/etc/hosts` 也加入同一条。这里用 `.test` 本地域名，所以不用购买域名。编辑 `runtime/launcher-input/server.json`：

```json
{
  "ApiUrl": "https://players.purplemagic.test:8443",
  "GameHost": "192.168.1.50",
  "BootstrapPort": 10000,
  "ChannelServerId": 1,
  "MinimumLauncherVersion": "0.2.20",
  "DefaultClientDirectory": ""
}
```

`GameHost` 当前填 IP；`ChannelServerId` 必须等于实际频道目录的区服编号。需要 `ChannelHistory` 的客户端沿用已验证的兼容字段，格式见[频道兼容字段](LAUNCHER_INPUTS.md#频道兼容字段)。

公网部署则将 `ApiUrl` 改为自己的玩家域名，将 `GameHost` 改为玩家能直连的公网 IP，并使域名 DNS 指向对应主机。模板使用的 `example.com` 和 `203.0.113.10` 是文档示例地址。

### 准备 HTTPS 证书和更新签名密钥

公网部署使用覆盖自己域名的有效证书，准备叶证书在前的完整证书链、对应私钥，以及验证玩家证书的 CA 链。

单机/局域网可在 Linux 生成一张覆盖两个本地域名的自签证书：

```sh
openssl req -x509 -newkey rsa:3072 -nodes -days 365 \
  -keyout runtime/launcher-input/local-tls-key.pem \
  -out runtime/launcher-input/local-tls.pem \
  -subj "/CN=players.purplemagic.test" \
  -addext "subjectAltName=DNS:players.purplemagic.test,DNS:gm.purplemagic.test" \
  -addext "basicConstraints=critical,CA:TRUE"
```

将 `local-tls.pem` 公开证书复制到每台使用 GM 的 Windows 电脑，导入“当前用户 → 受信任的根证书颁发机构”。只导入自己生成并核对过的证书，私钥留在 Linux。部署配置的 `playerCA` 也使用这张证书。

首次建立自己的更新信任根：

```sh
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 \
  -out runtime/launcher-input/signing-key.pem
openssl pkey -in runtime/launcher-input/signing-key.pem -pubout \
  -out runtime/launcher-input/update-public.pem
```

签名私钥用来签发本服更新；公钥放进登录器，验证更新来自本服。已经运行的服继续使用原密钥。TLS 证书和更新签名是两套材料，各管各的用途。

生成 profile（下面继续使用本地证书；公网部署将 `--certificate` 换为实际玩家证书链路径）：

```sh
python3 scripts/prepare_launcher_profile.py \
  --connection runtime/launcher-input/server.json \
  --client runtime/client-input \
  --certificate runtime/launcher-input/local-tls.pem \
  --public-key runtime/launcher-input/update-public.pem \
  --output runtime/launcher-input/profile.json
```

它会计算 EXE、PVF 和证书摘要，写入公钥与连接参数。profile 是网关和登录器共同使用的连接说明，输出路径需为新文件。

<a id="start"></a>
## 4. 填服务配置，启动三个容器

### 4.1 填 `runtime/selfhost-settings.json`

沿用复制来的 JSON 模板，字段名保持原样。文件路径使用 Linux 绝对路径，可以先运行 `realpath runtime/launcher-input` 确认目录。相对路径也按执行准备命令时所在目录解析。

本地例子的文件字段可这样填写：

| 字段 | 值 |
| --- | --- |
| `gmOrigin` | `https://gm.purplemagic.test` |
| `approvedProfile` | `runtime/launcher-input/profile.json` |
| `signingPrivateKey` | `runtime/launcher-input/signing-key.pem` |
| `pvfFile` | `runtime/client-input/Script.pvf` |
| `playerCertificate`、`webCertificate`、`playerCA` | 都填 `runtime/launcher-input/local-tls.pem` |
| `playerPrivateKey`、`webPrivateKey` | 都填 `runtime/launcher-input/local-tls-key.pem` |

公网部署替换为各域名实际的证书与私钥路径，`playerCA` 填验证玩家证书的 CA 链。当前配方固定 GM HTTPS 为 `443`、玩家 HTTPS 为 `8443`。

接着填写数据库与游戏 API：

| 字段 | 怎么填 |
| --- | --- |
| `database.registrationDsn` | 注册/福利记录库，例如 `gm:你的密码@tcp(127.0.0.1:3306)/registration?parseTime=true&charset=utf8mb4` |
| `database.repositoryDsn` | 存档库，同样格式，将数据库名改为实际值 |
| `database.accountTable` | 实际账号表名，模板为 `dnf90_web_accounts` |
| `database.databases` | 实际存档库列表，JSON 数组；单库也保留数组格式 |
| `database.tablePrefix` | 实际存档表前缀，模板为 `dnf` |
| `game.upstream` | 现有游戏账号 HTTP Origin，例如 `http://127.0.0.1:7000` |
| `game.proxySecret` | 游戏服务已经接受的玩家代理密钥 |
| `game.adminUrl` | 现有私有管理 API，例如 `http://127.0.0.1:7000/api/game/admin` |
| `game.adminToken` | 游戏服务已经接受的管理 Token |

将所有 `CHANGE_ME` 占位值换掉。两个游戏侧密钥应与游戏服务一致；GM 登录 Token 稍后由准备工具另行生成。

### 4.2 生成配置并构建镜像

以下命令都在 **Linux 的 PurpleMagic 仓库根目录**，由普通部署用户运行：

```sh
python3 deploy/selfhost/prepare.py \
  --settings runtime/selfhost-settings.json --output runtime/selfhost
docker compose --env-file runtime/selfhost/compose.env \
  -f deploy/selfhost/compose.yaml build
```

准备工具核对 PVF 摘要、证书、域名与签名公钥，并生成匹配的服务配置和 Token。输出目录必须是新目录。镜像会构建新版 `gm-console` 和 Go 服务，Linux 宿主机只需前面列出的工具，Node.js/Go SDK 在镜像内下载使用。

### 4.3 建 PVF 索引、初始化管理表

先备份现有游戏数据库。然后依次执行，上一条成功再运行下一条：

```sh
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-pvf
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-items
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-quests
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm index-professions
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml --profile setup run --rm schema
```

前四条生成目录、物品摘要/详情、任务、职业和副职业六类索引；`schema` 为已有数据库增加管理附加表。这里的临时容器结束后会移除，索引和数据留在挂载目录。

### 4.4 启动并打开网页

```sh
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml up -d gateway gm edge
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml ps
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml logs --tail=80 gateway gm edge
```

`gateway`、`gm`、`edge` 应保持运行。启动失败时，最后一条能查看报错。

在浏览器打开 `https://gm.purplemagic.test/console/#query`（公网部署替换域名），输入 `runtime/selfhost/admin-token.txt` 里的 Token。可记住登录 30 天。GM Token 是管理员钥匙，玩家在登录器中使用游戏账号密码。

首次核对这三件事：

1. 玩家地址 `https://players.purplemagic.test:8443/api/status` 能正常返回。
2. 同一地址的 `/launcher/v2/bootstrap.json` 能返回签名发布信息。
3. GM 能查到已有账号与角色，物品搜索能找到本服 PVF 中的物品，“公告与下载源”能读到配置。

本地证书可在 Linux 用下面的命令验证，无需跳过 TLS 校验：

```sh
curl --fail --cacert runtime/launcher-input/local-tls.pem https://players.purplemagic.test:8443/api/status
curl --fail --cacert runtime/launcher-input/local-tls.pem https://players.purplemagic.test:8443/launcher/v2/bootstrap.json
```

<a id="multiplayer"></a>
## 5. 从自己玩，变成和朋友一起玩

局域网用户在各自电脑上配置同样的本地域名解析，使用能访问服务器的登录器即可。公网联机还要处理 DNS、云防火墙/安全组和家用路由器端口映射。

| 连接 | 本配方的端口 | 谁需要访问 |
| --- | --- | --- |
| GM 网页 | TCP `443` | 管理员 |
| 玩家账号与更新 API | TCP `8443` | 玩家 |
| 游戏频道入口 | `BootstrapPort` 的实际端口 | 玩家 |
| 游戏频道端口 | 游戏频道表实际公布的端口 | 玩家；开放/映射所有要使用的频道 |
| 私有管理与 PVF | `127.0.0.1:18444`、`18080`、`18081` | 本机服务之间；跨主机使用 SSH 隧道 |

朋友能打开登录器、甚至能登录账号，却进不了频道时，优先检查**频道表公布的 IP/端口**。只改 profile 的 `GameHost` 不会改写游戏服务的频道表。家用网络的路由映射也要覆盖实际游戏端口。

从手机热点或其他外网实际检查一次公网入口。只有运营商内网、没有公网入站能力时，需要准备可访问的服务器或网络入口。实时游戏按直连配置，GM 的管理隧道用于管理流量。

单机切公网后，重新准备正确地址的 profile，并按发布流程更新登录器/签名配置。数据库、原福利规则与领取记录继续使用。

<a id="launcher"></a>
## 6. 给自己的服生成登录器

如果管理员已经给了你适配本服的登录器，直接看本节最后的玩家操作。自己开服则需要在 **Windows 构建机**安装 Python 3、Git、Flutter、.NET 8 SDK、Visual Studio C++ 桌面工具与 NativeAOT；Docker 负责服务端，Windows 工具链负责登录器。

1. 在 Windows 获取同一版本源码，将第 3 节的 `profile.json` 放到 Windows 仓库的 `runtime/launcher-input/`。
2. 准备自己的 `launcher/Flutter/assets/background.webp` 和 `announcement.webp`。witch 品牌图标已经在仓库中。
3. 在仓库根目录的 Windows 命令提示符（CMD）运行，SDK 路径换成自己的。首次先获取 Flutter 依赖，供构建脚本使用：

```bat
call D:/SDKs/flutter/bin/flutter.bat config --enable-windows-desktop
call D:/SDKs/flutter/bin/flutter.bat doctor
cd launcher/Flutter
call D:/SDKs/flutter/bin/flutter.bat pub get
cd ../..
BUILD_FLUTTER.bat --flutter D:/SDKs/flutter --dotnet dotnet --profile-dir runtime/launcher-input --output dist/PurpleMagic-0.2.20
BUILD_PORTABLE.bat --bundle dist/PurpleMagic-0.2.20.zip --output dist/PurpleMagic.exe --dotnet dotnet
dotnet run --project launcher/Probe -- runtime/launcher-input/profile.json --channel-only
```

这里的 `0.2.20` 对应本文所依据的源码版本；后续构建使用所检出版本。输出选择新路径。产物包括完整目录、完整 ZIP、`-app-update.zip` 和便携入口 `PurpleMagic.exe`。最后一条只检查连接、证书和频道，不启动游戏。

初次把 `PurpleMagic.exe` 与配套游戏客户端交给玩家即可。客户端里应能直接找到 `DNF.exe`、`Script.pvf`；运行登录器，选择这个目录，注册/登录，再进入游戏。登录器也会识别入口附近的 `.client/`、`client/`。客户端状态旁的 `V153` 等标识来自本地 EXE 与已验签清单的哈希匹配；底部显示登录器自身版本。内部程序名和更新标识继续由构建工具管理。

自更新时，将 **`-app-update.zip`** 按 [Hub 发布步骤](LAUNCHER_INPUTS.md#发布到本服)上传；完整 ZIP 用于便携包装。profile 只包含公开连接信息和信任材料，签名私钥与数据库配置留在服务端。

<a id="daily"></a>
## 7. 日常怎么用 GM

### 查人、改背包

先在“查询”或“账号与角色”搜索账号/角色，选择目标。进入“背包”后，按物品栏、穿戴栏、宠物栏、仓库、账号仓库、宠物、时装、徽章切换，点物品查看详情，再使用页面提供的修改操作。

角色专属修改只要求目标角色离线；账号共享资产按账号状态检查。页面提示仍在线或清理中时，等目标退出完成后重试。完整可编辑范围见[功能说明](../README.zh-CN.md#能做什么)。

### 批量发邮件、道具

1. 勾选角色，或勾选账号。**勾选账号会展开该账号的全部现有角色**。
2. 进入“邮件”，填写标题、正文、金币和物品附件。
3. 检查最终角色名单、人数和数量，确认一次发送。
4. 查看队列/发放结果；关闭网页后后台继续处理。

“给账号下全部角色各发一份”和“每账号只领一次”是不同范围，后一种使用福利规则对应的账号范围。

### 福利：配置一次，让后台持续发

从侧栏“福利规则”进入，也可直接打开 `/console/#benefits`，无需先选择账号或角色。填写规则名称，选择金币、点券或指定道具，再设置领取次数和是否“包含未来新建角色”，最后创建并启用。

金币与道具可以选择每角色一次或每账号一次；点券固定每账号一次，直接存入账号共享余额。道具可按名称搜索或输入物品 ID，每条规则一种道具。账号级规则通过一个符合条件的角色收取邮件或到账通知；新账号还没有角色时，等创建角色后发放。

例如“冒险启程福利”：每个角色 `2,000,000` 金币，包含未来新角色。启用后后台立即安排校验；每轮完成后间隔约三小时再扫描所有账号，覆盖新账号的首个角色和老账号中新建的角色。浏览器可以关闭，服务重启后接续未完成的扫描。

| 操作或情况 | 会发生什么 |
| --- | --- |
| 创建并启用 | 后台自动扫描并发放，无需逐个手工发送 |
| 包含未来新角色 | 后续符合条件的角色也能加入 |
| 只覆盖现有角色 | 按首次启用保存的现有角色截止时间判断 |
| 重启服务、重复扫描 | 使用永久回执跳过已经发过的对象 |
| 暂停后恢复 | 保留原规则和回执，继续处理符合条件的未领对象 |
| 删除游戏内已收邮件 | 不改变该规则已领取的事实 |
| 新建另一条规则 | 是另一份领取资格，原来领过的人也可能再次符合条件 |

打开“发放详情”后，页面会自动逐片查询，优先显示未发放人数与名单；成功记录默认折叠在“已成功发放”。查询尚未结束时显示“已查询范围”，完成后才是本轮完整统计。

需要立即处理缺口时，点击规则行或详情中的“补发未收到”，确认一次即可。任务在后台继续，已经有成功回执的对象自动跳过。规则是长期一次性福利，不按天或每轮校验重置领取次数。完整说明见[福利规则机制](BENEFIT_RULES.md)与[发放进度、补发和自动校验](GM_BENEFIT_DELIVERY.md)。

### 任务、成长、公告

“任务”用于查看和处理游戏任务；“GM”提供等级、按根职业选择转职/觉醒、SP/TP、金币与点券等调整；“活动管理”是独立侧栏，可启停游戏内活动，保存后在下次游戏服务重启时生效；“公告与下载源”维护登录器公告、更新配置和游戏连接。操作完成后看结果回执，修改前先核对当前目标与数值。

<a id="updates"></a>
## 8. 更新源到底填什么

第一次部署，可以先用完整配套客户端连服。Hub 初始状态允许没有内容更新包；需要让登录器自动更新时，再接入符合协议的内容源。

“公告与下载源”中的上游来源填写 **HTTPS Origin**（例如 `https://updates.example.com`）和该来源的签名公钥。服务会从下面的路径读取清单：

```text
https://updates.example.com/channels/stable/manifest.signed.json
https://updates.example.com/channels/preview/manifest.signed.json
```

内容源还需提供 `/blobs/{hash}` 文件接口，支持 `HEAD/GET`、长度信息和 `Range/206`。可由 HTTPS 文件站、对象存储/CDN 提供；普通网盘分享页面、GitHub 仓库首页没有这些接口，不能直接填进来源框。

日常顺序是：**准备文件并完整验哈 → 检查来源生成候选 → 确认发布 → 从玩家实际入口下载并验哈**。APP 包和游戏资源分别发布。Hub 上传 APP 更新包会直接产生新的签名发布序列，因此文件与下载映射应先准备好。详细字段及不同存储形式见[下载源契约](architecture/NETWORK_AND_CONTRACTS_20261019.md)。

<a id="maintenance"></a>
## 9. 关机、更新、备份

临时停止/恢复服务，在 Linux 仓库根目录执行：

```sh
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml stop
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml up -d gateway gm edge
```

容器设置了自动重启策略；Linux 主机恢复后也要确保 Docker 自身启动。源码升级时，先备份、阅读该版本变更，再更新源码、`build` 和 `up -d`。保留同一套配置与持久目录。

| 需要保留 | 包含什么 |
| --- | --- |
| 游戏数据库完整备份 | 玩家存档、福利规则、永久回执与管理记录 |
| `runtime/selfhost/gm/` | 管理会话和持久发送队列 |
| `runtime/selfhost/hub/`、`releases/` | 发布状态、公告、更新包和入口文件 |
| 私有配置、`private/`、`tls-player/`、`tls-web/` | 地址、凭据、签名密钥、profile、证书 |
| `assets/`、`indexes/` | 当前配套 PVF 与索引 |

备份时暂停管理写入和扫描，并按数据库自身的备份方法取一致快照；文件与数据库成套保存。更新程序或迁移主机后恢复原规则、回执、队列和签名序列。

PVF 用**完整 SHA256**判断是否还是同一份文件。hash 改变就准备新索引，核对配套 EXE/profile 与规则物品定义后再切换。索引换代不会重置福利领取记录。详细操作见[部署维护](DEPLOYMENT.zh-CN.md#更新数据保留与双主机)。

<a id="troubleshooting"></a>
## 10. 常见问题，对着症状找

| 症状 | 先检查什么 |
| --- | --- |
| Docker 只有 Client，没有 Server | 启动 Docker 服务；确认当前用户能访问它 |
| `prepare.py` 提示非 Linux 或 root 用户 | 在 Linux 主机上，以可运行 Docker 的普通部署用户执行 |
| 提示 `CHANGE_ME` 或缺少输入 | 检查 settings 所有占位值、文件路径和两套游戏侧凭据 |
| 提示 output 已存在 | 新准备使用新目录；已有服务升级保留原配置，不重复跑首次初始化准备 |
| 网页打不开 | 检查域名解析、证书、443 端口占用，以及 `edge` 容器日志 |
| Token 登录失败 | 使用本次部署生成的 `admin-token.txt`，确认打开的是对应服务 |
| 能进 GM，查人失败 | 检查游戏 API、DSN、库表映射和 management 连接 |
| 物品搜不到、PVF hash 不一致 | 核对 profile 与实际 PVF 全哈，确认四条索引命令全部成功 |
| 能登录账号，进不了游戏 | 检查 `GameHost`、BootstrapPort、区服编号和频道表地址/端口，再运行 Probe |
| 自己能玩，朋友不能玩 | 检查朋友是否填了 localhost/内网地址，以及 DNS、路由映射和安全组 |
| TLS/固定证书摘要错误 | 核对实际玩家证书与 profile 的 `CertificateSha256`；换证后同步信任配置 |
| 福利还没到 | 检查规则是否启用、受众范围与详情回执，再看 `gm`/`gateway` 日志；自动扫描分批推进 |
| 更新源检查失败 | 检查是否填写 HTTPS Origin、签名公钥及真实清单/blob 接口 |

排查日志的常用命令：

```sh
docker compose --env-file runtime/selfhost/compose.env -f deploy/selfhost/compose.yaml logs --tail=100 gateway gm edge
```

反馈问题时附上版本、症状和对应报错，隐去 Token、DSN、私钥及玩家信息。

## 11. 许可证

PurpleMagic 的项目代码采用 **GNU GPL v3（GPL-3.0-only）**，完整条款见 [LICENSE](../LICENSE)，适用范围与第三方来源见[许可说明](LICENSING.md)。可以自行使用、研究和修改；分发受 GPL 覆盖的程序及修改版时，应保留许可和版权声明，并按 GPLv3 提供对应源码。

[返回中文 README](../README.zh-CN.md) · [完整部署手册](DEPLOYMENT.zh-CN.md) · [网关接入](GATEWAY_INTEGRATION.md) · [登录器生成](LAUNCHER_INPUTS.md)
