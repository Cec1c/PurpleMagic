# 根据游戏服务生成 PurpleMagic 登录器

当前已提供 **配置准备 → Windows 构建 → 便携包装 → Hub 发布** 的命令行工具链。网页上选择服务器并生成安装包的图形化生成器尚未实现。

## 先区分三个地址

| 配置 | 指向哪里 | 用途 |
| --- | --- | --- |
| `ApiUrl` | 玩家 HTTPS 网关，如 `https://players.example.com:8443` | 登录、注册、状态、签名发布信息 |
| `GameHost` + `BootstrapPort` | 游戏公网 IP 和原生频道入口 | 游戏实时 TCP 直连；`GameHost` 当前使用 IP 字面值 |
| 内容源/登录器包 URL | Hub 中发布的来源和包地址 | 更新下载；可使用独立 HTTPS 文件服务/CDN |

游戏网关的 `upstream` 是私有账号服务地址，不填进玩家 profile。数据库密码、游戏管理 Token 和签名私钥也不进入登录器。接入要求见 [网关接入办法](GATEWAY_INTEGRATION.md)。

## 准备 profile

1. 复制 `launcher/server.example.json` 到忽略目录 `runtime/launcher-input/server.json`，填写玩家 HTTPS、游戏 IP、BootstrapPort 和 `ChannelServerId`。
2. 提供批准的客户端目录（包含 `DNF.exe`、`Script.pvf`）、玩家 HTTPS 叶证书，以及本服更新 RSA 公钥。
3. 运行配置准备工具。它从真实文件计算 EXE/PVF SHA256 和证书 DER SHA256，生成供 Hub 与登录器共同使用的 `profile.json`。

```text
python scripts/prepare_launcher_profile.py --connection runtime/launcher-input/server.json --client D:/GameClient --certificate runtime/launcher-input/player-fullchain.pem --public-key runtime/launcher-input/update-public.pem --output runtime/launcher-input/profile.json
```

输出文件必须不存在。工具拒绝私钥作为公钥输入，也拒绝混入 DSN/Token 等未知连接字段。`DefaultClientDirectory` 默认留空；登录器会发现现有客户端，也可以通过 Windows 原生文件夹选择器选择路径。

首次部署可通过 OpenSSL 创建 RSA 信任根，私钥只提供给 Hub：

```sh
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out signing-key.pem
openssl pkey -in signing-key.pem -pubout -out update-public.pem
```

已有服务沿用原公钥和私钥。Hub 的 `signing_key_file` 与 profile 中 `UpdatePublicKey` 必须配对。TLS 换证也会改变固定指纹，需要安排对应 profile/客户端更新。

### 频道兼容字段

`ChannelServerId` 必须等于实际频道目录发布的区服编号；`PreviousChannelServerId` 只用于明确的临时迁移。部分 DNF90 客户端需要 `ChannelHistory`：它包含 `WindowsCodePage:936`、`Adapter:dnf90-history-v1`、经该客户端验证的 `AccountSectionBase64`。准备工具会保留和校验这组字段；其内容来自客户端版本适配，不是玩家账号密码，也不能用完整玩家 `DNF.cfg` 代替。

`MinimumLauncherVersion` 控制允许的最低客户端版本；当前模板为 0.2.18。`SettingsUrl` 是保留的旧清单字段，当前引擎主要从 `ApiUrl/launcher/v2/bootstrap.json` 获取签名发布信息。新 Hub 的首个状态可以没有内容包；发布批准资源后客户端开始检查对应更新。

客户端状态旁的 EXE 版本标识取自签名资源清单的版本名，如 `client-v153-raid-state-20261022` 显示为 `V153`。只有本地 `DNF.exe` 的长度及 SHA256 与清单一致时才显示；待更新的旧 EXE 可使用本目录 `.dnf90-update/installed.json` 的已验签回执识别。没有匹配版本时省略标识。发布资源时继续保留 `client-v数字-说明` 的命名；该标识与底部的登录器版本独立。

## 构建与检查

构建机使用 Windows、Flutter、.NET 8 SDK、Visual Studio C++ 桌面工具与 NativeAOT。准备自己的 `launcher/Flutter/assets/background.webp` 和 `announcement.webp`；这两张美术输入不随源码分发。witch PNG 和 ICO 已在仓库，见 [品牌资源](BRANDING.md)。

```text
BUILD_FLUTTER.bat --flutter D:/SDKs/flutter --dotnet dotnet --profile-dir runtime/launcher-input --output dist/PurpleMagic-0.2.18
BUILD_PORTABLE.bat --bundle dist/PurpleMagic-0.2.18.zip --output dist/0.2.19/PurpleMagic.exe --dotnet dotnet
```

`--output` 选择新路径，省略时生成 `dist/PurpleMagic.exe`。构建产生完整目录、完整 ZIP 和 `-app-update.zip`，便携脚本包装完整 ZIP。显示名、窗口名与产品属性为 PurpleMagic；内部 `DNF90.exe`、`DNF90Launcher.exe`、更新 Product ID 和缓存目录继续沿用既有兼容契约。

只读连接检查（不启动游戏）：

```text
dotnet run --project launcher/Probe -- runtime/launcher-input/profile.json --channel-only
```

这会检查证书固定、账号状态、原生频道握手、区服编号和代表性游戏端口。`Core/Engine/Portable` 与 Flutter 检查见仓库首页。

## 发布到本服

将完整便携 EXE 命名为 `PurpleMagic.exe`，部署至网关 `release_dir` 后通过 `/launcher/PurpleMagic.exe` 下载。旧的 `DNF90-Portable-x.y.z.exe` 链接继续支持，下载文件名同样为 `PurpleMagic.exe`。新版路由需要随网关部署；签名 APP 更新地址与内部文件名保持原契约。

通过私有 Hub `POST /admin/api/bundle/0.2.18` 上传 `-app-update.zip`，使用管理 Bearer Token 或已登录会话及 CSRF。此接口完成校验后立即发布新签名序列；不要把完整便携 EXE 当作 APP 更新 ZIP 上传。管理员页面/脚本的实际请求地址在管理域名下，玩家从签名 bootstrap 获取下载 URL、长度和 SHA256。

游戏内容更新由“公告与下载源”中的来源检查和发布契约处理。来源需要提供兼容的签名清单与内容文件接口，普通网盘分享页面不是直接内容源；参见 [网络和下载源契约](architecture/NETWORK_AND_CONTRACTS_20261019.md)。

发布后读回 `/launcher/v2/bootstrap.json` 并验签，完整下载更新包验哈，分别检查旧版检测更新和新版启动。APP 更新替换内部界面程序；旧桌面便携入口自身的图标/属性需换用新便携 EXE。

## English quick path

The existing generator is a command-line build pipeline, not a web build UI. Copy `launcher/server.example.json` into the ignored runtime directory, fill the player HTTPS origin, direct game IP/bootstrap port and actual channel server ID, then run `scripts/prepare_launcher_profile.py` with the approved client, TLS certificate and RSA **public** key. The tool computes all file/certificate hashes and preserves explicit channel compatibility fields.

On Windows, supply your background/announcement artwork and run `BUILD_FLUTTER.bat`, then `BUILD_PORTABLE.bat` using the commands above. The same approved profile is used by the Hub and embedded launcher. Keep the matching PKCS8 signing private key exclusively in the Hub. The read-only `launcher/Probe --channel-only` checks TLS and game connectivity. Upload the APP ZIP through the private Hub bundle endpoint to publish a new signed update sequence; distribute the portable EXE separately.

便携入口会在当前目录和入口目录检查 `.client/`、`client/`，要求存在非空 `DNF.exe`、`Script.pvf`；已有有效手选目录优先。完整版本哈希在更新与启动时继续校验。
