import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'launcher_controller.dart';
import 'launcher_account_field.dart';

class AccountSecurityPanel extends StatefulWidget {
  const AccountSecurityPanel({
    super.key,
    required this.model,
    required this.changing,
    required this.initialAccount,
    this.inline = false,
    this.onBusyChanged,
  });
  final LauncherController model;
  final bool changing;
  final String initialAccount;
  final bool inline;
  final ValueChanged<bool>? onBusyChanged;
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
    widget.onBusyChanged?.call(true);
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
      if (mounted) {
        setState(() => busy = false);
        widget.onBusyChanged?.call(false);
      }
    }
  }

  Widget field(
    String label,
    TextEditingController controller, {
    bool secret = false,
  }) => Padding(
    padding: const EdgeInsets.only(bottom: 12),
    child: LauncherAccountField(
      label: label,
      hint: '请输入$label',
      controller: controller,
      icon: secret ? Icons.lock_outline_rounded : Icons.person_outline_rounded,
      fieldKey: 'security-$label',
      secret: secret,
      enabled: !busy,
      onChanged: (_) => setState(() {}),
    ),
  );
  @override
  Widget build(BuildContext context) {
    final cooldown = widget.model.accountCooldown(account.text);
    final remaining = expiresAt - DateTime.now().millisecondsSinceEpoch ~/ 1000;
    final content = Column(
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
            '获取绑定码后，在允许群内艾特机器人，或私聊机器人发送绑定指令。',
            style: TextStyle(
              fontSize: 11,
              color: Color(0xffb3aebf),
              height: 1.6,
            ),
          ),
        if (command.isNotEmpty) ...[
          const SizedBox(height: 12),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: const Color(0x44201d2e),
              borderRadius: BorderRadius.circular(9),
              border: Border.all(color: const Color(0x26ded3f0)),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                SelectableText(
                  command,
                  style: const TextStyle(
                    fontSize: 12,
                    color: Color(0xffe3cfa9),
                  ),
                ),
                const SizedBox(height: 6),
                Text(
                  remaining > 0 ? '有效期剩余 $remaining 秒' : '绑定码已过期，请重新获取',
                  style: const TextStyle(
                    fontSize: 10,
                    color: Color(0xffb3aebf),
                  ),
                ),
                TextButton.icon(
                  onPressed: remaining > 0
                      ? () async {
                          await Clipboard.setData(ClipboardData(text: command));
                          if (mounted) setState(() => message = '绑定指令已复制');
                        }
                      : null,
                  icon: const Icon(Icons.copy, size: 15),
                  label: const Text('复制绑定指令'),
                ),
              ],
            ),
          ),
        ],
        const SizedBox(height: 10),
        if (message.isNotEmpty)
          Text(
            message,
            style: const TextStyle(
              fontSize: 11,
              color: Color(0xffe3cfa9),
              height: 1.5,
            ),
          ),
        const SizedBox(height: 8),
        Text(
          cooldown > 0 ? '验证冷却：$cooldown 秒' : '连续验证失败 3 次，冷却 1 分钟。',
          style: const TextStyle(fontSize: 10, color: Color(0xffb3aebf)),
        ),
      ],
    );
    final submitButton = FilledButton(
      onPressed: busy || cooldown > 0 ? null : submit,
      style: FilledButton.styleFrom(
        backgroundColor: const Color(0xff705038),
        foregroundColor: const Color(0xffffe6bb),
        disabledBackgroundColor: const Color(0xff423a48),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(9),
          side: const BorderSide(color: Color(0xffb88a54)),
        ),
      ),
      child: busy
          ? const SizedBox(
              width: 18,
              height: 18,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : Text(
              widget.changing ? '确认修改' : '获取绑定码',
              style: TextStyle(fontSize: widget.inline ? 16 : 14),
            ),
    );
    if (widget.inline) {
      return Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          content,
          const SizedBox(height: 18),
          SizedBox(height: 51, child: submitButton),
        ],
      );
    }
    return PopScope(
      canPop: !busy,
      child: AlertDialog(
        backgroundColor: const Color(0xff282333),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(18)),
        title: Text(widget.changing ? '修改密码' : '绑定 QQ'),
        content: SizedBox(
          width: 420,
          child: SingleChildScrollView(child: content),
        ),
        actions: [
          TextButton(
            onPressed: busy ? null : () => Navigator.pop(context),
            child: const Text('关闭'),
          ),
          submitButton,
        ],
      ),
    );
  }
}
