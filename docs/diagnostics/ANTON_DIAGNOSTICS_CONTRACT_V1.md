# 安徒恩首进队友可见性诊断契约（Contract v1）

适用版本：**登录器 1.0.11 起**  
契约名称：`purplemagic-anton-first-entry-diagnostics`（`schema_version = 1`）  
Schema 文件：[contract-v1.schema.json](contract-v1.schema.json)

## 1. 设计分层与职责边界

PurpleMagic 登录器诊断系统现规范为两层正交能力，互不混写、各自遵循严格 Schema：

1. **Contract v1 进图会话与可见性诊断包（`.zip`，默认轻量常驻 P0）**：
   - 无需开启“增强诊断”调试器、无需 UAC 提权、无需修改或重启游戏服务端。
   - 按每次客户端实例启动建立独立 `client_session_id`（双开/多开按 `(pid, process_created_utc)` 与 `launch_instance_id` 严格隔离）。
   - 记录会话身份、客户端与 PVF 磁盘真实 SHA-256（与发布清单声明摘要分开记录）、Win32 `iphlpapi` 进程级 TCP/UDP 套接字归属生命周期、跨端时钟锚点、角色绑定、进图轮次链（`entry_id` / `parent_entry_id` / `scene_epoch`）以及玩家一键标记的队友可见性观察。
   - 通过原生另存为导出原子 `.zip` 诊断包。
2. **增强诊断日志（`.log`，`envelope-v5` / `record-v4`，需用户显式 `consentVersion = 3` 同意开启）**：
   - 采集原生 SEH 异常栈、寄存器、模块窗口、前台匿名输入计数及 AT-5T 专项采样。
   - 当增强诊断已开启且存在已完成刷新的 `.log` 时，导出 Contract v1 `.zip` 诊断包会同时纳入 `enhanced/<session_id>.log` 并在 `manifest.json` 的 `files` 中登记完整字节数与 SHA-256。

## 2. 导出包目录结构

```text
PurpleMagic-<incident_id>.zip
├── manifest.json
├── integrity.json
├── sessions/
│   └── <client_session_id>/
│       ├── launcher-events.jsonl
│       └── observations.jsonl
└── enhanced/                       # 可选：仅当该会话存在完整增强诊断日志时包含
    └── <enhanced_session_id>.log
```

## 3. 公共事件头与时序契约

`launcher-events.jsonl` 与 `observations.jsonl` 的每一行均包含以下 10 个公共必填字段：

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `schema_version` | `integer` | 固定为 `1` |
| `event` | `string` | 事件名（见第 4 节） |
| `source` | `string` | `"launcher"`（预留 `"packet_capture"` / `"native_client"`） |
| `client_session_id` | `string` | 本次游戏实例唯一会话编号 |
| `event_seq` | `integer` | **事件生成时刻**按会话原子递增（`Interlocked.Increment`），不由后台落盘线程重排 |
| `observed_utc` | `string` | ISO-8601 UTC（`Z` 结尾），用于与服务端日志及同队队友跨机对齐 |
| `mono_ticks` | `integer` | 本机 `QueryPerformanceCounter` 原始 tick，仅用于同机精细间隔计算 |
| `mono_frequency_hz` | `integer` | 本机 `QueryPerformanceFrequency` 原始频率，禁止跨机器直接比较 `mono_ticks` |
| `pid` | `integer\|null` | 游戏进程 PID |
| `process_created_utc` | `string\|null` | 游戏进程创建 UTC 时间，与 `pid` 共同防御 Windows PID 复用误判 |

除上述必填字段外，每条登录器事件还携带 `incident_id`、`thread_id` 与 `launch_instance_id`。

## 4. P0 已实现事件与能力声明

| 事件名 | 说明 |
| --- | --- |
| `session.start` | 记录登录器版本、Git Commit、构建风味、协议配置、服务器端点、启动移交方式（`direct_child` / `anticheat_guard_child` / `discovered_process`）及关联 HTTP 追踪头（不含凭据） |
| `process.started` | 记录脱敏后的可执行路径（`%USERPROFILE%` 折叠）与路径 SHA-256、磁盘实际 `client_file_sha256` / `pvf_file_sha256` 及字节数、清单声明的 `manifest_client_sha256` / `manifest_pvf_sha256` |
| `character.associated` | 记录本端角色名/角色编号绑定，标明 `association_source`（`authenticated_server_context` 或 `user_selected`） |
| `scene.entry` | 记录进图轮次 `entry_id`、重进父级 `parent_entry_id`、`scene_epoch` 及已知的副本/地图/房间/实例标识 |
| `connection.open` / `connection.close` | 基于 Win32 `GetExtendedTcpTable` / `GetExtendedUdpTable`（采集前后双重校验进程创建时间）记录该 `DNF.exe` 的 TCP 连接流、同远端重连 `previous_stream_id` 及本地 UDP 端口绑定（无伪造包计数，显式标注 `bound_local_only` 与 `unknown_reason`） |
| `observation.reported` | 记录玩家上报的 `missing`、`late_appeared`、`appeared`、`recovered_after_reentry`，保留点击时刻 `observed_utc` 与补报估计时刻 `estimated_onset_utc` + `time_uncertainty_ms` |
| `clock.anchor` | 记录本机 UTC 偏移、HTTP `Date` 响应头锚点或受认证服务端时钟偏差与不确定度 |
| `process.exited` | 记录进程退出码、十六进制状态码、退出分类（正常退出 / 启动早退 / 异常崩溃码 / 服务端业务断线）与持续时长 |
| `trace.integrity` | 记录队列丢弃、关键事件丢失、超长截断、环形缓存淘汰计数、单会话字节预算占用以及原生/抓包能力状态 |

### 真实能力边界（不猜测、不伪造）

当前版本为 **P0 纯登录器侧实现**：
- `capabilities.launcher_sessions = true`
- `capabilities.manual_observations = true`
- `capabilities.game_socket_ownership = true`
- `capabilities.client_side_packet_capture = false`（`client_side_packet_capture_not_enabled`）
- `capabilities.application_frames = false`（`application_frame_decoder_not_installed`）
- `capabilities.native_actor_events / native_registry_events / native_context_events / native_render_events = false`（`launcher_only_no_client_event_provider`）

所有未知或未接入字段一律输出 `null` 加明确的 `*_unknown_reason`；`capability = false` 仅代表当前未安装对应探针，绝不代表游戏内部函数执行失败。

## 5. 资源预算与隐私排除

- **环形窗口与预算**：常规事件保留触发前 120 秒滑窗，标记观察或关联 `incident_id` 后锁定前置窗口并继续保留后续 60 秒；单会话上限 **32 MiB**（`33,554,432` 字节），其中预留 **256 KiB** 专供关键事件（`session.start`、`process.started`、`character.associated`、`scene.entry`、`observation.reported`、`process.exited`、`trace.integrity`）；本地最多保留 **5 个**事件组。
- **严禁记录的敏感信息**：密码、`login_token`、`Authorization`、登录请求正文、含敏感 token 的完整命令行、聊天正文、原始用户目录隐私路径。

## 6. 校验与离线分析

```powershell
python scripts/analyze_diagnostics.py "PurpleMagic-inc-xxxx.zip"
```

分析器校验 `manifest.json` 全部必填项、能力原因、每个成员文件的字节数与 SHA-256、`integrity.json`、敏感字段黑名单、`event_seq` 严格单调性、`observations.jsonl` 与主事件流一致性，以及附带的 `enhanced/*.log`。
