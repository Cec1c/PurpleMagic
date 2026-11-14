# 登录器 1.0.12

当签名游戏内容清单包含 `CombatPower.dll` 或 `DpsMeter.dll` 时，1.0.11 的路径校验会拒绝整个清单，显示“内容文件路径、大小或格式无效”。1.0.12 允许这两个固定根目录文件名，并保持原有签名、相对路径、重复路径、文件大小、SHA-256 和压缩校验。

## 客户端请求

普通与反作弊构建均在原有 `bootstrap.json` 请求上附加：

```http
X-DNF90-Content-Capabilities: native-plugins-v1
```

该请求头表示客户端已支持上述两个插件路径。服务端不必回显此请求头；继续返回原有合法签名清单的更新源仍可使用。客户端继续验证证书固定值、RSA-PSS 签名和单调递增的清单序号。

旧登录器会先验证整份清单，再检查登录器自身更新。因此，对于仍有旧登录器的更新源，仅在含插件的完整清单里增加 APP 更新记录不能解除旧路径校验失败。此类更新源需要另行提供旧版可验证的升级入口；支持分阶段升级的服务端可向旧版返回仅含 APP 更新的签名清单，待升级后的能力请求到达，再返回更高序号的完整内容清单。同一个序号不得对应不同正文，完整清单不得回退到较旧的升级清单。

本次仓库更新包含客户端实现与隔离回归。服务端分阶段升级的路由、签名清单生成和运行配置需要在对应更新服务中另行实现或配置；它们不由客户端代码自动提供。

## 构建和检查

引擎版本与 Host 程序集版本为 `1.0.12`，Flutter 包版本为 `1.0.12+1`。沿用仓库的 [构建输入说明](LAUNCHER_INPUTS.md) 和 `BUILD_FLUTTER.bat`、`BUILD_PORTABLE.bat`；运行配置仍由构建者提供。

```powershell
dotnet build launcher/EngineTests/Launcher.EngineTests.csproj -c Release
dotnet run --project launcher/EngineTests/Launcher.EngineTests.csproj -c Release --no-build -- --native-plugin-paths
dotnet run --project launcher/EngineTests/Launcher.EngineTests.csproj -c Release --no-build -- --native-plugin-update
dotnet run --project launcher/EngineTests/Launcher.EngineTests.csproj -c Release --no-build
```

反作弊检查在构建和运行时均指定构建类型，例如：

```powershell
dotnet run --project launcher/EngineTests/Launcher.EngineTests.csproj -c Release -p:AntiCheat=true -- --native-plugin-update
dotnet run --project launcher/EngineTests/Launcher.EngineTests.csproj -c Release -p:AntiCheat=true
```

完整引擎检查还调用 Python 诊断分析器，需要测试环境可导入 `jsonschema`。路径专项使用本地生成的签名数据；升级专项只连接回环 TLS 服务器，验证实际请求头、从缓存升级清单读取完整清单，以及无效响应不覆盖已验证缓存。测试不需要生产配置、实际游戏资源或在线账号。
