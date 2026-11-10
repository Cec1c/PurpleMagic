import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'launcher_controller.dart';

class DiagnosticsPanel extends StatefulWidget {
  const DiagnosticsPanel({
    super.key,
    required this.model,
    this.initialTab = 0,
  });
  final LauncherController model;
  final int initialTab;
  @override
  State<DiagnosticsPanel> createState() => _DiagnosticsPanelState();
}

class _DiagnosticsPanelState extends State<DiagnosticsPanel> {
  String exported = '', pickerError = '', feedback = '';
  bool choosing = false, consenting = false;
  late int tab = widget.initialTab;
  String? selectedSessionId;
  String peerScope = 'all_mutual_invisible';
  int onsetSeconds = 0;
  final incidentController = TextEditingController();
  final receiverNameController = TextEditingController();
  final peerNameController = TextEditingController();

  @override
  void dispose() {
    incidentController.dispose();
    receiverNameController.dispose();
    peerNameController.dispose();
    super.dispose();
  }

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
              '还会向服务器读取本次登录的角色名与角色编号、频道、副本、房间、结算阶段和连接事件，'
              '记录发生时间及客户端与服务器的时钟偏差，方便直接定位问题。\n\n'
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
      consent: enabled ? 3 : null,
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

  String stateLabel(String? state) => switch (state) {
    'missing' => '队友不可见',
    'late_appeared' => '队友晚出现',
    'appeared' => '现在出现了',
    'recovered_after_reentry' => '退出重进后恢复',
    _ => state ?? '',
  };

  double uncertaintyForOnset(int seconds) => switch (seconds) {
    0 => 1000,
    5 => 3000,
    15 => 5000,
    _ => 10000,
  };

  Map<String, dynamic>? currentSession(List<Map<String, dynamic>> ledgers) {
    if (ledgers.isEmpty) return null;
    if (selectedSessionId != null) {
      for (final row in ledgers) {
        if (row['clientSessionId'] == selectedSessionId) return row;
      }
    }
    for (final row in ledgers) {
      if (row['running'] == true) return row;
    }
    return ledgers.first;
  }

  Future<void> export(Map<String, dynamic> log) async {
    if (choosing || widget.model.diagnosticBusy) return;
    setState(() {
      choosing = true;
      pickerError = '';
      exported = '';
      feedback = '';
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

  Future<void> exportBundle(
    String activeIncidentId,
    Map<String, dynamic>? selectedSession,
  ) async {
    if (choosing || widget.model.diagnosticBusy) return;
    setState(() {
      choosing = true;
      pickerError = '';
      exported = '';
      feedback = '';
    });
    try {
      final stamp = DateTime.now()
          .toIso8601String()
          .split('.')
          .first
          .replaceAll(':', '-');
      final suffix = activeIncidentId.isNotEmpty ? activeIncidentId : stamp;
      final path = await const MethodChannel('dnf90.preview/window')
          .invokeMethod<String>('saveDiagnosticBundle', {
            'fileName': 'PurpleMagic-$suffix.zip',
          });
      if (path == null || path.isEmpty || !mounted) return;
      final args = <String, dynamic>{'path': path};
      if (activeIncidentId.isNotEmpty) {
        args['incidentId'] = activeIncidentId;
      } else if (selectedSession?['clientSessionId'] is String) {
        args['clientSessionId'] = selectedSession!['clientSessionId'];
      }
      final reply = await widget.model.diagnosticAction(
        'diagnosticExportBundle',
        args,
      );
      if (mounted && reply?['diagnosticExport'] is Map) {
        setState(() => exported = '已保存：${reply!['diagnosticExport']['path']}');
      }
    } catch (_) {
      if (mounted) setState(() => pickerError = '无法打开另存为窗口，请重试。');
    } finally {
      if (mounted) setState(() => choosing = false);
    }
  }

  Future<void> setIncident({required bool generateNew}) async {
    final text = incidentController.text.trim();
    if (!generateNew && text.isEmpty) return;
    setState(() => feedback = '');
    final reply = await widget.model.diagnosticAction('diagnosticSetIncident', {
      'incidentId': generateNew ? null : text,
      'generateNew': generateNew,
    });
    if (!mounted || reply == null) return;
    final id = reply['diagnosticIncidentId'] as String? ?? '';
    incidentController.clear();
    setState(() => feedback = id.isEmpty ? '' : '已关联同队编号：$id');
  }

  Future<void> bindCharacter(String clientSessionId) async {
    final name = receiverNameController.text.trim();
    if (name.isEmpty) return;
    setState(() => feedback = '');
    final reply = await widget.model.diagnosticAction(
      'diagnosticAssociateCharacter',
      {
        'clientSessionId': clientSessionId,
        'receiverCharacterName': name,
      },
    );
    if (!mounted || reply == null) return;
    receiverNameController.clear();
    setState(() => feedback = '已绑定本端角色：$name');
  }

  Future<void> markNewEntry(String clientSessionId) async {
    setState(() => feedback = '');
    final reply = await widget.model.diagnosticAction('diagnosticMarkEntry', {
      'clientSessionId': clientSessionId,
      'entryKind': 'reentry',
    });
    if (!mounted || reply == null) return;
    setState(() => feedback = '已记录新一次进图');
  }

  Future<void> reportObservation(String clientSessionId, String state) async {
    setState(() => feedback = '');
    final peerName = peerNameController.text.trim();
    final reply = await widget.model.diagnosticAction(
      'diagnosticReportObservation',
      {
        'clientSessionId': clientSessionId,
        'state': state,
        'peerScope': peerScope,
        'peerCharacterName':
            peerScope == 'single_peer' && peerName.isNotEmpty ? peerName : null,
        'onsetOffsetSeconds': onsetSeconds,
        'timeUncertaintyMs': uncertaintyForOnset(onsetSeconds),
      },
    );
    if (!mounted || reply == null) return;
    setState(() => feedback = '已记录：${stateLabel(state)}');
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.model,
    builder: (context, _) {
      final model = widget.model;
      final status = model.diagnostics;
      final logs = (status['clips'] as List? ?? [])
          .cast<Map<String, dynamic>>()
          .take(5)
          .toList();
      final ledgers = (status['sessionLedgers'] as List? ?? [])
          .whereType<Map>()
          .map((e) => Map<String, dynamic>.from(e))
          .toList();
      final observations = (status['recentObservations'] as List? ?? [])
          .whereType<Map>()
          .map((e) => Map<String, dynamic>.from(e))
          .toList();
      final activeIncidentId = status['activeIncidentId'] as String? ?? '';
      final selectedSession = currentSession(ledgers);
      final active = (status['instances'] as List? ?? [])
          .cast<Map<String, dynamic>>()
          .map((row) => row['id'])
          .toSet();
      final busy = model.diagnosticBusy || choosing;
      return ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.sizeOf(context).height - 80,
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
                  OutlinedButton.icon(
                    key: const Key('export-diagnostic-bundle'),
                    onPressed: busy || ledgers.isEmpty
                        ? null
                        : () => exportBundle(activeIncidentId, selectedSession),
                    icon: const Icon(Icons.folder_zip_outlined, size: 16),
                    label: const Text(
                      '导出诊断包 (.zip)',
                      style: TextStyle(fontSize: 12),
                    ),
                  ),
                  const SizedBox(width: 8),
                  IconButton(
                    tooltip: '关闭诊断面板',
                    onPressed: () => Navigator.pop(context),
                    icon: const Icon(Icons.close_rounded),
                  ),
                ],
              ),
              const SizedBox(height: 10),
              Row(
                children: [
                  ChoiceChip(
                    key: const Key('diagnostics-tab-logs'),
                    label: const Text('启动日志', style: TextStyle(fontSize: 12)),
                    selected: tab == 0,
                    onSelected: (_) => setState(() => tab = 0),
                  ),
                  const SizedBox(width: 8),
                  ChoiceChip(
                    key: const Key('diagnostics-tab-visibility'),
                    label: Text(
                      observations.isEmpty
                          ? '队友可见性标记'
                          : '队友可见性标记 (${observations.length})',
                      style: const TextStyle(fontSize: 12),
                    ),
                    selected: tab == 1,
                    onSelected: (_) => setState(() => tab = 1),
                  ),
                ],
              ),
              const SizedBox(height: 8),
              if (tab == 0)
                ..._buildLogsTab(model, status, logs, active, busy)
              else
                Flexible(
                  child: SingleChildScrollView(
                    child: _buildVisibilityTab(
                      ledgers,
                      selectedSession,
                      observations,
                      activeIncidentId,
                      busy,
                    ),
                  ),
                ),
              if (feedback.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: SelectableText(
                    feedback,
                    style: const TextStyle(
                      fontSize: 11,
                      color: Color(0xff9ac9b2),
                    ),
                  ),
                ),
              if (exported.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
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
                  padding: const EdgeInsets.only(top: 8),
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

  List<Widget> _buildLogsTab(
    LauncherController model,
    Map<String, dynamic> status,
    List<Map<String, dynamic>> logs,
    Set<dynamic> active,
    bool busy,
  ) => [
    SwitchListTile(
      key: const Key('diagnostics-toggle'),
      contentPadding: EdgeInsets.zero,
      title: const Text('开启增强诊断'),
      subtitle: const Text(
        '按每次游戏启动记录，保留最近 5 次。',
        style: TextStyle(fontSize: 12),
      ),
      value: model.diagnosticsEnabled,
      onChanged: busy || model.busy || !model.connected ? null : toggle,
    ),
    SizedBox(
      height: 32,
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
    const Divider(height: 20),
    for (final instance
        in (status['instances'] as List? ?? []).cast<Map<String, dynamic>>())
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
    const SizedBox(height: 8),
    Flexible(
      child: SingleChildScrollView(
        child: Column(
          children: [
            if (logs.isEmpty)
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 20),
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
                    color: const Color(0xffb7a9dd).withValues(alpha: .07),
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
                          crossAxisAlignment: CrossAxisAlignment.start,
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
                        icon: const Icon(Icons.save_alt_rounded, size: 16),
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
  ];

  Widget _buildVisibilityTab(
    List<Map<String, dynamic>> ledgers,
    Map<String, dynamic>? selectedSession,
    List<Map<String, dynamic>> observations,
    String activeIncidentId,
    bool busy,
  ) {
    final sessionId = selectedSession?['clientSessionId'] as String?;
    final currentRole = selectedSession?['receiverCharacterName'] as String?;
    final roleSource = selectedSession?['associationSource'] as String?;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: const Color(0xffb7a9dd).withValues(alpha: .07),
            borderRadius: BorderRadius.circular(12),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: SelectableText(
                      activeIncidentId.isEmpty
                          ? '同队事件编号：记录可见性时自动生成，也可粘贴队友编号对齐'
                          : '同队事件编号：$activeIncidentId',
                      style: const TextStyle(fontSize: 12),
                    ),
                  ),
                  if (activeIncidentId.isNotEmpty)
                    TextButton(
                      key: const Key('copy-incident-id'),
                      onPressed: () async {
                        await Clipboard.setData(
                          ClipboardData(text: activeIncidentId),
                        );
                        if (mounted) {
                          setState(() => feedback = '已复制编号：$activeIncidentId');
                        }
                      },
                      child: const Text('复制', style: TextStyle(fontSize: 12)),
                    ),
                  TextButton(
                    key: const Key('new-incident-id'),
                    onPressed: busy
                        ? null
                        : () => setIncident(generateNew: true),
                    child: const Text('新建编号', style: TextStyle(fontSize: 12)),
                  ),
                ],
              ),
              const SizedBox(height: 6),
              Row(
                children: [
                  Expanded(
                    child: SizedBox(
                      height: 36,
                      child: TextField(
                        key: const Key('incident-id-input'),
                        controller: incidentController,
                        style: const TextStyle(fontSize: 12),
                        decoration: const InputDecoration(
                          isDense: true,
                          hintText: '粘贴队友分享的同队事件编号（例如 inc-...）',
                          border: OutlineInputBorder(),
                          contentPadding: EdgeInsets.symmetric(
                            horizontal: 10,
                            vertical: 8,
                          ),
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  OutlinedButton(
                    key: const Key('apply-incident-id'),
                    onPressed: busy
                        ? null
                        : () => setIncident(generateNew: false),
                    child: const Text('关联编号', style: TextStyle(fontSize: 12)),
                  ),
                ],
              ),
            ],
          ),
        ),
        const SizedBox(height: 12),
        if (ledgers.isEmpty)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 16),
            child: Text(
              '启动游戏后会自动创建会话台账；无需开启增强诊断即可在此标记队友不可见、退出重进恢复并导出 .zip 诊断包。',
              style: TextStyle(fontSize: 12, color: Color(0xffb3aebf)),
            ),
          )
        else ...[
          const Text('目标客户端实例', style: TextStyle(fontSize: 13)),
          const SizedBox(height: 6),
          Wrap(
            spacing: 8,
            runSpacing: 6,
            children: [
              for (final s in ledgers)
                ChoiceChip(
                  key: ValueKey('session-chip-${s['clientSessionId']}'),
                  label: Text(
                    '${s['launchInstanceId']}${s['pid'] != null ? ' · PID ${s['pid']}' : ''}${s['running'] == true ? ' · 运行中' : ''}${s['receiverCharacterName'] != null ? ' · ${s['receiverCharacterName']}' : ''}',
                    style: const TextStyle(fontSize: 11),
                  ),
                  selected: s['clientSessionId'] == sessionId,
                  onSelected: (_) => setState(
                    () => selectedSessionId = s['clientSessionId'] as String?,
                  ),
                ),
            ],
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: SizedBox(
                  height: 36,
                  child: TextField(
                    key: const Key('receiver-character-input'),
                    controller: receiverNameController,
                    style: const TextStyle(fontSize: 12),
                    decoration: InputDecoration(
                      isDense: true,
                      hintText: currentRole == null
                          ? '本端当前角色名（双开时区分客户端实例）'
                          : '当前角色：$currentRole（${roleSource == 'authenticated_server_context' ? '服务器关联' : '手动绑定'}）',
                      border: const OutlineInputBorder(),
                      contentPadding: const EdgeInsets.symmetric(
                        horizontal: 10,
                        vertical: 8,
                      ),
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              OutlinedButton(
                key: const Key('bind-character-button'),
                onPressed: busy || sessionId == null
                    ? null
                    : () => bindCharacter(sessionId),
                child: const Text('保存本端角色', style: TextStyle(fontSize: 12)),
              ),
              const SizedBox(width: 8),
              OutlinedButton(
                key: const Key('mark-new-entry'),
                onPressed: busy || sessionId == null
                    ? null
                    : () => markNewEntry(sessionId),
                child: const Text('记录新一次进图', style: TextStyle(fontSize: 12)),
              ),
            ],
          ),
          const SizedBox(height: 12),
          const Text('队友范围与发生时间', style: TextStyle(fontSize: 13)),
          const SizedBox(height: 6),
          Wrap(
            spacing: 8,
            runSpacing: 6,
            children: [
              ChoiceChip(
                key: const Key('peer-scope-all'),
                label: const Text('全队互相看不见', style: TextStyle(fontSize: 11)),
                selected: peerScope == 'all_mutual_invisible',
                onSelected: (_) =>
                    setState(() => peerScope = 'all_mutual_invisible'),
              ),
              ChoiceChip(
                key: const Key('peer-scope-single'),
                label: const Text('指定队友', style: TextStyle(fontSize: 11)),
                selected: peerScope == 'single_peer',
                onSelected: (_) => setState(() => peerScope = 'single_peer'),
              ),
              ChoiceChip(
                key: const Key('peer-scope-unknown'),
                label: const Text('队友名未知', style: TextStyle(fontSize: 11)),
                selected: peerScope == 'unknown_peer',
                onSelected: (_) => setState(() => peerScope = 'unknown_peer'),
              ),
            ],
          ),
          if (peerScope == 'single_peer') ...[
            const SizedBox(height: 8),
            SizedBox(
              height: 36,
              child: TextField(
                key: const Key('peer-character-input'),
                controller: peerNameController,
                style: const TextStyle(fontSize: 12),
                decoration: const InputDecoration(
                  isDense: true,
                  hintText: '填写不可见或晚出现的队友角色名',
                  border: OutlineInputBorder(),
                  contentPadding: EdgeInsets.symmetric(
                    horizontal: 10,
                    vertical: 8,
                  ),
                ),
              ),
            ),
          ],
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 6,
            children: [
              for (final (sec, label) in const [
                (0, '刚刚发生'),
                (5, '约 5 秒前'),
                (15, '约 15 秒前'),
                (30, '约 30 秒前'),
              ])
                ChoiceChip(
                  key: ValueKey('onset-$sec'),
                  label: Text(label, style: const TextStyle(fontSize: 11)),
                  selected: onsetSeconds == sec,
                  onSelected: (_) => setState(() => onsetSeconds = sec),
                ),
            ],
          ),
          const SizedBox(height: 12),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              FilledButton.tonal(
                key: const Key('obs-missing'),
                onPressed: busy || sessionId == null
                    ? null
                    : () => reportObservation(sessionId, 'missing'),
                child: const Text('队友不可见', style: TextStyle(fontSize: 12)),
              ),
              FilledButton.tonal(
                key: const Key('obs-late-appeared'),
                onPressed: busy || sessionId == null
                    ? null
                    : () => reportObservation(sessionId, 'late_appeared'),
                child: const Text('队友晚出现', style: TextStyle(fontSize: 12)),
              ),
              OutlinedButton(
                key: const Key('obs-appeared'),
                onPressed: busy || sessionId == null
                    ? null
                    : () => reportObservation(sessionId, 'appeared'),
                child: const Text('现在出现了', style: TextStyle(fontSize: 12)),
              ),
              FilledButton(
                key: const Key('obs-recovered'),
                onPressed: busy || sessionId == null
                    ? null
                    : () => reportObservation(
                        sessionId,
                        'recovered_after_reentry',
                      ),
                child: const Text('退出重进后恢复', style: TextStyle(fontSize: 12)),
              ),
            ],
          ),
        ],
        if (observations.isNotEmpty) ...[
          const SizedBox(height: 14),
          const Text('最近标记记录', style: TextStyle(fontSize: 13)),
          const SizedBox(height: 6),
          for (final obs in observations.take(6))
            Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Text(
                '${date(obs['observedUtc'])} · ${stateLabel(obs['state'] as String?)} · ${obs['launchInstanceId']} · 第 ${obs['sceneEpoch']} 次进图${obs['peerCharacterName'] != null ? ' · 队友 ${obs['peerCharacterName']}' : ''}',
                style: const TextStyle(fontSize: 11, color: Color(0xffb3aebf)),
              ),
            ),
        ],
      ],
    );
  }
}
