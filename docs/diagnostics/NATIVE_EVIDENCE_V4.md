# 原生异常证据契约

适用 1.0.7 源码候选：外层 `schema_version=4`、记录 `record_version=3`、诊断同意 `consentVersion=2`。此前的外层 2/3 保留读取与导出能力；反作弊同意版本保持独立。玩家操作仍为匿名活动计数，业务消息关联和 dump 尚未实现。

## 异常、寄存器与栈

`evidence.native.exception` 保存原始 code、flags、first_chance、thread_id、异常地址、异常链地址、参数数量及至多 15 个参数。C0000005/C0000006 参数足够时，将参数 0 的 0/1/8 解释为 read/write/execute，参数 1 为 fault_address；C0000006 参数 2 为 io_status。未知访问方式标记 unknown，参数不足不解释；损坏的参数数量标记 invalid_count_truncated。只记录异常链地址，不读取链指向的对象。

每个阶段有独立 `exception_id`。`related_exception_id` 是同线程最近相邻、两秒内、原始记录与栈指针一致的关联提示，`correlation_status=adjacent_same_thread_signature` 明示依据。它不会合并两条记录，也不是操作系统提供的异常唯一标识。下一次同线程异常会替换候选；线程退出清理候选。每个观察器独立保留最多 16 次首轮和 2 次未处理通知。

`context` 在 StackWalk 改写缓冲前保存 x86 的 EIP/ESP/EBP/EAX/EBX/ECX/EDX/ESI/EDI/EFlags，或 x64 的 RIP/RSP/RBP/RAX/RBX/RCX/RDX/RSI/RDI/R8–R15/RFlags。附带 architecture、pointer_bits、context_flags、context_status、win32_error 和耗时。

原生地址、基址、RVA、寄存器、参数均为固定宽度十六进制字符串：32 位八位，64 位十六位，例如 `0xFFFFFFFFFFFFFFFF`。异常 code/flags 和页保护常量为八位。WOW64 中宿主 64 位模块按其自身地址宽度记录。旧 AT-5T 32 位专项数值契约保持，通用原生证据不经 double 转换。

`evidence.stack` 至多 64 帧，每帧包括绝对地址、模块 ID、安全文件名和 RVA；未知模块使用 unmapped 并保留绝对地址。首帧始终为异常地址。`unwind` 区分正常 end_of_stack、重复帧、API 失败、帧上限、时间上限、context 失败和符号初始化失败。栈失败仍保留其他证据。

## 页属性、代码与模块

- 最多查询六个候选：fault address、异常 IP、SP，以及明确标识的 EDI/ESI/EAX 或 RDI/RSI/RAX。相同区域合并来源；只保存 VirtualQueryEx 属性，不读取任意堆数据。free 与 query_failed 不同，失败记录 Win32 错误码。
- IP 位于已加载模块且页面可读时，读取自 IP 起最多 64 字节代码。限制在模块和已验证可读区域内，边界处报告 partial，保存请求/实际长度、起始地址、RVA、模块 ID 及读取状态。堆上执行地址报告 outside_module。
- 模块加载/卸载有独立事件，模块 ID 包含观察器代次，避免重新关联或基址复用混淆。每观察器最多跟踪 512 次加载；异常中附带最多 16 个引用模块的缓存身份，超出标明 reference_limit，完整模块事件可按 ID 关联。
- 模块名、版本和 SHA256 由后台处理调试事件给出的文件句柄，来源标为 debug_event_disk_file；SHA 描述磁盘文件，代码窗口描述运行内存。哈希状态明确 pending、ok、file_unavailable、read_failed、queue_limit、size_limit、time_limit 或 cancelled。模块尚未缓存时保留 unknown，不同步等待哈希。
- 文件哈希上限 512 MiB、每任务检查 10 秒预算；模块元数据队列最多 32 项。加载地址、大小、架构和 PE timestamp 来自有界的已加载 PE 头读取。后台文件 I/O 不在异常捕获线程运行。

基础性能增加系统 commit total/limit、进程 virtual size、PE 架构和 LAA；API 失败显示对应原因和可取得的系统错误码。EXE/PVF 完整 SHA256、预期 PVF、发布序号和 standard/anticheat 通道保存在汇总中。地址空间最大空闲区扫描尚未实现。

## 暂停、容量与格式

每次异常输出 context/pages/code/unwind 阶段耗时、ContinueDebugEvent 前后的耗时和 `pause_ms`。栈循环检查 150 ms，整体在开始下一可选读取前检查 250 ms，超限记录 `budget_exceeded`。Windows 同步 API 单次调用不能被这些检查中断，因此 250 ms 是降级预算，不是暂停时长的硬保证。符号初始化在调试关联前完成；序列化、管道写入和模块哈希在后台完成。异常继续交给游戏，观察器脱离不结束游戏。

管道两端为 FIFO 有界队列：普通消息最多占 128 个槽位，异常/状态可用保留空间，终止异常另有八个保留槽位。落盘为普通记录保留 512 KiB 给关键尾部，再分别预留未处理异常、退出和汇总容量。每文件仍不超过 10 MiB；每批 512 KiB、每行 64 KiB，省略计入 dropped/truncated。模块身份附带数量受限以保护原始异常现场。

新头尾必须同为版本 4，中间每行显式标记记录 3。升级时旧流封存，新格式建立新日志段；旧流不会混入新字段。最近五份清理规则继续作用于这些日志段。严格 Schema 见 [record-v3](record-v3.schema.json) 和 [envelope-v4](envelope-v4.schema.json)。

分析器的 `critical_events` 包含所有已记录异常阶段、退出和捕获失败，保持原顺序；普通上下文最多 100 个事件，保留首尾及异常附近窗口，并给出遗漏数量和范围。首轮不等同崩溃；`outcome.unhandled_exceptions` 只统计最终未处理通知。后续 observer_failed 不自动归因。`overwritten` 是环形缓存覆盖累计，不是导出缺行数。

## 复现入口

```powershell
python -m pip install -r scripts/diagnostics/requirements.txt
python scripts/test_diagnostics_analysis.py
python scripts/test_native_diagnostics.py --dotnet <dotnet.exe> --output <新的隔离目录>
python scripts/test_diagnostics_host.py --exe <NativeAOT核心> --fixture-exe <合成DNF.exe> --native-crash-fixture --output <新的隔离目录>
```

公开回归使用合成程序与日志。[外层 4 合成日志](diagnostics-native-synthetic.log) 展示 NULL read 的首轮、未处理和退出三条记录，全部地址、寄存器和哈希均为构造数据。玩家原始日志、配置和游戏资源保持私有；分享时保留附件可见范围说明。
