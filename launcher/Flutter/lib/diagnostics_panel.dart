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
  String? instanceId, exportSession;
  final selected = <String>{};
  String exported = '', pickerError = '';
  bool choosing = false;
  static const types = {
    'auto': '自动',
    'quests': '任务与副本',
    'interaction': '交互与载具',
    'connection': '登录与频道',
    'performance': '卡顿与退出',
  };
  static const categories = {
    'basic': '启动与事件',
    'performance': '性能摘要',
    'at5t': 'AT-5T 状态',
  };
  String date(Object? value) {
    final time = DateTime.tryParse('$value')?.toLocal();
    if (time == null) return '';
    return '${time.month}/${time.day} ${time.hour.toString().padLeft(2, '0')}:${time.minute.toString().padLeft(2, '0')}:${time.second.toString().padLeft(2, '0')}';
  }

  String size(num bytes) => bytes >= 1048576
      ? '${(bytes / 1048576).toStringAsFixed(2)} MiB'
      : '${(bytes / 1024).toStringAsFixed(1)} KiB';
  Future<void> export() async {
    if (choosing || selected.isEmpty || exportSession == null) return;
    setState(() {
      choosing = true;
      pickerError = '';
      exported = '';
    });
    try {
      final path = await const MethodChannel('dnf90.preview/window')
          .invokeMethod<String>('chooseDirectory', {
            'purpose': 'diagnosticExport',
          });
      if (path == null || path.isEmpty || !mounted) return;
      final reply = await widget.model.diagnosticAction('diagnosticExport', {
        'sessionId': exportSession,
        'incidentIds': selected.toList(),
        'directory': path,
      });
      if (mounted && reply?['diagnosticExport'] is Map) {
        final result = reply!['diagnosticExport'] as Map;
        setState(
          () => exported =
              '已导出 ${result['incidents']} 个片段 · ${size(result['bytes'] as num)}\n${result['path']}',
        );
      }
    } catch (_) {
      if (mounted) setState(() => pickerError = '导出目录未打开，请重试。');
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
      final instances = (status['instances'] as List? ?? [])
          .cast<Map<String, dynamic>>();
      final clips = (status['clips'] as List? ?? [])
          .cast<Map<String, dynamic>>();
      if (!instances.any((r) => r['id'] == instanceId)) {
        instanceId =
            instances
                    .where((r) => r['pid'] != 0 && r['selected'] == true)
                    .firstOrNull?['id']
                as String? ??
            instances.firstOrNull?['id'] as String?;
      }
      final instance = instances
          .where((r) => r['id'] == instanceId)
          .firstOrNull;
      final sessionIds = clips.map((c) => c['sessionId'] as String).toSet();
      if (!sessionIds.contains(exportSession)) {
        exportSession = sessionIds.firstOrNull;
        selected.clear();
      }
      final visible = clips
          .where((c) => c['sessionId'] == exportSession)
          .toList();
      selected.removeWhere((id) => !visible.any((c) => c['id'] == id));
      final busy = model.diagnosticBusy || choosing;
      final canRecord =
          model.diagnosticsEnabled && instance?['selected'] == true && !busy;
      final running =
          instance?['status'] == 'countdown' ||
          instance?['status'] == 'sampling';
      final label =
          status['message'] as String? ??
          (model.diagnosticsEnabled ? '等待游戏' : '已关闭');
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
              Flexible(
                child: SingleChildScrollView(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      SwitchListTile(
                        key: const Key('diagnostics-toggle'),
                        contentPadding: EdgeInsets.zero,
                        title: const Text('开启增强诊断'),
                        subtitle: const Text(
                          '记录保存在本机，导出后可交给开发者分析。',
                          style: TextStyle(fontSize: 11),
                        ),
                        value: model.diagnosticsEnabled,
                        onChanged: busy || model.busy || !model.connected
                            ? null
                            : (v) => model.diagnosticPreferences(enabled: v),
                      ),
                      SizedBox(
                        height: 38,
                        child: Row(
                          children: [
                            if (busy)
                              const SizedBox(
                                width: 14,
                                height: 14,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                ),
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
                                label,
                                style: const TextStyle(fontSize: 12),
                              ),
                            ),
                          ],
                        ),
                      ),
                      DropdownButtonFormField<String>(
                        key: ValueKey(
                          'diagnostic-type-${model.diagnosticType}',
                        ),
                        initialValue: model.diagnosticType,
                        decoration: const InputDecoration(
                          labelText: '问题类型',
                          isDense: true,
                        ),
                        items: types.entries
                            .map(
                              (e) => DropdownMenuItem(
                                value: e.key,
                                child: Text(e.value),
                              ),
                            )
                            .toList(),
                        onChanged: busy || model.busy
                            ? null
                            : (v) => model.diagnosticPreferences(type: v),
                      ),
                      Padding(
                        padding: const EdgeInsets.symmetric(vertical: 12),
                        child: Text(
                          switch (model.diagnosticType) {
                            'auto' => '自动识别已适配的 AT-5T 场景；其他场景保留基础事件与性能摘要。',
                            'interaction' =>
                              'AT-5T：游戏输入、描述符、动作、占用、锁与坐标距离。支持版本由 EXE 摘要核验。',
                            'connection' => '启动与登录阶段、耗时、频道检查、错误码与进程退出。',
                            'performance' => 'CPU、内存、窗口响应与退出码；帧时间尚未接入。',
                            _ => '当前收集基础事件与性能；任务、房间进度及服务端结算事件尚未接入。',
                          },
                          style: const TextStyle(
                            fontSize: 11,
                            height: 1.6,
                            color: Color(0xffb3aebf),
                          ),
                        ),
                      ),
                      if (instances.isNotEmpty)
                        DropdownButtonFormField<String>(
                          key: ValueKey('diagnostic-instance-$instanceId'),
                          initialValue: instanceId,
                          isExpanded: true,
                          decoration: const InputDecoration(
                            labelText: '采集窗口',
                            isDense: true,
                          ),
                          items: instances
                              .map(
                                (r) => DropdownMenuItem(
                                  value: r['id'] as String,
                                  child: Text(
                                    '${r['window']} · ${r['exeVersion']}',
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                ),
                              )
                              .toList(),
                          onChanged: busy
                              ? null
                              : (v) => setState(() => instanceId = v),
                        ),
                      if (instance?['selected'] == false)
                        TextButton(
                          onPressed: busy
                              ? null
                              : () => model.diagnosticAction(
                                  'diagnosticSelect',
                                  {'sessionId': instanceId},
                                ),
                          child: const Text('关联这个游戏窗口'),
                        ),
                      const SizedBox(height: 12),
                      Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        children: [
                          OutlinedButton(
                            key: const Key('diagnostic-mark'),
                            onPressed: canRecord
                                ? () => model.diagnosticAction(
                                    'diagnosticMark',
                                    {'sessionId': instanceId},
                                  )
                                : null,
                            child: const Text('标记刚才的问题'),
                          ),
                          FilledButton(
                            key: const Key('diagnostic-reproduce'),
                            onPressed: canRecord && !running
                                ? () => model.diagnosticAction(
                                    'diagnosticReproduce',
                                    {'sessionId': instanceId},
                                  )
                                : null,
                            child: const Text('开始复现采集'),
                          ),
                          TextButton(
                            key: const Key('diagnostic-stop'),
                            onPressed: canRecord
                                ? () => model.diagnosticAction(
                                    'diagnosticStop',
                                    {'sessionId': instanceId},
                                  )
                                : null,
                            child: const Text('停止并保存'),
                          ),
                        ],
                      ),
                      const Padding(
                        padding: EdgeInsets.only(top: 8),
                        child: Text(
                          '标记保留此前缓存并继续记录 10 秒；复现倒计时 5 秒后采集 20 秒。',
                          style: TextStyle(
                            fontSize: 11,
                            height: 1.5,
                            color: Color(0xffb3aebf),
                          ),
                        ),
                      ),
                      const Divider(height: 30),
                      const Text('已保存片段', style: TextStyle(fontSize: 14)),
                      const SizedBox(height: 10),
                      if (sessionIds.isEmpty)
                        const Padding(
                          padding: EdgeInsets.symmetric(vertical: 16),
                          child: Text(
                            '片段完成后会显示在这里。',
                            style: TextStyle(fontSize: 12),
                          ),
                        )
                      else ...[
                        DropdownButtonFormField<String>(
                          key: ValueKey(
                            'diagnostic-export-session-$exportSession',
                          ),
                          initialValue: exportSession,
                          isExpanded: true,
                          decoration: const InputDecoration(
                            labelText: '导出会话',
                            isDense: true,
                          ),
                          items: sessionIds
                              .map(
                                (id) => DropdownMenuItem(
                                  value: id,
                                  child: Text(
                                    '${date(clips.firstWhere((c) => c['sessionId'] == id)['startedUtc'])} · ${id.substring(0, id.length < 8 ? id.length : 8)}',
                                  ),
                                ),
                              )
                              .toList(),
                          onChanged: busy
                              ? null
                              : (v) => setState(() {
                                  exportSession = v;
                                  selected.clear();
                                  exported = '';
                                }),
                        ),
                        const SizedBox(height: 8),
                        ...visible.map(
                          (c) => CheckboxListTile(
                            key: ValueKey('clip-${c['id']}'),
                            contentPadding: EdgeInsets.zero,
                            controlAffinity: ListTileControlAffinity.leading,
                            dense: true,
                            value: selected.contains(c['id']),
                            title: Text(
                              '${date(c['startedUtc'])} — ${date(c['endedUtc'])}',
                              style: const TextStyle(fontSize: 12),
                            ),
                            subtitle: Text(
                              '${(c['categories'] as List? ?? []).map((v) => categories[v] ?? v).join(' · ')} · ${size(c['bytes'] as num)}',
                              style: const TextStyle(fontSize: 11),
                            ),
                            onChanged: busy
                                ? null
                                : (v) => setState(() {
                                    if (v == true) {
                                      selected.add(c['id'] as String);
                                    } else {
                                      selected.remove(c['id']);
                                    }
                                  }),
                          ),
                        ),
                        OutlinedButton.icon(
                          key: const Key('diagnostic-export'),
                          onPressed: busy || selected.isEmpty ? null : export,
                          icon: const Icon(
                            Icons.file_download_outlined,
                            size: 16,
                          ),
                          label: Text('导出诊断包（${selected.length}）'),
                        ),
                      ],
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
                      if (model.diagnosticError.isNotEmpty ||
                          pickerError.isNotEmpty)
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
              ),
            ],
          ),
        ),
      );
    },
  );
}
