import 'dart:async';
import 'dart:math' as math;
import 'dart:ui';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'launcher_controller.dart';
import 'diagnostics_panel.dart';
import 'account_security_panel.dart';
import 'launcher_account_field.dart';
export 'launcher_controller.dart';

List<String> launchArguments = const [];
void main(List<String> args) {
  launchArguments = args;
  runApp(const LauncherPreview());
}

const ink = Color(0xfff0edf5), muted = Color(0xffb3aebf);
const gold = Color(0xffe3cfa9),
    violet = Color(0xffb7a9dd),
    green = Color(0xff9ac9b2);

class LauncherPreview extends StatelessWidget {
  const LauncherPreview({super.key, this.controller});
  final LauncherController? controller;
  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'PurpleMagic',
    debugShowCheckedModeBanner: false,
    theme: ThemeData(
      brightness: Brightness.dark,
      useMaterial3: true,
      fontFamily: 'Microsoft YaHei UI',
      fontFamilyFallback: const ['Microsoft YaHei', 'Segoe UI'],
      colorScheme: ColorScheme.fromSeed(
        seedColor: violet,
        brightness: Brightness.dark,
      ),
      scaffoldBackgroundColor: const Color(0xff11111a),
      textTheme: const TextTheme(
        bodyMedium: TextStyle(color: ink, fontSize: 13),
      ),
      tooltipTheme: TooltipThemeData(
        decoration: BoxDecoration(
          color: const Color(0xff2b2838),
          borderRadius: BorderRadius.circular(7),
        ),
        textStyle: const TextStyle(color: ink, fontSize: 12),
      ),
    ),
    home: PreviewHome(controller: controller),
  );
}

class Glass extends StatelessWidget {
  const Glass({
    super.key,
    required this.child,
    this.radius = 24,
    this.solid = false,
    this.color = const Color(0xa31b1b28),
  });
  final Widget child;
  final double radius;
  final bool solid;
  final Color color;
  @override
  Widget build(BuildContext context) => Container(
    decoration: BoxDecoration(
      borderRadius: BorderRadius.circular(radius),
      boxShadow: const [
        BoxShadow(
          color: Color(0x45000000),
          blurRadius: 38,
          offset: Offset(0, 18),
        ),
      ],
    ),
    child: ClipRRect(
      borderRadius: BorderRadius.circular(radius),
      child: BackdropFilter(
        filter: ImageFilter.blur(
          sigmaX: solid ? 0 : 20,
          sigmaY: solid ? 0 : 20,
        ),
        child: DecoratedBox(
          decoration: BoxDecoration(
            color: solid ? const Color(0xf51c1c29) : color,
            borderRadius: BorderRadius.circular(radius),
            border: Border.all(color: const Color(0x26e8e1f7)),
          ),
          child: Material(type: MaterialType.transparency, child: child),
        ),
      ),
    ),
  );
}

class PreviewHome extends StatefulWidget {
  const PreviewHome({super.key, this.controller});
  final LauncherController? controller;
  @override
  State<PreviewHome> createState() => _PreviewHomeState();
}

class _PreviewHomeState extends State<PreviewHome> {
  late final LauncherController model =
      widget.controller ?? LauncherController();
  final account = TextEditingController(),
      password = TextEditingController(),
      confirmation = TextEditingController();
  int accountTab = 0;
  bool get registering => accountTab == 1;
  bool remember = true, submitting = false, securityBusy = false;
  int category = 0, featured = 0;
  String error = '', directory = '';
  @override
  void initState() {
    super.initState();
    model
        .initialize()
        .then((_) async {
          if (!mounted) return;
          setState(() {
            directory = model.clientDirectory;
            account.text = model.account;
            remember = model.rememberAccount;
          });
          if (launchArguments.length == 2 &&
              launchArguments.first == '--update-health') {
            await model.reportHealthy(launchArguments[1]);
          }
          if (mounted && !launchArguments.contains('--update-retry-required')) {
            await _automaticUpdate();
          }
        })
        .catchError((Object failure) {
          if (mounted) {
            setState(
              () => error = failure.toString().replaceFirst('Bad state: ', ''),
            );
          }
        });
  }

  Future<void> _automaticUpdate() async {
    if (await model.automaticUpdate() && mounted) {
      await windowAction('close');
    }
  }

  Future<void> _beginUpdate() async {
    if (await model.beginUpdate() && mounted) await windowAction('close');
  }

  Future<void> _setAutoUpdate(bool enabled) async {
    try {
      await model.save(check: enabled);
      if (mounted && enabled) await _automaticUpdate();
    } catch (failure) {
      if (mounted) {
        await _article(
          '设置未保存',
          failure.toString().replaceFirst('Bad state: ', ''),
        );
      }
    }
  }

  static const platform = MethodChannel('dnf90.preview/window');
  @override
  void dispose() {
    account.dispose();
    password.dispose();
    confirmation.dispose();
    if (widget.controller == null) {
      model.dispose();
    }
    super.dispose();
  }

  Future<void> windowAction(String action) async {
    try {
      await platform.invokeMethod<void>(action);
    } on MissingPluginException {
      /* No host window in widget tests. */
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    body: AnimatedBuilder(
      animation: model,
      builder: (context, _) => LayoutBuilder(
        builder: (context, bounds) {
          final scale = math.min(
            bounds.maxWidth / 1440,
            bounds.maxHeight / 810,
          );
          return Stack(
            fit: StackFit.expand,
            children: [
              Image.asset('assets/background.webp', fit: BoxFit.cover),
              const DecoratedBox(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    begin: Alignment.topCenter,
                    end: Alignment.bottomCenter,
                    colors: [
                      Color(0x180e0c18),
                      Color(0x000e0c18),
                      Color(0x99100f1a),
                    ],
                    stops: [0, .50, 1],
                  ),
                ),
              ),
              const DecoratedBox(
                decoration: BoxDecoration(
                  gradient: LinearGradient(
                    colors: [
                      Color(0x00000000),
                      Color(0x00151222),
                      Color(0x44110e1c),
                    ],
                    stops: [0, .55, 1],
                  ),
                ),
              ),
              Center(
                child: SizedBox(
                  width: 1440 * scale,
                  height: 810 * scale,
                  child: FittedBox(
                    child: SizedBox(width: 1440, height: 810, child: _stage()),
                  ),
                ),
              ),
            ],
          );
        },
      ),
    ),
  );

  Widget _stage() => Stack(
    children: [
      Positioned(
        left: 0,
        right: 0,
        top: 0,
        height: 68,
        child: GestureDetector(
          behavior: HitTestBehavior.translucent,
          onPanStart: (_) => windowAction('drag'),
          onDoubleTap: () => windowAction('maximize'),
        ),
      ),
      Positioned(
        left: 40,
        top: 26,
        child: Row(
          children: [
            Image.asset(
              'brand/witch.png',
              width: 124,
              height: 77,
              fit: BoxFit.contain,
            ),
            const SizedBox(width: 21),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 13, vertical: 8),
              decoration: BoxDecoration(
                color: const Color(0x72201f30),
                borderRadius: BorderRadius.circular(22),
                border: Border.all(color: const Color(0x20ffffff)),
              ),
              child: const Text(
                '90 级  ·  阿拉德冒险',
                style: TextStyle(
                  fontSize: 11,
                  color: Color(0xffe5ddef),
                  letterSpacing: 1.5,
                ),
              ),
            ),
          ],
        ),
      ),
      Positioned(
        right: 24,
        top: 17,
        child: Row(
          children: [
            if (model.diagnosticsEnabled)
              TextButton.icon(
                key: const Key('diagnostics-active'),
                onPressed: _diagnostics,
                icon: const Icon(Icons.monitor_heart_outlined, size: 16),
                label: const Text('诊断已开启', style: TextStyle(fontSize: 11)),
              ),
            _icon(Icons.settings_outlined, '设置', _settings, key: 'settings'),
            const SizedBox(width: 10),
            _icon(Icons.remove_rounded, '最小化', () => windowAction('minimize')),
            _icon(Icons.close_rounded, '关闭', () => windowAction('close')),
          ],
        ),
      ),
      Positioned(left: 40, bottom: 67, width: 435, child: _announcements()),
      Positioned(right: 42, top: 108, width: 388, child: _loginPanel()),
      const Positioned(
        left: 42,
        bottom: 24,
        child: Text(
          'PurpleMagic',
          style: TextStyle(
            color: Color(0xffc1b9ce),
            fontSize: 11,
            letterSpacing: 2.2,
          ),
        ),
      ),
      const Positioned(
        left: 0,
        right: 0,
        bottom: 24,
        child: Center(
          child: Text(
            '愿每一次重逢，都成为新的冒险。',
            style: TextStyle(
              color: Color(0xffa6a0b2),
              fontSize: 11,
              letterSpacing: 1,
            ),
          ),
        ),
      ),
      Positioned(
        right: 42,
        bottom: 20,
        child: Row(
          children: [
            Text(
              model.connected ? 'PurpleMagic · 本服账号与更新' : '正在连接核心',
              style: TextStyle(fontSize: 10, color: Color(0xffb8b0c6)),
            ),
            const SizedBox(width: 16),
            _link(
              '使用帮助',
              () => _article(
                '开启你的阿拉德之旅',
                '选择包含 DNF.exe 和 Script.pvf 的客户端目录，检查更新后输入账号密码。\n\n更新前请退出游戏。记住账号不会保存密码；更新来源由本服后台配置。',
              ),
            ),
          ],
        ),
      ),
    ],
  );

  Widget _announcements() {
    const titles = ['重返阿拉德\n再启冒险征程', '熟悉的世界\n等待再次相遇', '准备就绪\n开启你的旅程'];
    return Glass(
      solid: model.reducedEffects,
      radius: 22,
      color: const Color(0x99201928),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          SizedBox(
            height: 175,
            child: Stack(
              fit: StackFit.expand,
              children: [
                Image.asset('assets/announcement.webp', fit: BoxFit.cover),
                const DecoratedBox(
                  decoration: BoxDecoration(
                    gradient: LinearGradient(
                      colors: [
                        Color(0xd91a1525),
                        Color(0x651a1525),
                        Color(0x221a1525),
                      ],
                    ),
                  ),
                ),
                Positioned.fill(
                  child: Material(
                    color: Colors.transparent,
                    child: InkWell(
                      key: const Key('featured-news'),
                      onTap: () => _article(
                        titles[featured].replaceAll('\n', ' · '),
                        '冒险从这里开始。\n\n选择配套客户端，完成版本检查后即可登录。服务器发布的公告显示在下方列表中。',
                      ),
                      child: Padding(
                        padding: const EdgeInsets.fromLTRB(25, 21, 25, 20),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            const Text(
                              'DUNGEON & FIGHTER',
                              style: TextStyle(
                                fontSize: 9,
                                letterSpacing: 3.1,
                                fontWeight: FontWeight.w700,
                                color: gold,
                              ),
                            ),
                            const SizedBox(height: 10),
                            Text(
                              titles[featured],
                              style: const TextStyle(
                                fontSize: 27,
                                height: 1.33,
                                fontWeight: FontWeight.w700,
                                letterSpacing: 1,
                              ),
                            ),
                            const Spacer(),
                            const Row(
                              children: [
                                Text(
                                  '阿拉德，久别重逢',
                                  style: TextStyle(
                                    fontSize: 11,
                                    color: Color(0xffded4e6),
                                  ),
                                ),
                                Spacer(),
                                Icon(
                                  Icons.arrow_forward_rounded,
                                  size: 17,
                                  color: gold,
                                ),
                              ],
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
                Positioned(
                  bottom: 17,
                  left: 195,
                  child: Row(
                    children: List.generate(
                      3,
                      (i) => Semantics(
                        label: '公告推荐 ${i + 1}',
                        button: true,
                        selected: featured == i,
                        child: InkWell(
                          onTap: () => setState(() => featured = i),
                          child: SizedBox(
                            width: 24,
                            height: 22,
                            child: Center(
                              child: AnimatedContainer(
                                duration: const Duration(milliseconds: 160),
                                width: featured == i ? 17 : 5,
                                height: 4,
                                decoration: BoxDecoration(
                                  color: featured == i
                                      ? gold
                                      : const Color(0x75d1c8db),
                                  borderRadius: BorderRadius.circular(8),
                                ),
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 8, 20, 14),
            child: Column(
              children: [
                Row(
                  children: [
                    for (var i = 0; i < 3; i++)
                      Padding(
                        padding: const EdgeInsets.only(right: 20),
                        child: _tab(
                          ['综合', '更新', '指南'][i],
                          category == i,
                          () => setState(() => category = i),
                          compact: true,
                        ),
                      ),
                    const Spacer(),
                    _icon(
                      Icons.add_rounded,
                      '更多公告',
                      () => _article(
                        '公告中心',
                        model.news.isEmpty
                            ? '服务器尚未发布公告。'
                            : model.news
                                  .map((n) => '${n['title']}\n${n['body']}')
                                  .join('\n\n'),
                      ),
                      small: true,
                    ),
                  ],
                ),
                const Divider(height: 1, color: Color(0x18ffffff)),
                const SizedBox(height: 6),
                for (final item
                    in model.news
                        .where(
                          (n) =>
                              category == 0 ||
                              n['category'] == ['综合', '更新', '指南'][category],
                        )
                        .take(3))
                  _news(
                    item['category'] as String? ?? '综合',
                    item['title'] as String? ?? '',
                    item['date'] as String? ?? '',
                    item['title'] as String? ?? '',
                    item['body'] as String? ?? '',
                  ),
                if (model.news
                    .where(
                      (n) =>
                          category == 0 ||
                          n['category'] == ['综合', '更新', '指南'][category],
                    )
                    .isEmpty)
                  const SizedBox(
                    height: 93,
                    child: Center(
                      child: Text(
                        '暂无公告',
                        style: TextStyle(color: muted, fontSize: 12),
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _news(
    String tag,
    String title,
    String trailing,
    String heading,
    String body,
  ) => Material(
    color: Colors.transparent,
    child: InkWell(
      onTap: () => _article(heading, body),
      borderRadius: BorderRadius.circular(5),
      child: SizedBox(
        height: 31,
        child: Row(
          children: [
            Text('[$tag]', style: const TextStyle(fontSize: 10, color: muted)),
            const SizedBox(width: 9),
            Expanded(
              child: Text(
                title,
                style: const TextStyle(fontSize: 11, color: Color(0xffe3deeb)),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            Text(
              trailing,
              style: TextStyle(
                fontSize: 9,
                color: trailing == '置顶' ? gold : muted,
              ),
            ),
          ],
        ),
      ),
    ),
  );

  Widget _loginPanel() => Glass(
    solid: model.reducedEffects,
    child: Padding(
      padding: const EdgeInsets.fromLTRB(28, 21, 28, 21),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Image.asset(
            'brand/witch.png',
            width: 108,
            height: 69,
            fit: BoxFit.contain,
          ),
          const SizedBox(height: 5),
          const Text(
            '欢迎回到阿拉德',
            style: TextStyle(
              fontSize: 18,
              fontWeight: FontWeight.w500,
              letterSpacing: 1.8,
            ),
          ),
          const SizedBox(height: 4),
          const Text(
            '每一段冒险，都值得重新启程',
            style: TextStyle(fontSize: 10, color: muted, letterSpacing: .8),
          ),
          const SizedBox(height: 15),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Expanded(
                child: _tab(
                  '开始游戏',
                  accountTab == 0,
                  () => _mode(false),
                  compact: true,
                ),
              ),
              Expanded(
                child: _tab(
                  '账号注册',
                  accountTab == 1,
                  () => _mode(true),
                  compact: true,
                ),
              ),
              Expanded(
                child: _tab(
                  '修改密码',
                  accountTab == 2,
                  () => _accountSecurity(true),
                  compact: true,
                ),
              ),
              if (model.qqBindingEnabled)
                Expanded(
                  child: _tab(
                    '绑定 QQ',
                    accountTab == 3,
                    () => _accountSecurity(false),
                    compact: true,
                  ),
                ),
            ],
          ),
          const SizedBox(height: 15),
          if (accountTab >= 2)
            AccountSecurityPanel(
              key: ValueKey(accountTab),
              model: model,
              changing: accountTab == 2,
              initialAccount: account.text,
              inline: true,
              onBusyChanged: (value) {
                if (mounted) setState(() => securityBusy = value);
              },
            )
          else ...[
            _field(
              '账号',
              '请输入游戏账号',
              account,
              Icons.person_outline_rounded,
              'account',
            ),
            const SizedBox(height: 14),
            _field(
              '密码',
              '请输入密码',
              password,
              Icons.lock_outline_rounded,
              'password',
              secret: true,
            ),
            if (registering) ...[
              const SizedBox(height: 14),
              _field(
                '确认密码',
                '再次输入密码',
                confirmation,
                Icons.lock_outline_rounded,
                'confirmation',
                secret: true,
                toggle: false,
              ),
            ],
            const SizedBox(height: 5),
            SizedBox(
              height: 32,
              child: Row(
                children: [
                  SizedBox(
                    width: 24,
                    height: 28,
                    child: Checkbox(
                      key: const Key('remember'),
                      value: remember,
                      onChanged: (value) => setState(() => remember = value!),
                      activeColor: gold,
                      checkColor: const Color(0xff292432),
                      side: const BorderSide(color: Color(0xff8d869b)),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(3),
                      ),
                      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                  ),
                  const SizedBox(width: 5),
                  GestureDetector(
                    onTap: () => setState(() => remember = !remember),
                    child: const Text(
                      '记住账号',
                      style: TextStyle(fontSize: 11, color: muted),
                    ),
                  ),
                  const Spacer(),
                  _link(
                    registering ? '已有账号？去登录' : '还没有账号？注册',
                    () => _mode(!registering),
                  ),
                ],
              ),
            ),
            SizedBox(
              height: 20,
              child: Align(
                alignment: Alignment.centerLeft,
                child: Text(
                  error,
                  key: const Key('form-error'),
                  style: const TextStyle(
                    fontSize: 10,
                    color: Color(0xffffb9b4),
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ),
            if (!registering) ...[
              Material(
                color: const Color(0x44201d2e),
                borderRadius: BorderRadius.circular(9),
                child: InkWell(
                  key: const Key('directory'),
                  onTap: _chooseDirectory,
                  borderRadius: BorderRadius.circular(9),
                  child: Container(
                    height: 43,
                    padding: const EdgeInsets.symmetric(horizontal: 12),
                    decoration: BoxDecoration(
                      border: Border.all(color: const Color(0x26ded3f0)),
                      borderRadius: BorderRadius.circular(9),
                    ),
                    child: Row(
                      children: [
                        const Icon(
                          Icons.folder_open_rounded,
                          color: muted,
                          size: 17,
                        ),
                        const SizedBox(width: 10),
                        Expanded(
                          child: Text(
                            directory,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(
                              fontSize: 11,
                              color: Color(0xffd2cadc),
                            ),
                          ),
                        ),
                        const SizedBox(width: 9),
                        const Text(
                          '更改',
                          style: TextStyle(fontSize: 10, color: gold),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              _updateStatus(),
              const SizedBox(height: 15),
            ] else
              const SizedBox(height: 8),
            SizedBox(
              width: double.infinity,
              height: 51,
              child: FilledButton(
                key: const Key('primary'),
                onPressed:
                    submitting ||
                        model.busy ||
                        (!registering && !model.canLogin)
                    ? null
                    : _submit,
                style: FilledButton.styleFrom(
                  backgroundColor: const Color(0xff705038),
                  foregroundColor: const Color(0xffffe6bb),
                  disabledBackgroundColor: const Color(0xff423a48),
                  disabledForegroundColor: const Color(0xffb5adbf),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(9),
                    side: BorderSide(
                      color: model.canLogin
                          ? const Color(0xffb88a54)
                          : const Color(0xff666070),
                    ),
                  ),
                  elevation: 8,
                  shadowColor: const Color(0x50000000),
                ),
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    if (submitting) ...[
                      const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: gold,
                        ),
                      ),
                      const SizedBox(width: 12),
                    ],
                    Text(
                      submitting
                          ? '正在验证…'
                          : registering
                          ? '创建账号'
                          : model.canLogin
                          ? '登录并进入游戏'
                          : model.hasUpdate
                          ? '请先安装客户端更新'
                          : '等待客户端就绪',
                      style: const TextStyle(
                        fontSize: 18,
                        fontWeight: FontWeight.w500,
                        letterSpacing: 1.4,
                      ),
                    ),
                    if (!submitting && model.canLogin) ...[
                      const SizedBox(width: 12),
                      const Icon(Icons.arrow_forward_rounded, size: 18),
                    ],
                  ],
                ),
              ),
            ),
          ],
          const SizedBox(height: 15),
          Text(
            model.launcherVersion.isEmpty
                ? 'PurpleMagic'
                : 'PurpleMagic  ·  ${model.launcherVersion}',
            style: const TextStyle(
              fontSize: 9,
              color: Color(0xffa59bb4),
              letterSpacing: 1.7,
            ),
          ),
        ],
      ),
    ),
  );

  Widget _updateStatus() => SizedBox(
    height: 58,
    child: Column(
      children: [
        Row(
          children: [
            Icon(
              model.canLogin
                  ? Icons.check_circle_rounded
                  : Icons.downloading_rounded,
              size: 12,
              color: model.canLogin ? green : gold,
            ),
            const SizedBox(width: 6),
            Expanded(
              child: Text.rich(
                TextSpan(
                  children: [
                    TextSpan(text: model.status),
                    if (model.antiCheatEnabled)
                      const TextSpan(
                        text: '  ·  反作弊保护',
                        style: TextStyle(color: gold),
                      ),
                    if (model.clientVersion.isNotEmpty)
                      TextSpan(
                        text: '  ·  ${model.clientVersion}',
                        style: const TextStyle(color: gold),
                      ),
                  ],
                ),
                key: const Key('client-status'),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(fontSize: 11, color: Color(0xffd3cbdc)),
              ),
            ),
            if (!model.busy && model.phase != UpdatePhase.paused)
              _link(
                model.hasLauncherUpdate
                    ? '更新登录器'
                    : model.hasUpdate
                    ? '安装更新'
                    : '检查更新',
                _beginUpdate,
                key: 'check-update',
              )
            else if (model.phase == UpdatePhase.downloading ||
                model.phase == UpdatePhase.paused)
              _link(
                model.phase == UpdatePhase.paused ? '继续' : '暂停',
                model.pauseOrResume,
                key: 'pause-update',
              ),
          ],
        ),
        const SizedBox(height: 9),
        ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: LinearProgressIndicator(
            value:
                model.phase == UpdatePhase.checking ||
                    model.phase == UpdatePhase.loggingIn
                ? null
                : model.canLogin
                ? 1
                : model.progress,
            minHeight: 3,
            backgroundColor: const Color(0x35c8bddb),
            color: model.canLogin ? const Color(0xff869d91) : gold,
          ),
        ),
        const SizedBox(height: 5),
        Tooltip(
          message: model.message,
          child: Text(
            model.total > 0 && !model.canLogin
                ? '${(model.downloaded / 1048576).toStringAsFixed(1)} / ${(model.total / 1048576).toStringAsFixed(1)} MiB · ${model.message}'
                : model.message,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontSize: 9, color: muted),
          ),
        ),
      ],
    ),
  );

  Widget _field(
    String label,
    String hint,
    TextEditingController controller,
    IconData icon,
    String key, {
    bool secret = false,
    bool toggle = true,
  }) => LauncherAccountField(
    label: label,
    hint: hint,
    controller: controller,
    icon: icon,
    fieldKey: key,
    secret: secret,
    toggle: toggle,
    enabled: !submitting,
    onSubmitted: secret ? (_) => _submit() : null,
  );
  Widget _tab(
    String text,
    bool selected,
    VoidCallback action, {
    bool compact = false,
  }) => Material(
    color: Colors.transparent,
    child: InkWell(
      onTap: submitting || securityBusy ? null : action,
      borderRadius: BorderRadius.circular(5),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 2),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: EdgeInsets.symmetric(vertical: compact ? 10 : 9),
              child: Text(
                text,
                style: TextStyle(
                  fontSize: compact ? 12 : 15,
                  color: selected ? gold : muted,
                ),
              ),
            ),
            Container(
              height: 2,
              width: compact ? 22 : 26,
              decoration: BoxDecoration(
                color: selected ? gold : Colors.transparent,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ],
        ),
      ),
    ),
  );
  Widget _icon(
    IconData icon,
    String tooltip,
    VoidCallback action, {
    String? key,
    bool small = false,
  }) => SizedBox(
    width: small ? 28 : 35,
    height: small ? 28 : 35,
    child: IconButton(
      key: key == null ? null : Key(key),
      tooltip: tooltip,
      padding: EdgeInsets.zero,
      onPressed: action,
      icon: Icon(icon, size: small ? 16 : 18, color: const Color(0xffd0c8df)),
    ),
  );
  Widget _link(String text, VoidCallback action, {String? key}) => TextButton(
    key: key == null ? null : Key(key),
    onPressed: action,
    style: TextButton.styleFrom(
      foregroundColor: gold,
      padding: EdgeInsets.zero,
      minimumSize: const Size(0, 22),
      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
    ),
    child: Text(text, style: const TextStyle(fontSize: 10)),
  );
  void _mode(bool value) {
    if (!submitting && !securityBusy) {
      setState(() {
        accountTab = value ? 1 : 0;
        password.clear();
        error = '';
        confirmation.clear();
      });
    }
  }

  void _accountSecurity(bool changing) {
    if (submitting || securityBusy || (!changing && !model.qqBindingEnabled)) {
      return;
    }
    setState(() {
      accountTab = changing ? 2 : 3;
      password.clear();
      confirmation.clear();
      error = '';
    });
  }

  Future<void> _submit() async {
    if (submitting || model.busy || (!registering && !model.canLogin)) return;
    if (account.text.trim().isEmpty || password.text.isEmpty) {
      setState(() => error = '请输入账号和密码');
      return;
    }
    if (registering && password.text != confirmation.text) {
      setState(() => error = '两次输入的密码不一致');
      return;
    }
    final creating = registering;
    setState(() {
      error = '';
      submitting = true;
    });
    try {
      await model.save(
        directory: directory,
        remembered: account.text.trim(),
        remember: remember,
      );
      if (!mounted) return;
      if (!creating && model.needsAntiCheatConsent) {
        final agreed = await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            key: const Key('anticheat-consent'),
            title: const Text('启动反作弊保护'),
            content: const Text(
              '本服已开启保护。辅助程序将请求 Windows 管理员授权，锁定本次游戏资源，并检查游戏模块及已配置的进程规则。记录保存在本机，不自动上传。\n\n游戏和登录器界面保持普通权限。退出登录器或监护中断会结束本次游戏；最小化不受影响。\n\n同意记录保存在本机，下次不再重复提示。Windows 管理员授权仍会在提权时出现。',
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('取消'),
              ),
              TextButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('同意并继续'),
              ),
            ],
          ),
        );
        if (!mounted || agreed != true) return;
        await model.save(
          antiCheatConsent: LauncherController.antiCheatDisclosureVersion,
        );
        if (!mounted) return;
      }
      await model.submit(creating, account.text.trim(), password.text);
      if (!mounted) return;
      password.clear();
      confirmation.clear();
      setState(() => submitting = false);
      if (creating) await _article('注册成功', '账号已创建，请切换到开始游戏并登录。');
    } catch (failure) {
      if (mounted) {
        setState(
          () => error = failure.toString().replaceFirst('Bad state: ', ''),
        );
      }
    } finally {
      password.clear();
      confirmation.clear();
      if (mounted) {
        setState(() {
          submitting = false;
          if (!remember) account.clear();
        });
      }
    }
  }

  Future<void> _article(String title, String body) => showDialog<void>(
    context: context,
    builder: (context) => Dialog(
      backgroundColor: Colors.transparent,
      child: Glass(
        solid: model.reducedEffects,
        color: const Color(0xe3221f30),
        child: SizedBox(
          width: 470,
          child: Padding(
            padding: const EdgeInsets.all(30),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        title,
                        style: const TextStyle(fontSize: 19, color: gold),
                      ),
                    ),
                    _icon(
                      Icons.close_rounded,
                      '关闭弹窗',
                      () => Navigator.pop(context),
                    ),
                  ],
                ),
                const SizedBox(height: 18),
                Flexible(
                  child: SingleChildScrollView(
                    child: Text(
                      body,
                      style: const TextStyle(
                        fontSize: 13,
                        height: 1.9,
                        color: Color(0xffddd5e7),
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 24),
                Align(
                  alignment: Alignment.centerRight,
                  child: TextButton(
                    onPressed: () => Navigator.pop(context),
                    child: const Text('知道了'),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );

  bool _choosingDirectory = false;

  Future<void> _chooseDirectory() async {
    if (_choosingDirectory) return;
    _choosingDirectory = true;
    try {
      final selected = await platform.invokeMethod<String>('chooseDirectory', {
        'initialDirectory': directory,
      });
      if (!mounted || selected == null || selected.isEmpty) return;
      await model.save(directory: selected);
      if (mounted) setState(() => directory = selected);
      if (mounted) await _automaticUpdate();
    } on PlatformException catch (failure) {
      if (mounted) setState(() => error = failure.message ?? '无法打开目录选择器');
    } catch (failure) {
      if (mounted) {
        setState(
          () => error = failure.toString().replaceFirst('Bad state: ', ''),
        );
      }
    } finally {
      _choosingDirectory = false;
    }
  }

  Future<void> _resetGameConfiguration() async {
    final confirmed = await showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (context) => AlertDialog(
        key: const Key('reset-config-confirmation'),
        backgroundColor: const Color(0xff262332),
        title: const Text('重置本机游戏配置？'),
        scrollable: true,
        content: const SizedBox(
          width: 390,
          child: Text(
            '将备份并重置 DNF.cfg。游戏内记住的账号名、频道记录和画面、按键等设置可能丢失，需要重新设置。\n\n'
            '同一 Windows 用户下的其他 DNF 客户端也可能共用这份配置。请先退出所有 DNF 游戏。\n\n'
            '服务器上的账号、角色、装备和进度不会删除。登录器单独记住的账号及客户端目录会保留。',
            style: TextStyle(fontSize: 13, height: 1.7),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            key: const Key('confirm-reset-config'),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('备份并重置'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    try {
      final result = await model.resetGameConfiguration();
      if (!mounted) return;
      setState(() => error = '');
      await _article(
        result['reset'] == true ? '游戏配置已重置' : '无需重置',
        result['reset'] == true
            ? '原配置已完整备份。重新登录后会重建频道记录；进入游戏后请检查个人设置。\n\n备份位置：\n${result['backupPath']}\n\n如需恢复，请退出所有 DNF 游戏，保留新配置后，将这份备份改回 DNF.cfg。'
            : '未发现 DNF.cfg，没有改动任何文件。下次登录时会自动初始化所需的频道配置。',
      );
    } catch (failure) {
      if (mounted) {
        await _article(
          '游戏配置未重置',
          failure.toString().replaceFirst('Bad state: ', ''),
        );
      }
    }
  }

  Future<void> _diagnostics() => showDialog<void>(
    context: context,
    builder: (context) => Dialog(
      backgroundColor: Colors.transparent,
      child: Glass(
        solid: model.reducedEffects,
        color: const Color(0xe5221f30),
        child: SizedBox(width: 600, child: DiagnosticsPanel(model: model)),
      ),
    ),
  );

  Future<void> _settings() => showDialog<void>(
    context: context,
    builder: (context) => Dialog(
      backgroundColor: Colors.transparent,
      child: AnimatedBuilder(
        animation: model,
        builder: (context, _) => Glass(
          solid: model.reducedEffects,
          color: const Color(0xe5221f30),
          child: SizedBox(
            width: 460,
            child: SingleChildScrollView(
              child: Padding(
                padding: const EdgeInsets.all(28),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        const Expanded(
                          child: Text(
                            '登录器设置',
                            style: TextStyle(fontSize: 20, color: gold),
                          ),
                        ),
                        _icon(
                          Icons.close_rounded,
                          '关闭设置',
                          () => Navigator.pop(context),
                        ),
                      ],
                    ),
                    const SizedBox(height: 24),
                    Row(
                      children: [
                        const Expanded(child: Text('更新线路')),
                        Flexible(
                          child: Text(
                            model.source,
                            style: const TextStyle(fontSize: 12),
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                      ],
                    ),
                    SwitchListTile(
                      key: const Key('auto-update'),
                      contentPadding: EdgeInsets.zero,
                      title: const Text('自动更新', style: TextStyle(fontSize: 13)),
                      subtitle: const Text(
                        '启动时更新登录器和客户端文件，登录器更新后自动重启',
                        style: TextStyle(fontSize: 11, color: muted),
                      ),
                      value: model.autoUpdate,
                      onChanged: model.busy || !model.connected
                          ? null
                          : _setAutoUpdate,
                      activeThumbColor: gold,
                    ),
                    SwitchListTile(
                      key: const Key('reduced-effects'),
                      contentPadding: EdgeInsets.zero,
                      title: const Text(
                        '低特效模式',
                        style: TextStyle(fontSize: 13),
                      ),
                      subtitle: const Text(
                        '关闭背景模糊，保留清晰界面',
                        style: TextStyle(fontSize: 11, color: muted),
                      ),
                      value: model.reducedEffects,
                      onChanged: model.busy || !model.connected
                          ? null
                          : (v) => model.preferences(effects: v),
                      activeThumbColor: gold,
                    ),
                    const Divider(height: 34, color: Color(0x22ffffff)),
                    ListTile(
                      key: const Key('diagnostics-settings'),
                      contentPadding: EdgeInsets.zero,
                      leading: const Icon(
                        Icons.monitor_heart_outlined,
                        color: violet,
                      ),
                      title: const Text(
                        '增强诊断日志',
                        style: TextStyle(fontSize: 13),
                      ),
                      subtitle: Text(
                        model.diagnosticsEnabled
                            ? '已开启 · 标记问题与导出片段'
                            : '自动关联游戏，收集问题前后记录',
                        style: const TextStyle(fontSize: 11, color: muted),
                      ),
                      trailing: const Icon(Icons.chevron_right_rounded),
                      onTap: () {
                        Navigator.pop(context);
                        _diagnostics();
                      },
                    ),
                    const Divider(height: 26, color: Color(0x22ffffff)),
                    const Text(
                      '本机游戏配置',
                      style: TextStyle(color: gold, fontSize: 12),
                    ),
                    const SizedBox(height: 8),
                    const Text(
                      '遇到“频道配置过大”等问题时，可备份并重置游戏配置。操作前会说明影响并再次确认。',
                      style: TextStyle(fontSize: 11, color: muted, height: 1.7),
                    ),
                    const SizedBox(height: 8),
                    SizedBox(
                      width: double.infinity,
                      child: OutlinedButton(
                        key: const Key('reset-game-config'),
                        onPressed: model.busy || !model.connected
                            ? null
                            : _resetGameConfiguration,
                        child: Text(
                          model.resettingConfiguration
                              ? '正在备份并重置…'
                              : '重置本机游戏配置',
                        ),
                      ),
                    ),
                    const Divider(height: 26, color: Color(0x22ffffff)),
                    const Text(
                      '更新与安全',
                      style: TextStyle(color: gold, fontSize: 12),
                    ),
                    const SizedBox(height: 8),
                    const Text(
                      '设置会保存在本机。游戏更新使用本服批准的内容来源；账号密码不会发送到内容源。',
                      style: TextStyle(fontSize: 11, color: muted, height: 1.7),
                    ),
                    const SizedBox(height: 16),
                    SizedBox(
                      width: double.infinity,
                      child: OutlinedButton(
                        key: const Key('settings-check-update'),
                        onPressed: model.busy || !model.connected
                            ? null
                            : () {
                                Navigator.pop(context);
                                _beginUpdate();
                              },
                        child: const Text('检查更新'),
                      ),
                    ),
                    if (model.hasLauncherUpdate)
                      SizedBox(
                        width: double.infinity,
                        child: FilledButton(
                          onPressed: model.busy
                              ? null
                              : () async {
                                  try {
                                    if (await model.updateLauncher()) {
                                      await windowAction('close');
                                    }
                                  } catch (failure) {
                                    if (context.mounted) {
                                      await _article(
                                        '登录器更新未完成',
                                        failure.toString().replaceFirst(
                                          'Bad state: ',
                                          '',
                                        ),
                                      );
                                    }
                                  }
                                },
                          child: const Text('更新登录器并重启'),
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    ),
  );
}
