import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'launcher_controller.dart';

class DiagnosticsPanel extends StatefulWidget {
  const DiagnosticsPanel({super.key, required this.model});
  final LauncherController model;
  @override
  State<DiagnosticsPanel> createState() => _DiagnosticsPanelState();
}

class _DiagnosticsPanelState extends State<DiagnosticsPanel> {
  String exported = '', pickerError = '';
  bool choosing = false, consenting = false;

  Future<void> toggle(bool enabled) async {
    if (consenting) return;
    if (enabled) {
      consenting = true;
      final agreed = await showDialog<bool>(
        context: context,
        barrierDismissible: false,
        builder: (context) => AlertDialog(
          title: const Text('开启增强诊断日志'),
          content: const SingleChildScrollView(
            child: Text(
              '开启期间会收集登录器和游戏的异常码、异常调用栈、故障地址、寄存器、内存页属性、模块版本与摘要，'
              '以及异常位置最多 64 字节的模块代码。还会记录游戏窗口处于前台时的玩家操作：'
              '方向键、按键次数、鼠标点击、移动与滚轮活动和焦点变化。\n\n'
              '其他按键合并为次数，不记录按键文字、聊天内容、密码或 Token。'
              '日志只保存在本机，保留最近 5 次，可由你导出 .log 分享；不会自动上传。\n\n'
              '异常采集会以调试方式关联游戏进程，异常发生时短暂暂停进程以读取调用栈。'
              '权限不足时可点击提升诊断权限，由 Windows 弹出 UAC 确认。'
              '关闭开关或退出登录器后停止采集。',
            ),
          ),
          actions: [
            TextButton(
              key: const Key('diagnostics-decline'),
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              key: const Key('diagnostics-consent'),
              onPressed: () => Navigator.pop(context, true),
              child: const Text('同意并开启'),
            ),
          ],
        ),
      );
      consenting = false;
      if (agreed != true || !mounted) return;
    }
    await widget.model.diagnosticPreferences(
      enabled: enabled,
      consent: enabled ? 2 : null,
    );
  }

  String date(Object? value) {
    final time = DateTime.tryParse('$value')?.toLocal();
    if (time == null) return '';
    String pad(int n) => '$n'.padLeft(2, '0');
    return '${time.month}/${time.day} ${pad(time.hour)}:${pad(time.minute)}:${pad(time.second)}';
  }

  String size(num bytes) => bytes >= 1048576
      ? '${(bytes / 1048576).toStringAsFixed(1)} MiB'
      : '${(bytes / 1024).toStringAsFixed(1)} KiB';

  Future<void> export(Map<String, dynamic> log) async {
    if (choosing || widget.model.diagnosticBusy) return;
    setState(() {
      choosing = true;
      pickerError = '';
      exported = '';
    });
    try {
      final time =
          DateTime.tryParse('${log['startedUtc']}')?.toLocal() ??
          DateTime.now();
      final stamp = time
          .toIso8601String()
          .split('.')
          .first
          .replaceAll(':', '-');
      final path = await const MethodChannel('dnf90.preview/window')
          .invokeMethod<String>('saveDiagnosticLog', {
            'fileName': 'PurpleMagic-$stamp.log',
          });
      if (path == null || path.isEmpty || !mounted) return;
      final reply = await widget.model.diagnosticAction('diagnosticExport', {
        'sessionId': log['sessionId'],
        'path': path,
      });
      if (mounted && reply?['diagnosticExport'] is Map) {
        setState(() => exported = '已保存：${reply!['diagnosticExport']['path']}');
      }
    } catch (_) {
      if (mounted) setState(() => pickerError = '无法打开另存为窗口，请重试。');
    } finally {
      if (mounted) setState(() => choosing = false);
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.model,
    builder: (context, _) {
      final model = widget.model;
      final status = model.diagnostics;
      final logs = (status['clips'] as List? ?? [])
          .cast<Map<String, dynamic>>()
          .take(5);
      final active = (status['instances'] as List? ?? [])
          .cast<Map<String, dynamic>>()
          .map((row) => row['id'])
          .toSet();
      final busy = model.diagnosticBusy || choosing;
      return ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.sizeOf(context).height - 100,
        ),
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  const Expanded(
                    child: Text('增强诊断日志', style: TextStyle(fontSize: 20)),
                  ),
                  IconButton(
                    tooltip: '关闭诊断面板',
                    onPressed: () => Navigator.pop(context),
                    icon: const Icon(Icons.close_rounded),
                  ),
                ],
              ),
              SwitchListTile(
                key: const Key('diagnostics-toggle'),
                contentPadding: EdgeInsets.zero,
                title: const Text('开启增强诊断'),
                subtitle: const Text(
                  '按每次游戏启动记录，保留最近 5 次。',
                  style: TextStyle(fontSize: 12),
                ),
                value: model.diagnosticsEnabled,
                onChanged: busy || model.busy || !model.connected
                    ? null
                    : toggle,
              ),
              SizedBox(
                height: 36,
                child: Row(
                  children: [
                    if (busy)
                      const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    else
                      Icon(
                        model.diagnosticsEnabled
                            ? Icons.radio_button_checked
                            : Icons.radio_button_unchecked,
                        size: 16,
                        color: const Color(0xffb7a9dd),
                      ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        status['message'] as String? ?? '已关闭',
                        style: const TextStyle(fontSize: 12),
                      ),
                    ),
                  ],
                ),
              ),
              const Divider(height: 24),
              for (final instance
                  in (status['instances'] as List? ?? [])
                      .cast<Map<String, dynamic>>())
                if (instance['crashStatus'] == 'access_denied' ||
                    instance['crashStatus'] == 'elevation_cancelled')
                  ListTile(
                    contentPadding: EdgeInsets.zero,
                    title: const Text(
                      '异常栈采集需要额外权限',
                      style: TextStyle(fontSize: 13),
                    ),
                    subtitle: const Text(
                      '确认 Windows UAC 后，为本次游戏采集异常栈。',
                      style: TextStyle(fontSize: 12),
                    ),
                    trailing: TextButton(
                      onPressed: busy
                          ? null
                          : () => model.diagnosticAction('diagnosticElevate', {
                              'sessionId': instance['id'],
                            }),
                      child: const Text('提升诊断权限'),
                    ),
                  )
                else if (instance['crashStatus'] != 'recording' &&
                    instance['crashStatus'] != 'stopped')
                  Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: Text(
                      instance['crashStatus'] == 'starting' ||
                              instance['crashStatus'] == 'elevating'
                          ? '正在关联异常栈采集器…'
                          : '本次游戏的异常栈采集未就绪，基础日志仍在记录。',
                      style: const TextStyle(fontSize: 12),
                    ),
                  ),
              const Text('最近的启动日志', style: TextStyle(fontSize: 14)),
              const SizedBox(height: 10),
              Flexible(
                child: SingleChildScrollView(
                  child: Column(
                    children: [
                      if (logs.isEmpty)
                        const Padding(
                          padding: EdgeInsets.symmetric(vertical: 24),
                          child: Text(
                            '开启后启动游戏，日志会显示在这里。',
                            style: TextStyle(fontSize: 12),
                          ),
                        )
                      else
                        ...logs.map(
                          (log) => Container(
                            key: ValueKey('log-${log['id']}'),
                            margin: const EdgeInsets.only(bottom: 8),
                            padding: const EdgeInsets.symmetric(
                              horizontal: 14,
                              vertical: 12,
                            ),
                            decoration: BoxDecoration(
                              color: const Color(0xffb7a9dd)
                                  .withValues(alpha: .07),
                              borderRadius: BorderRadius.circular(12),
                            ),
                            child: Row(
                              children: [
                                const Icon(
                                  Icons.article_outlined,
                                  size: 22,
                                  color: Color(0xffb7a9dd),
                                ),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      Text(
                                        date(log['startedUtc']),
                                        style: const TextStyle(fontSize: 13),
                                      ),
                                      const SizedBox(height: 4),
                                      Text(
                                        '${active.contains(log['sessionId']) ? '记录中' : '已保存'} · ${size(log['bytes'] as num? ?? 0)}',
                                        style: const TextStyle(
                                          fontSize: 11,
                                          color: Color(0xffb3aebf),
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                                TextButton.icon(
                                  key: ValueKey('export-${log['id']}'),
                                  onPressed: busy ? null : () => export(log),
                                  icon: const Icon(
                                    Icons.save_alt_rounded,
                                    size: 16,
                                  ),
                                  label: const Text('导出'),
                                ),
                              ],
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              ),
              if (exported.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 10),
                  child: SelectableText(
                    exported,
                    style: const TextStyle(
                      fontSize: 11,
                      color: Color(0xff9ac9b2),
                    ),
                  ),
                ),
              if (model.diagnosticError.isNotEmpty || pickerError.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 10),
                  child: Text(
                    '${model.diagnosticError} $pickerError'.trim(),
                    style: const TextStyle(
                      fontSize: 12,
                      color: Color(0xffffb4ab),
                    ),
                  ),
                ),
            ],
          ),
        ),
      );
    },
  );
}
