# 增强诊断验证记录

2026-10-20，Windows 本地候选 0.2.19。基线是仓库提交 `a1ca94aba4a82d628796410e405ea6eff6f95fc3`；实现位于分支 `feature/launcher-diagnostics-20261020`。
本轮未推送、未发布签名清单、未部署服务。

## 自动化与原生读取

| 检查 | 结果 |
| --- | --- |
| Core | 155 项通过 |
| EngineTests | 274 项通过，含诊断新增的默认开关、布局/保护字段/规则/实时坐标、缓存链、导出、清理、磁盘故障和进程生命周期 |
| PortableTests | 14 项通过 |
| Flutter | analyze 通过；21 项 widget 测试通过，包含开关持久化、窗口/片段选择、导出、错误隔离与 1000×620 面板 |
| Windows RPM | 受控子进程读取成功；路径/创建时间不匹配被拒绝，退出码 259 正确识别，限定权限可读取性能摘要 |
| 双实例 | 两个临时 `DNF.exe` 子进程分别绑定和保存；窗口选择跨扫描稳定，重复标记合并，重开恢复为新会话；退出一个不影响另一个 |
| ZIP | 全部 payload 的 SHA256/大小逐项验证；损坏片段被拒绝，保留完整片段的导出上限检查通过；临时 ZIP 清理通过 |
| NativeAOT Host | 独立状态目录下，真实 NativeAOT 核心关联临时游戏子进程并识别退出；经 stdin RPC 验证默认关闭、开启、标记/停止、导出、倒计时/提前停止、关闭与 EOF 保存；未调用生产网络 |
| 构建 | NativeAOT Core、Windows Flutter、Bootstrap 与便携 EXE 构建通过 |
| 图像检查 | 检查 widget 真实渲染截图：文字、控件、导出回执与滚动面板；原生目录选择窗口手工验收待完成 |

双实例使用测试程序自建的 Windows 进程及内存，没有操作玩家游戏、角色或数据库。PID 复用通过创建时间不匹配检查覆盖；未人为促使 Windows 重用某个指定 PID。

## 性能结果

证据：[native-performance.json](native-performance.json)。受控 Windows 子进程提供 AT-5T 合成内存；采集侧实际执行 `ReadProcessMemory`、解析、JSONL 序列化及环形缓存。

| 模式 | 实测时长 | 样本 | 采集进程 CPU 时间 | 实际间隔 P50 / P95 | 读取与处理 P50 / P95 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 关闭 | 10.00 s | 0 | 0.0469 s | — | — |
| 基础 | 10.01 s | 10 | 0.0156 s | 1001.24 / 1001.63 ms | 0.914 / 1.136 ms |
| 专项 | 30.00 s | 5015 | 3.5625 s | 6.015 / 6.243 ms | 0.628 / 0.868 ms |

专项 CPU 时间折合约单核 11.9%；这不是整机 CPU 百分比。采集测试进程工作集约 65.5 MiB；目标子进程约 24.4 MiB。两者包含运行时，其值不等于诊断缓存占用。
短基础测试的 CPU 计时精度不足以比较关闭前后的小差异。目标进程为空闲受控程序，不能由此推断真实游戏的帧时间变化。

最初 `Task.Delay(5)` 实测中位间隔约 15.5 ms。改用独立 waitable timer 后得到上述结果；没有设置系统全局定时器分辨率。

## 容量压力

[logical-two-hour.json](logical-two-hour.json)：双实例、两小时逻辑时间、每五分钟一次 30 秒专项，共 300960 条记录。

- 两个环形缓存峰值计账合计 4194304 字节，达到 4 MiB 预算后覆盖旧数据。
- 两个游戏的环/片段、登录器会话及最多三个待写/正在写片段，最坏保留数据计账 20709376 字节（19.75 MiB）。
- 过大导出按完整片段选择；被省略片段在根摘要列出，JSONL 保持完整。
- 十份/七天清理、导出副本保留、磁盘失败和有界事件队列饱和均有回归检查。

该测试用加速逻辑时钟执行，实耗约 5.26 秒。**真实游戏连续两小时运行、开关前后 CPU/内存趋势及帧时间分布待实机验收。**

## 示例与复现

[diagnostics-synthetic.zip](diagnostics-synthetic.zip) 是合成示例；[synthetic-analysis.json](synthetic-analysis.json) 是分析器的清单验证、时间线与缺失字段结果。

```powershell
dotnet run --project launcher/EngineTests -c Release
dotnet run --project launcher/EngineTests -c Release -- --diagnostic-benchmark runtime/diagnostics-benchmark
python scripts/analyze_diagnostics.py docs/diagnostics/diagnostics-synthetic.zip
python scripts/test_diagnostics_host.py --exe dist/<bundle>/app/DNF90.Core.exe --output runtime/native-host-check
```

部署沿用现有交付链：度盘上传、全量读回验哈、artifacts 映射加载、实际 307/供应方 206、真实下载器 APP 与全部资源下载、二次计划为零。候选包当前只在本地生成；正式发布时再执行这组门槛。
