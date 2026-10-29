import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'launcher_controller.dart';

class AccountSecurityPanel extends StatefulWidget {
  const AccountSecurityPanel({
    super.key,
    required this.model,
    required this.changing,
    required this.initialAccount,
  });
  final LauncherController model;
  final bool changing;
  final String initialAccount;
  @override
  State<AccountSecurityPanel> createState() => _AccountSecurityPanelState();
}

class _AccountSecurityPanelState extends State<AccountSecurityPanel> {
  late final account = TextEditingController(text: widget.initialAccount);
  final password = TextEditingController(),
      next = TextEditingController(),
      confirmation = TextEditingController();
  Timer? timer;
  bool busy = false;
  String message = '', command = '';
  int expiresAt = 0;
  @override
  void initState() {
    super.initState();
    timer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    timer?.cancel();
    account.dispose();
    password.dispose();
    next.dispose();
    confirmation.dispose();
    super.dispose();
  }

  Future<void> submit() async {
    if (busy || widget.model.accountCooldown(account.text) > 0) return;
    if (account.text.trim().isEmpty || password.text.isEmpty) {
      setState(() => message = '请输入账号和原密码');
      return;
    }
    if (widget.changing &&
        (next.text.length < 6 || next.text != confirmation.text)) {
      setState(() => message = '新密码至少 6 位，两次输入需一致');
      return;
    }
    setState(() {
      busy = true;
      message = '';
      command = '';
    });
    try {
      final result = await widget.model.accountSecurity(
        widget.changing,
        account.text,
        password.text,
        next.text,
      );
      if (!mounted) return;
      setState(() {
        message = result['message'] as String? ?? '操作已完成';
        final code = result['bindingCode'] as String? ?? '';
        if (code.isNotEmpty) {
          command = '/绑定 ${account.text.trim()} $code';
          expiresAt = (result['expiresAt'] as num?)?.toInt() ?? 0;
        }
        final qq = result['boundQQ'] as String? ?? '';
        if (qq.isNotEmpty) message = '账号已绑定 QQ $qq，可以登录游戏';
      });
    } catch (error) {
      if (mounted) {
        setState(
          () => message = error.toString().replaceFirst('Bad state: ', ''),
        );
      }
    } finally {
      password.clear();
      next.clear();
      confirmation.clear();
      if (mounted) setState(() => busy = false);
    }
  }

  Widget field(
    String label,
    TextEditingController controller, {
    bool secret = false,
  }) => Padding(
    padding: const EdgeInsets.only(bottom: 12),
    child: TextField(
      controller: controller,
      enabled: !busy,
      obscureText: secret,
      autocorrect: false,
      enableSuggestions: false,
      onChanged: (_) => setState(() {}),
      decoration: InputDecoration(
        labelText: label,
        border: const OutlineInputBorder(),
      ),
    ),
  );
  @override
  Widget build(BuildContext context) {
    final cooldown = widget.model.accountCooldown(account.text);
    final remaining = expiresAt - DateTime.now().millisecondsSinceEpoch ~/ 1000;
    return PopScope(
      canPop: !busy,
      child: AlertDialog(
        backgroundColor: const Color(0xff282333),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(18)),
        title: Text(widget.changing ? '修改密码' : '绑定 QQ'),
        content: SizedBox(
          width: 420,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                field('游戏账号', account),
                field(widget.changing ? '原密码' : '账号密码', password, secret: true),
                if (widget.changing) ...[
                  field('新密码', next, secret: true),
                  field('确认新密码', confirmation, secret: true),
                ],
                if (!widget.changing)
                  const Text(
                    '获取绑定码后，在允许群内艾特机器人，或私聊机器人发送下方指令。每个 QQ 只能绑定一个账号。',
                    style: TextStyle(fontSize: 12),
                  ),
                if (command.isNotEmpty) ...[
                  const SizedBox(height: 14),
                  SelectableText(command),
                  const SizedBox(height: 6),
                  Text(
                    remaining > 0 ? '有效期剩余 $remaining 秒' : '绑定码已过期，请重新获取',
                    style: const TextStyle(fontSize: 12),
                  ),
                  TextButton.icon(
                    onPressed: remaining > 0
                        ? () async {
                            await Clipboard.setData(
                              ClipboardData(text: command),
                            );
                            if (mounted) setState(() => message = '绑定指令已复制');
                          }
                        : null,
                    icon: const Icon(Icons.copy, size: 16),
                    label: const Text('复制绑定指令'),
                  ),
                ],
                const SizedBox(height: 10),
                if (message.isNotEmpty)
                  Text(
                    message,
                    style: const TextStyle(
                      fontSize: 12,
                      color: Color(0xffe3cfa9),
                    ),
                  ),
                const SizedBox(height: 8),
                Text(
                  cooldown > 0
                      ? '验证冷却：$cooldown 秒'
                      : '登录、获取绑定码及改密共用验证次数，连续失败 3 次冷却 10 分钟。',
                  style: const TextStyle(
                    fontSize: 11,
                    color: Color(0xffb3aebf),
                  ),
                ),
              ],
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: busy ? null : () => Navigator.pop(context),
            child: const Text('关闭'),
          ),
          FilledButton(
            onPressed: busy || cooldown > 0 ? null : submit,
            child: busy
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : Text(widget.changing ? '确认修改' : '获取绑定码'),
          ),
        ],
      ),
    );
  }
}
