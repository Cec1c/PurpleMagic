# Optional launcher anti-cheat / 可选反作弊

普通构建不编译监护实现。需要此功能的服主使用 `BUILD_FLUTTER.bat --anticheat`，并在批准的 profile 中设置 `AntiCheat.Enabled=true`。保护版未配置或配置为 false 时沿用普通启动流程，不弹 UAC。NPC 商店买卖保护不在本功能内。

```text
BUILD_FLUTTER.bat --anticheat --profile-dir runtime/launcher-input --output dist/PurpleMagic-anticheat
BUILD_PORTABLE.bat --bundle dist/PurpleMagic-anticheat.zip --output dist/PurpleMagic-anticheat.exe
```

直接构建 .NET Host 的等效选项是 `-p:AntiCheat=true`；省略时为 false。两种构建分别使用 `bin/obj` 和 `bin-anticheat/obj-anticheat`。普通构建收到启用策略时要求更换保护发行包，不能悄悄忽略策略继续启动。

## 配置与行为

最小启用配置：

```json
"AntiCheat": {
  "Enabled": true,
  "Mode": "audit",
  "RequiredFiles": [],
  "Rules": [],
  "AllowedModuleHashes": [],
  "CodeRegions": []
}
```

| 字段 | 用途及上限 |
| --- | --- |
| `Enabled` | 默认 false，控制是否启动 UAC 监护 |
| `Mode` | 默认 `audit`，规则命中只记录；`enforce` 对明确哈希规则命中结束本次游戏 |
| `RequiredFiles` | 最多 64 个 `{Name,Sha256}`，仅客户端根目录 DLL 叶文件名 |
| `Rules` | 最多 256 个 `{Id,Scope,Sha256}`；Scope 是 `module` 或 `process`，只匹配完整 SHA256 |
| `AllowedModuleHashes` | 最多 512 个摘要，减少已确认模块的观察记录，不覆盖明确拦截规则 |
| `CodeRegions` | 最多 32 个 `{Id,ExeSha256,Rva,Length,Sha256}`，每段最多 32 KiB；仅匹配指定 EXE 版本，变化只记录 |

摘要为 64 位十六进制；ID 为 1–48 位小写字母、数字、连字符，首位不能为连字符。规则由服主基于已确认样本配置；仓库不预装按软件名称猜测的黑名单。代码区只选不会被重定位或合法补丁改写的固定版本区域。

首次开始游戏前显示收集与退出说明。同意版本通过 `antiCheatConsentVersion` 保存到本机 preferences.json，重启后继续使用；说明发生实质变更时递增版本并重新确认。玩家同意后，只有辅助监护请求 UAC；界面与游戏保持普通权限。要求 Windows 10+ x64、同一 Windows 账号的管理员授权，辅助程序核对双方 PID、创建时间、用户、会话、程序路径和签名策略。以管理员权限打开整个登录器会提示改用普通方式启动。

保护版第一次使用独立配置目录时，会从同一用户的 `DNF90Launcher/preferences.json` 导入账号、客户端目录和设置；已有保护版配置保持。更新清单、序列号与下载缓存不导入，保护说明仍需首次同意。旧配置损坏时按新安装初始化。服主把本服从旧版迁移到保护版时，还需发布旧更新通道并切换默认下载入口；仅发布独立保护通道不会升级现有普通版。

EXE 校验沿用已批准公会兼容补丁的固定摘要映射，代码区域检查使用实际 EXE 摘要。监护重新校验并持有 `DNF.exe`、`Script.pvf` 及指定 DLL 的只读句柄，保护期间拒绝写入/替换。游戏以暂停状态创建，原子加入登录器 Job，再与监护 Job 关联，成功后才恢复执行。取消 UAC、校验失败或关联失败均不启动游戏。关闭登录器、监护退出或通信中断会结束本次关联的游戏；最小化继续运行。规则命中只结束受保护游戏，不结束被观察到的其他软件。

每约 3 秒扫描游戏模块；只有配置了 process 规则才检查同一 Windows 会话中的进程文件摘要。每类扫描单轮预算 1.5 秒，轮转继续，摘要缓存最多 1,024 项且最多保留 30 秒。过大文件（超过 512 MiB）、访问失败或预算耗尽记录覆盖不足；扫描不是即时全量覆盖。每 20 秒有界统计游戏私有可执行内存区域，代码区域校验只读；未知模块、私有可执行区域、代码区域变化均只记录。现有增强诊断观察器可以同时附加，不以调试器存在作为拦截依据。

保护记录在 `%LOCALAPPDATA%/DNF90Launcher-anticheat/anticheat/`，保留最近 5 份，每份最多 1 MiB，记录时间、事件类型、规则 ID、摘要或区域数量。无完整进程列表、文件路径、账号密码、内存转储和自动上传；诊断日志仍使用现有独立开关和同意流程。配置锁文件错误、UAC取消、关联失败和运行期断开分别通过 AC 错误提示呈现。

## 发布与升级

包内 `app/build-flavor.json` 标记 `standard` 或 `anticheat`，Hub 签名清单携带 `buildFlavor` 和策略，客户端更新器再次核对签名及解包标记。普通旧包缺标记按 standard 兼容。Hub 拒绝同一已发布通道切换类型；应为保护版使用独立 Hub 发布状态/玩家 bootstrap 入口，普通版继续原通道。普通构建固定读取 `/launcher/v2/bootstrap.json`，保护构建固定读取 `/launcher/anticheat/v2/bootstrap.json`；APP 仅接受对应前缀下 `bundles/<sha>.zip`。旧版登录器没有此类型校验，不能向其原通道投放保护包来代替迁移。

服务端配套仅涉及 PurpleMagic Hub 的策略校验/签名发布和包类型隔离，不需要修改游戏服务、NPC 逻辑或玩家数据库。本服服务端配套归类为“群服内容”，由 Syncer 在私有仓 `group/` 分支、同名标签和私有 PR 中累计集成；上线按本服资源交付要求验收下载链路。

本实现是用户态保护与已知样本规则检查。管理员权限提高进程可见性和监护稳定性，但不构成内核级防注入；其他管理员或驱动仍可干预，修改过的客户端也可绕过本地逻辑。本轮没有游戏服务心跳或远程可信证明，服务端不能据此认定玩家必然运行了监护。

## 验证入口

`scripts/test_anticheat_native.py --dotnet <.NET8> --output <new-directory>` 使用隔离 x86/x64 程序验证资源锁、暂停创建与 Job 清理、规则行为、诊断共存和有界 IPC，不运行真实游戏、不弹 UAC。`scripts/verify_component.py` 覆盖普通 Core/Engine/Portable、Flutter 和 Go 模块回归。

发布验收需单独记录：真实 UAC 同意/取消、同账号提权下的完整管道绑定、关闭界面/终止监护、真实 DNF 启动与运行兼容、PVF 文件锁影响以及持续运行资源占用。隔离程序通过不代替这组玩家环境验收。

交互 UAC 回归使用 `Launcher.EngineTests` 的独立自包含构建，不给正式 Host 增加测试入口：

```text
dotnet publish launcher/EngineTests -c Release -r win-x64 --self-contained true -p:AntiCheat=true -o runtime/uac-harness
python scripts/test_anticheat_uac.py --harness runtime/uac-harness/Launcher.EngineTests.exe --fixture runtime/native-checks/x64 --output runtime/uac-cancel --action cancel
python scripts/test_anticheat_uac.py --harness runtime/uac-harness/Launcher.EngineTests.exe --fixture runtime/native-checks/x64 --output runtime/uac-approve --action approve
```

`fixture` 目录取自前述原生测试脚本输出，只使用其隔离 EXE/PVF；先运行 cancel 并在安全桌面点“否”，再运行 approve 并点“是”。脚本检查 AC02、完整提权管道、游戏普通权限、PVF 锁、父进程异常退出后的游戏终止和监护退出。Windows 提权会改变 token owner；监护端不使用按 owner 比较的 `CurrentUserOnly`，由管道服务端用户 SID ACL、双方实际 PID/创建时间/用户/会话验证身份，并以 Anonymous 连接禁止服务端模拟管理员令牌。
