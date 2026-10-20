# 登录器增强诊断 · 0.2.19 候选

设置 → **增强诊断日志**。默认关闭，开启后主界面右上角显示“诊断已开启”。
选择问题类型和游戏窗口，标记刚才的问题或开始复现；在已保存片段中选择同一会话的片段，导出到所选目录。

本轮实现客户端首期：自动进程关联、启动/更新/登录阶段、性能摘要、AT-5T、标记/复现、ZIP 导出与保留策略。
游戏实机两小时连续运行及帧时间对照仍待验收。代码、构建和受控 Windows 子进程验证见 [验证记录](diagnostics/VALIDATION.md)。

## 采集与操作

| 操作或模式 | 行为 |
| --- | --- |
| 基础记录 | 启动、签名清单验证、更新完成/错误、客户端验证、频道检查、账号验证及 HTTP 状态码、进程创建/退出；游戏 CPU 累计时间、工作集、私有内存与窗口响应每秒一次 |
| 自动 | 已适配布局读到同一场景中的 AT-5T（对象标记 61235）后开始一次最长 30 秒专项；离开该场景或对象消失时结束 |
| 交互与载具 | 主动复现可在未识别场景时采集已支持的角色、描述符及游戏输入字段 |
| 标记刚才的问题 | 保留最近至多 120 秒的实际缓存，继续 10 秒；与正在采集的片段合并，合并后的尾部最长 60 秒 |
| 开始复现采集 | EXE 核验完成、窗口关联后，倒计时 5 秒，再记录 20 秒；可以提前停止并保存 |
| 重复触发 | 自动触发及采集异常重试冷却 30 秒；连续 fresh 状态只取上升沿。同场景不会在 30 秒专项结束后反复开启 |
| 任务与副本 | 首期保留基础事件和性能摘要；任务、房间、结算专项后续接入 |
| 登录与频道 | 当前覆盖登录器连接阶段及频道检查；游戏内切换频道与网络收发事件后续接入 |
| 卡顿与退出 | CPU/内存趋势、窗口响应与退出码；帧时间及异常模块事件后续接入 |

“候选输入变化”是定位片段的条件。最终是否存在故障由输入、描述符、动作、占用和距离证据共同分析。
只有基础记录的历史窗口保留基础数据；专项开始前的高频样本不会补造。

## 进程身份与后台执行

- `Process.Start` 返回的进程立即登记：PID、创建 FILETIME、规范 EXE 路径、Windows 用户会话和随机诊断会话号。
- 开关开启时发现本客户端目录的 `DNF.exe`。恢复登记须完整匹配路径、创建时间和 Windows 会话；多个新窗口先选择。
- 两个游戏各有缓存、序号及片段。重开登录器生成新诊断会话号；退出码为 259 的已退出进程也能正确结束。
- 私有 `processes.json` 仅用于本机关联；窗口标题也仅用于选择。两者均不进入导出包。
- 后台核验实际 EXE 完整 SHA256、PE32 与模块基址，再启用专项。PVF 单独读取完整 SHA256；缺失时摘要标明 `pvf_hash_unavailable`，期望摘要另列 `expected_pvf_sha256`。
- 采样线程使用进程内高精度 waitable timer；系统不支持时使用普通等待并记录实际间隔、丢样和降频。连续五次迟到会逐步放慢采样，最高 100 ms。
- 采样、哈希、压缩写入和 UI 通知分开执行。UI 标准输出管道阻塞不会卡住采样；导出不占用登录/更新操作锁。
- 游戏读取句柄权限为 `PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_VM_READ`（`0x1010`）。模块枚举用 Toolhelp；采集器没有写内存、注入、调试提权或全局键盘钩子。
- 登录器关闭通过 Host 的 stdin EOF 结束所有会话并等待后台队列写完。UI 不等待压缩线程退出。

## 字段与版本

布局模块：`at5t-readonly-1`。完整 EXE 大小均为 `111812608` 字节。

| 版本 | EXE SHA256 |
| --- | --- |
| V135 | `8de790c1974fe74d3b74ec6095b7281ea337759a4adcdfebdd874bf67a94126f` |
| V141 | `26abf551753c2745c2e5b349fb24336422a189187ff847c995e0df9a68871079` |

其他摘要继续记录基础日志，专项显示“不支持当前版本”。地址按实际模块基址重定位。

| 字段组 | 读取依据及含义 |
| --- | --- |
| `identity.*` / `actor.address` | 本地 primary、override、VirtualCharacter；override 读取失败时不回退 primary |
| `actor.*` | 场景、虚表及三个入口、ride、ride_lock、保护动作；保护字段及密钥链二次读回，密钥不输出 |
| `descriptor.*` | 角色 `+3DE0` 的描述符、使能 `+30`、状态 `+2C`、mode `+28`，角色 input 注册关系 |
| `rule.*` | 描述符 `+4/+8` 的 28 字节规则行；嵌套逻辑键向量、`condition_10` 原始条件值、有符号 `minimum_duration`、`time_window`；按游戏规则顺序输出 |
| `input.*` | 角色独立 input `+643C`、回退源、四个过滤等级 `+4C..58`、过滤旁路 `+84`；过滤等级不是队列长度 |
| `logical4.*` | 游戏输入记录中的 logical 4 行；原行序号、time `+4`、held_duration `+8`、down `+C`、fresh `+D` |
| `vehicle.*` | 当前场景至多 8 个 61235 对象；owner/rider、交互状态、锁、模式、动作与位置 |
| `*.x/y/z` | physics `+B8` → 位置向量 → 已验证 `0288E950` getter 的浮点位置；委托 physics 或未知 getter 明确为 unsupported |
| `*.distance_xy/scaled` | 根据同次稳定采样的实时位置计算欧氏距离及 Y 比例校正距离；比例来自 `057C2918`。这是计算值；游戏内部整数舍入后的 distance 调用结果单列 unsupported |

对象槽位/标记、身份指针、向量头、嵌套规则内容、physics 坐标和保护字段均有稳定性检查。变化中的合成快照标为 `unstable`，成功数值改为 null。对象及规则变化通过完整/差量快照的序号与时间定位。场景列表每 250 ms 或身份变化时刷新；高频样本不反复输出不变对象身份。

字段边界：场景 4096 槽、最多 8 辆载具、规则 32 行、每规则 32 逻辑键、注册 4096 行、硬件输入 1024 行、最多 32 条 logical4 行。超过边界记录失败或限制原因。

来源：ServerS5A3 已审阅只读采样器 `87861a0`（上游合并 `8ead65a`）；V135 原生规则/physics 契约与 V141 代码窗口对照。新增位置 getter、虚表和舍入函数窗口另外完成 V135/V141 逐字节比对。字段端口实现自包含于 `launcher/Engine/Diagnostics/`，构建不读取相邻游戏源码。

## 预算与保留

| 对象 | 上限 |
| --- | --- |
| 每游戏环形缓存 | 2 MiB（低于需求的 8 MiB 上限），最近至多 120 秒 |
| 每游戏正在保存的片段 | 3 MiB，达到上限时完整结束并标记 `capture_budget` |
| 登录器未启动游戏的会话 | 环 256 KiB、片段 512 KiB |
| 写入队列 | 2 个等待片段 + 1 个写入中的片段；普通采样不等待队列 |
| 总保留缓存 | 最坏计账 19.75 MiB；其余空间留给解析、序列化及有界命令，共受 32 MiB 设计预算约束 |
| 单行 | 65535 字节；过大时写入完整 dropped 事件并要求下一次完整快照 |
| 导出 ZIP | 10 MiB；按完整片段的未压缩长度保守选取，优先较新片段，在 summary 列出省略片段 |
| 本地保存 | 最多 10 份、100 MiB、7 天；写入后与每 30 秒清理一次 |

计账包含行对象与引用预留。32 MiB 是诊断缓存预算，区别于整个 .NET/Flutter 进程工作集。
磁盘失败和队列满有状态与原因，已导出到其他目录的副本不参与清理。临时 ZIP 写完且关闭后才改为最终名称。

## ZIP / JSONL v1

```text
manifest.json
summary.json
session.jsonl
incidents/<incident-id>/summary.json
incidents/<incident-id>/events.jsonl
incidents/<incident-id>/samples.jsonl
```

本地单片段包的摘要直接位于根目录，导出多片段包将每份摘要放入其 incident 目录，根摘要汇总。
manifest 覆盖除自身以外的全部文件，记录大小、SHA256、schema/collector/module 版本、会话、时间和结束原因。导出前逐文件验证本地摘要。

JSONL [机器可读 schema](diagnostics/record-v1.schema.json)：

- `sequence`：会话内递增；`utc` 和 `monotonic_ms` 同时记录。
- `type` / `module` / `session_id`：事件、身份、性能或样本及来源。
- `read_ms`、`quality` 和 `timing.interval_ms`：实际读取耗时、质量、实际间隔；目标间隔独立记录。
- `fields.<name> = {value, status, reason}`：正常 0 是 `ok`；不适用、不支持、读取失败、不稳定分别使用不同 status，数值为 null。
- 样本 `reason=full` 重置状态；后续 `delta` 的 `base_sequence` 指向最近的完整快照，差量依次应用。删除字段表示为 `not_applicable / field_removed`。至少每秒补全量；覆盖环缓存时丢掉失去基准的差量链。
- summary 的错误/丢样/覆盖计数为会话累计，`counter_scope=session_cumulative`。片段实际记录数、起止范围、截断与缺失字段单列。
- `correlation_id` 预留诊断会话号；服务器时钟估计和误差为 null，`server_events_connected=false`。状态采样的 `observation_kind=client_polling` 与函数执行事件分开。

仅输出结构化字段白名单。账号不写入，以会话编号定位；密码、Token、完整启动参数、聊天、安装路径、原始异常文本和请求对象都不导出。

```powershell
python scripts/analyze_diagnostics.py docs/diagnostics/diagnostics-synthetic.zip
```

分析器验证清单、完整 JSONL、会话/序号、差量基准与敏感字段，输出关键时间线和缺失清单。[示例包](diagnostics/diagnostics-synthetic.zip) 全部由合成数据生成。

## 扩展接线

Flutter 设置保存 `diagnostics: {enabled, problemType}`。Host 通过现有 stdin RPC 提供 `diagnosticSelect`、`diagnosticMark`、`diagnosticReproduce`、`diagnosticStop`（`sessionId`），以及 `diagnosticExport`（`sessionId`、1–10 个 `incidentIds`、native picker 返回的 `directory`）。

新增模块须声明摘要白名单、字段来源/边界、稳定性保护、采样频率、触发/冷却/停止条件及测试。诊断模块和布局随登录器一起发行。服务器事件后续按会话/请求关联编号接入，不从客户端相邻快照推导同一帧的调用顺序或奖励落库事实。
