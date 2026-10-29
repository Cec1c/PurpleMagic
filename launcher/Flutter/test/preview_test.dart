import 'dart:io';
import 'dart:async';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:purplemagic/main.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final capture = GlobalKey();
  setUp(() => launchArguments = const []);
  tearDown(() => launchArguments = const []);
  setUpAll(() async {
    // Use the actual installed Windows font for offline render evidence.
    final font = File(
      '${Platform.environment['WINDIR'] ?? 'C:/Windows'}/Fonts/msyh.ttc',
    );
    final loader = FontLoader('Microsoft YaHei UI');
    loader.addFont(font.readAsBytes().then((b) => ByteData.sublistView(b)));
    await loader.load();
    final icons = FontLoader('MaterialIcons');
    icons.addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'));
    await icons.load();
  });

  Future<void> start(
    WidgetTester tester, {
    LauncherController? controller,
    Size size = const Size(1440, 810),
  }) async {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final model = controller ?? LauncherController(client: FakeCore());
    addTearDown(model.dispose);
    await tester.pumpWidget(
      RepaintBoundary(
        key: capture,
        child: LauncherPreview(controller: model),
      ),
    );
    await tester.runAsync(() async {
      final context = tester.element(find.byType(PreviewHome));
      await Future.wait(
        [
          'assets/background.webp',
          'assets/announcement.webp',
          'brand/witch.png',
        ].map((name) => precacheImage(AssetImage(name), context)),
      );
    });
    await tester.pumpAndSettle();
  }

  void mockDirectoryPicker(String? selected) {
    const channel = MethodChannel('dnf90.preview/window');
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          if ((call.method == 'chooseDirectory' ||
              call.method == 'saveDiagnosticLog')) {
            return selected;
          }
          return null;
        });
    addTearDown(
      () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, null),
    );
  }

  Future<void> screenshot(WidgetTester tester, String name) async {
    await tester.pump();
    await tester.runAsync(() async {
      final boundary =
          capture.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 1);
      final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
      final file = File('artifacts/screenshots/$name.png');
      await file.parent.create(recursive: true);
      await file.writeAsBytes(bytes!.buffer.asUint8List());
      image.dispose();
    });
    expect(tester.takeException(), isNull);
  }

  testWidgets('default build hides QQ tab and password uses the account card', (
    tester,
  ) async {
    await start(tester);
    expect(find.text('绑定 QQ'), findsNothing);
    await tester.tap(find.text('修改密码'));
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsNothing);
    expect(find.byKey(const Key('security-原密码')), findsOneWidget);
    expect(find.byKey(const Key('security-新密码')), findsOneWidget);
    await screenshot(tester, 'account-password-tab');
    await tester.tap(find.text('开始游戏'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('account')), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  });
  for (final size in [const Size(1440, 810), const Size(1120, 700)]) {
    testWidgets('optional binding tab shares card at ${size.width}', (
      tester,
    ) async {
      final core = FakeCore();
      core.state['qqBindingEnabled'] = true;
      await start(
        tester,
        controller: LauncherController(client: core),
        size: size,
      );
      expect(find.text('绑定 QQ'), findsOneWidget);
      await tester.tap(find.text('绑定 QQ'));
      await tester.pumpAndSettle();
      expect(find.byType(AlertDialog), findsNothing);
      expect(find.byKey(const Key('security-账号密码')), findsOneWidget);
      await screenshot(tester, 'account-binding-tab-${size.width.toInt()}');
      await tester.tap(find.text('修改密码'));
      await tester.pumpAndSettle();
      await screenshot(tester, 'account-password-tab-${size.width.toInt()}');
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  testWidgets('installed EXE version appears beside client status', (
    tester,
  ) async {
    final fake = FakeCore();
    fake.state['clientVersion'] = 'V150';
    fake.state['version'] = 'client-v153-new-release';
    final model = LauncherController(client: fake);
    await start(tester, controller: model);
    String status() => tester
        .widget<Text>(find.byKey(const Key('client-status')))
        .textSpan!
        .toPlainText();
    expect(status(), '客户端已就绪  ·  V150');
    await screenshot(tester, 'client-version-1440');
    tester.view.physicalSize = const Size(1000, 620);
    await tester.pumpAndSettle();
    await screenshot(tester, 'client-version-1000');
    fake.state['clientVersion'] = 'V153';
    fake.emit();
    await tester.pump();
    expect(status(), '客户端已就绪  ·  V153');
    fake.state.remove('clientVersion');
    fake.emit();
    await tester.pump();
    expect(status(), '客户端已就绪');
    expect(tester.takeException(), isNull);
  });

  testWidgets('diagnostic preference persists through other settings', (
    tester,
  ) async {
    final fake = FakeCore();
    final model = LauncherController(client: fake);
    await start(tester, controller: model, size: const Size(1000, 620));
    await tester.tap(find.byKey(const Key('settings')));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byKey(const Key('diagnostics-settings')));
    await tester.tap(find.byKey(const Key('diagnostics-settings')));
    await tester.pumpAndSettle();
    expect(find.text('已关闭'), findsOneWidget);
    await tester.tap(find.byKey(const Key('diagnostics-toggle')));
    await tester.pumpAndSettle();
    expect(model.diagnosticsEnabled, isFalse);
    expect(find.textContaining('异常调用栈'), findsOneWidget);
    await screenshot(tester, 'diagnostics-consent');
    await tester.tap(find.byKey(const Key('diagnostics-decline')));
    await tester.pumpAndSettle();
    expect(model.diagnosticsEnabled, isFalse);
    await tester.tap(find.byKey(const Key('diagnostics-toggle')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('diagnostics-consent')));
    await tester.pumpAndSettle();
    expect(model.diagnosticsEnabled, isTrue);
    await model.save(effects: true);
    expect(
      (fake.state['preferences'] as Map)['diagnostics']['consentVersion'],
      1,
    );
    expect(
      (fake.state['preferences'] as Map)['diagnostics']['enabled'],
      isTrue,
    );
    expect(find.byKey(const Key('diagnostics-active')), findsOneWidget);
    await screenshot(tester, 'diagnostics-waiting');
  });

  testWidgets('diagnostic elevation targets only the denied game session', (
    tester,
  ) async {
    final fake = FakeCore();
    (fake.state['preferences'] as Map)['diagnostics'] = {
      'enabled': true,
      'consentVersion': 1,
    };
    fake.state['diagnostics'] = {
      'enabled': true,
      'message': '基础日志正在记录',
      'instances': [
        {'id': 'needs-permission', 'crashStatus': 'access_denied'},
      ],
      'clips': [],
    };
    final model = LauncherController(client: fake);
    await start(tester, controller: model, size: const Size(1000, 620));
    await tester.tap(find.byKey(const Key('diagnostics-active')));
    await tester.pumpAndSettle();
    expect(find.text('提升诊断权限'), findsOneWidget);
    await screenshot(tester, 'diagnostics-permission');
    await tester.tap(find.text('提升诊断权限'));
    await tester.pumpAndSettle();
    expect(fake.calls.where((m) => m == 'diagnosticElevate').length, 1);
    expect(fake.diagnosticArguments, {'sessionId': 'needs-permission'});
  });

  testWidgets(
    'diagnostic launch rows export directly as log with cancel and failure',
    (tester) async {
      final fake = FakeCore();
      (fake.state['preferences'] as Map)['diagnostics'] = {
        'enabled': true,
        'consentVersion': 1,
        'problemType': 'interaction',
      };
      fake.state['diagnostics'] = {
        'enabled': true,
        'phase': 'basic',
        'message': '基础记录中',
        'instances': [
          {
            'id': 'session-a',
            'crashStatus': 'recording',
            'pid': 10,
            'window': 'DNF · 14:20:00',
            'status': 'basic',
            'exeVersion': 'V141',
            'selected': true,
          },
          {
            'id': 'session-b',
            'crashStatus': 'recording',
            'pid': 11,
            'window': 'DNF · 14:21:00',
            'status': 'basic',
            'exeVersion': 'V141',
            'selected': true,
          },
        ],
        'clips': [
          {
            'id': 'clip-a',
            'sessionId': 'session-a',
            'startedUtc': '2026-10-20T06:20:00Z',
            'endedUtc': '2026-10-20T06:20:30Z',
            'bytes': 24680,
            'categories': ['basic', 'at5t'],
          },
          {
            'id': 'clip-b',
            'sessionId': 'session-b',
            'startedUtc': '2026-10-20T06:21:00Z',
            'endedUtc': '2026-10-20T06:21:30Z',
            'bytes': 13579,
            'categories': ['basic'],
          },
        ],
      };
      mockDirectoryPicker(r'C:\Fixture\Exports\玩家日志.log');
      final model = LauncherController(client: fake);
      await start(tester, controller: model);
      await tester.tap(find.byKey(const Key('diagnostics-active')));
      await tester.pumpAndSettle();
      expect(find.text('问题类型'), findsNothing);
      expect(find.text('标记刚才的问题'), findsNothing);
      expect(find.byKey(const Key('log-clip-a')), findsOneWidget);
      expect(find.byKey(const Key('log-clip-b')), findsOneWidget);
      await tester.tap(find.byKey(const Key('export-clip-a')));
      await tester.pumpAndSettle();
      expect(fake.diagnosticArguments?['sessionId'], 'session-a');
      expect(fake.diagnosticArguments?['path'], r'C:\Fixture\Exports\玩家日志.log');
      expect(fake.diagnosticArguments?.containsKey('incidentIds'), isFalse);
      expect(find.textContaining('已保存：'), findsOneWidget);
      expect(model.busy, isFalse);
      await screenshot(tester, 'diagnostics-export');
      final beforeCancel = fake.calls
          .where((m) => m == 'diagnosticExport')
          .length;
      mockDirectoryPicker(null);
      await tester.tap(find.byKey(const Key('export-clip-b')));
      await tester.pumpAndSettle();
      expect(
        fake.calls.where((m) => m == 'diagnosticExport').length,
        beforeCancel,
      );
      mockDirectoryPicker(r'C:\Fixture\Exports\other.log');
      fake.diagnosticFailure = true;
      await tester.tap(find.byKey(const Key('export-clip-b')));
      await tester.pumpAndSettle();
      expect(model.phase, UpdatePhase.ready);
      expect(model.diagnosticError, '诊断写入失败');
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('footer shows the running core version, not content version', (
    tester,
  ) async {
    final fake = FakeCore();
    fake.state['launcherVersion'] = '0.7.31';
    fake.state['version'] = 'client-v999';
    await start(tester, controller: LauncherController(client: fake));
    expect(find.text('PurpleMagic  ·  0.7.31'), findsOneWidget);
    expect(find.text('PurpleMagic  ·  0.2.5'), findsNothing);
    fake.state['launcherVersion'] = '0.7.32';
    fake.emit();
    await tester.pump();
    expect(find.text('PurpleMagic  ·  0.7.32'), findsOneWidget);
    fake.state.remove('launcherVersion');
    fake.emit();
    await tester.pump();
    expect(find.text('PurpleMagic'), findsWidgets);
    expect(find.text('PurpleMagic  ·  client-v999'), findsNothing);
  });

  testWidgets(
    'login, validation, reveal, local submission, and clearing secrets',
    (tester) async {
      await start(tester);
      await screenshot(tester, 'home-1440');
      await tester.tap(find.byKey(const Key('primary')));
      await tester.pump();
      expect(find.text('请输入账号和密码'), findsOneWidget);
      await tester.enterText(find.byKey(const Key('account')), 'adventurer');
      await tester.enterText(
        find.byKey(const Key('password')),
        'demo-password',
      );
      expect(
        tester.widget<TextField>(find.byKey(const Key('password'))).obscureText,
        isTrue,
      );
      await tester.tap(find.byKey(const Key('password-visibility')));
      await tester.pump();
      expect(
        tester.widget<TextField>(find.byKey(const Key('password'))).obscureText,
        isFalse,
      );
      await tester.tap(find.byKey(const Key('password-visibility')));
      await tester.tap(find.byKey(const Key('primary')));
      await tester.pump();
      expect(
        tester.widget<FilledButton>(find.byKey(const Key('primary'))).onPressed,
        isNull,
      );
      await tester.pump(const Duration(milliseconds: 900));
      await tester.pumpAndSettle();
      expect(find.textContaining('客户端正在运行'), findsWidgets);
      expect(
        tester
            .widget<TextField>(find.byKey(const Key('password')))
            .controller!
            .text,
        isEmpty,
      );
      await screenshot(tester, 'login-result');
    },
  );

  testWidgets(
    'registration mismatch, confirmation and registered preview state',
    (tester) async {
      await start(tester);
      await tester.tap(find.text('账号注册'));
      await tester.pumpAndSettle();
      await screenshot(tester, 'register');
      await tester.enterText(
        find.byKey(const Key('account')),
        'new_adventurer',
      );
      await tester.enterText(
        find.byKey(const Key('password')),
        'demo-password',
      );
      await tester.enterText(
        find.byKey(const Key('confirmation')),
        'different',
      );
      await tester.tap(find.byKey(const Key('primary')));
      await tester.pump();
      expect(find.text('两次输入的密码不一致'), findsOneWidget);
      await tester.enterText(
        find.byKey(const Key('confirmation')),
        'demo-password',
      );
      await tester.tap(find.byKey(const Key('primary')));
      await tester.pump(const Duration(milliseconds: 900));
      await tester.pumpAndSettle();
      expect(find.text('注册成功'), findsOneWidget);
      await tester.tap(find.text('知道了'));
      await tester.pumpAndSettle();
    },
  );

  testWidgets(
    'update pauses without progress and blocks login until verified',
    (tester) async {
      final fake = FakeCore();
      final model = LauncherController(client: fake);
      await start(tester, controller: model);
      await tester.tap(find.byKey(const Key('check-update')));
      await tester.pumpAndSettle();
      expect(find.text('安装更新'), findsOneWidget);
      await tester.tap(find.byKey(const Key('check-update')));
      await tester.pump(const Duration(seconds: 3));
      expect(model.phase, UpdatePhase.downloading);
      expect(
        tester.widget<FilledButton>(find.byKey(const Key('primary'))).onPressed,
        isNull,
      );
      await screenshot(tester, 'updating');
      await tester.tap(find.byKey(const Key('pause-update')));
      await tester.pump();
      final paused = model.progress;
      await tester.pump(const Duration(seconds: 3));
      expect(model.progress, paused);
      expect(model.phase, UpdatePhase.paused);
      await screenshot(tester, 'paused');
      await tester.tap(find.byKey(const Key('pause-update')));
      await tester.pump();
      fake.finishUpdate();
      await tester.pump(const Duration(seconds: 1));
      await tester.pumpAndSettle();
      expect(model.phase, UpdatePhase.ready);
      expect(
        tester.widget<FilledButton>(find.byKey(const Key('primary'))).onPressed,
        isNotNull,
      );
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('home prioritizes launcher over content and closes for restart', (
    tester,
  ) async {
    final fake = FakeCore()..launcherRestart = true;
    fake.state['hasLauncherUpdate'] = true;
    fake.state['hasUpdate'] = true;
    fake.state['message'] = '上次更新失败，已保留旧版';
    final actions = <String>[];
    const channel = MethodChannel('dnf90.preview/window');
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(channel, (
      call,
    ) async {
      actions.add(call.method);
      return null;
    });
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        channel,
        null,
      ),
    );
    await start(tester, controller: LauncherController(client: fake));
    expect(find.text('更新登录器'), findsOneWidget);
    await tester.tap(find.byKey(const Key('check-update')));
    await tester.pumpAndSettle();
    expect(fake.calls, ['initialize', 'updateLauncher']);
    expect(fake.launcherArguments, {'uiPid': pid});
    expect(actions, ['close']);
  });

  testWidgets('manual launcher failure is visible and can be retried', (
    tester,
  ) async {
    final fake = FakeCore()..launcherError = '运行目录访问被拒绝（Windows 错误 5）';
    fake.state['hasLauncherUpdate'] = true;
    final model = LauncherController(client: fake);
    await start(tester, controller: model);
    await tester.tap(find.byKey(const Key('check-update')));
    await tester.pumpAndSettle();
    expect(model.phase, UpdatePhase.error);
    expect(find.text(fake.launcherError!), findsOneWidget);
    fake.launcherError = null;
    await tester.tap(find.byKey(const Key('check-update')));
    await tester.pumpAndSettle();
    expect(fake.calls.where((c) => c == 'updateLauncher').length, 2);
    expect(fake.calls, isNot(contains('update')));
    expect(model.phase, UpdatePhase.ready);
  });

  testWidgets(
    'rollback launch skips automatic retry but permits manual retry',
    (tester) async {
      launchArguments = const ['--update-retry-required'];
      final fake = FakeCore();
      fake.state['hasLauncherUpdate'] = true;
      (fake.state['preferences'] as Map)['autoCheck'] = true;
      await start(tester, controller: LauncherController(client: fake));
      await tester.pump(const Duration(seconds: 10));
      expect(fake.calls, ['initialize']);
      await tester.tap(find.byKey(const Key('check-update')));
      await tester.pumpAndSettle();
      expect(fake.calls, ['initialize', 'updateLauncher']);
    },
  );

  testWidgets('signed news, preferences, and directory bind to the core', (
    tester,
  ) async {
    final fake = FakeCore();
    final model = LauncherController(client: fake);
    await start(tester, controller: model);
    await tester.tap(find.text('指南').first);
    await tester.pumpAndSettle();
    expect(find.text('常见问题与连接帮助'), findsOneWidget);
    expect(find.text('本服版本发布'), findsNothing);
    await tester.tap(find.text('常见问题与连接帮助'));
    await tester.pumpAndSettle();
    await screenshot(tester, 'announcement-detail');
    await tester.tap(find.text('知道了'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('settings')));
    await tester.pumpAndSettle();
    await screenshot(tester, 'settings');
    await tester.tap(find.byKey(const Key('reduced-effects')));
    await tester.pumpAndSettle();
    expect(model.reducedEffects, isTrue);
    await tester.tap(find.byTooltip('关闭设置'));
    await tester.pumpAndSettle();
    mockDirectoryPicker(r'E:\Games\DNF 90CN');
    await tester.tap(find.byKey(const Key('directory')));
    await tester.pumpAndSettle();
    expect(find.text(r'E:\Games\DNF 90CN'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets(
    'automatic updates default on and request restart after self-update',
    (tester) async {
      final fake = FakeCore()..autoRestart = true;
      (fake.state['preferences'] as Map<String, dynamic>).remove('autoCheck');
      final actions = <String>[];
      const channel = MethodChannel('dnf90.preview/window');
      tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(channel, (
        call,
      ) async {
        actions.add(call.method);
        return null;
      });
      addTearDown(
        () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
          channel,
          null,
        ),
      );
      final model = LauncherController(client: fake);
      await start(tester, controller: model);
      expect(model.autoUpdate, isTrue);
      expect(fake.calls, ['initialize', 'autoUpdate']);
      expect(fake.autoArguments, {'uiPid': pid});
      expect(actions, ['close']);
    },
  );

  testWidgets(
    'opt-out survives startup, opt-in runs immediately, manual updates remain available',
    (tester) async {
      final fake = FakeCore();
      final model = LauncherController(client: fake);
      await start(tester, controller: model);
      expect(fake.calls, ['initialize']);
      await tester.tap(find.byKey(const Key('settings')));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<SwitchListTile>(find.byKey(const Key('auto-update')))
            .value,
        isFalse,
      );
      await tester.tap(find.byKey(const Key('auto-update')));
      await tester.pumpAndSettle();
      expect(model.autoUpdate, isTrue);
      expect(fake.calls.where((c) => c == 'autoUpdate').length, 1);
      await screenshot(tester, 'settings-auto-update');
      await tester.tap(find.byKey(const Key('auto-update')));
      await tester.pumpAndSettle();
      expect((fake.state['preferences'] as Map)['autoCheck'], isFalse);
      expect(fake.calls.where((c) => c == 'autoUpdate').length, 1);
      await tester.tap(find.byTooltip('关闭设置'));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('check-update')));
      await tester.pumpAndSettle();
      expect(fake.calls.last, 'check');
      await tester.tap(find.byKey(const Key('check-update')));
      await tester.pump();
      expect(fake.calls.last, 'update');
      fake.finishUpdate();
      await tester.pumpAndSettle();
    },
  );

  testWidgets(
    'automatic failure remains visible without closing or repeating updates',
    (tester) async {
      final fake = FakeCore()..autoError = '内容源暂时不可用';
      (fake.state['preferences'] as Map<String, dynamic>)['autoCheck'] = true;
      final model = LauncherController(client: fake);
      await start(tester, controller: model);
      expect(model.phase, UpdatePhase.error);
      expect(model.message, fake.autoError);
      expect(model.busy, isFalse);
      expect(fake.calls.where((c) => c == 'autoUpdate').length, 1);
      await tester.pump(const Duration(seconds: 10));
      expect(fake.calls.where((c) => c == 'autoUpdate').length, 1);
    },
  );

  testWidgets(
    'choosing a client directory starts automatic updates when enabled',
    (tester) async {
      final fake = FakeCore();
      (fake.state['preferences'] as Map<String, dynamic>)['autoCheck'] = true;
      final model = LauncherController(client: fake);
      await start(tester, controller: model);
      mockDirectoryPicker(r'E:\Games\NewClient');
      await tester.tap(find.byKey(const Key('directory')));
      await tester.pumpAndSettle();
      expect(model.clientDirectory, r'E:\Games\NewClient');
      expect(fake.calls, [
        'initialize',
        'autoUpdate',
        'preferences',
        'autoUpdate',
      ]);
    },
  );

  testWidgets('canceling native directory selection preserves preferences', (
    tester,
  ) async {
    final fake = FakeCore();
    final model = LauncherController(client: fake);
    await start(tester, controller: model);
    final original = model.clientDirectory;
    final calls = List<String>.from(fake.calls);
    mockDirectoryPicker(null);
    await tester.tap(find.byKey(const Key('directory')));
    await tester.pumpAndSettle();
    expect(model.clientDirectory, original);
    expect(fake.calls, calls);
  });

  testWidgets(
    'directory picker carries Unicode paths and blocks duplicate dialogs',
    (tester) async {
      final fake = FakeCore();
      final model = LauncherController(client: fake);
      await start(tester, controller: model);
      const channel = MethodChannel('dnf90.preview/window');
      final selection = Completer<String?>();
      var calls = 0;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (call) async {
            expect(call.method, 'chooseDirectory');
            expect(call.arguments['initialDirectory'], model.clientDirectory);
            calls++;
            return selection.future;
          });
      addTearDown(
        () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .setMockMethodCallHandler(channel, null),
      );
      await tester.tap(find.byKey(const Key('directory')));
      await tester.pump();
      await tester.tap(find.byKey(const Key('directory')));
      await tester.pump();
      expect(calls, 1);
      selection.complete(r'E:\游戏目录\阿拉德 client');
      await tester.pumpAndSettle();
      expect(model.clientDirectory, r'E:\游戏目录\阿拉德 client');
    },
  );

  testWidgets(
    'configuration reset requires confirmation and retains launcher account',
    (tester) async {
      final fake = FakeCore();
      (fake.state['preferences'] as Map<String, dynamic>)['account'] =
          'remembered-user';
      final model = LauncherController(client: fake);
      await start(tester, controller: model, size: const Size(1000, 620));
      await tester.tap(find.byKey(const Key('settings')));
      await tester.pumpAndSettle();
      await screenshot(tester, 'settings-reset');
      await tester.tap(find.byKey(const Key('reset-game-config')));
      await tester.pumpAndSettle();
      expect(find.textContaining('账号名'), findsOneWidget);
      expect(find.textContaining('其他 DNF 客户端'), findsOneWidget);
      await screenshot(tester, 'reset-confirmation');
      expect(fake.calls, isNot(contains('resetGameConfiguration')));
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(fake.calls, isNot(contains('resetGameConfiguration')));
      await tester.tap(find.byKey(const Key('reset-game-config')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('confirm-reset-config')));
      await tester.pumpAndSettle();
      expect(fake.resetArguments, {'confirmed': true});
      expect(fake.calls.where((c) => c == 'resetGameConfiguration').length, 1);
      expect(find.text('游戏配置已重置'), findsOneWidget);
      expect(find.textContaining('DNF.cfg.fixture.bak'), findsOneWidget);
      expect(model.account, 'remembered-user');
      expect(tester.takeException(), isNull);
      await screenshot(tester, 'reset-complete');
    },
  );

  testWidgets(
    'configuration reset reports running-game refusal and disables busy actions',
    (tester) async {
      final fake = FakeCore()..resetError = '需要重置配置，请先退出所有游戏客户端。';
      final model = LauncherController(client: fake);
      await start(tester, controller: model);
      await tester.tap(find.byKey(const Key('settings')));
      await tester.pumpAndSettle();
      fake.state['phase'] = 'downloading';
      fake.emit();
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<OutlinedButton>(find.byKey(const Key('reset-game-config')))
            .onPressed,
        isNull,
      );
      fake.state['phase'] = 'ready';
      fake.emit();
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('reset-game-config')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('confirm-reset-config')));
      await tester.pumpAndSettle();
      expect(find.text('游戏配置未重置'), findsOneWidget);
      expect(find.text(fake.resetError!), findsOneWidget);
      expect(find.text('游戏配置已重置'), findsNothing);
      expect(model.resettingConfiguration, isFalse);
    },
  );

  for (final size in [
    const Size(1600, 910),
    const Size(1120, 700),
    const Size(1000, 620),
  ]) {
    testWidgets(
      'desktop composition fits ${size.width.toInt()}x${size.height.toInt()}',
      (tester) async {
        await start(tester, size: size);
        await screenshot(tester, 'home-${size.width.toInt()}');
        final primary = tester.getRect(find.byKey(const Key('primary')));
        expect(primary.left, greaterThanOrEqualTo(0));
        expect(primary.right, lessThanOrEqualTo(size.width));
        expect(primary.bottom, lessThanOrEqualTo(size.height));
        await tester.tap(find.text('账号注册'));
        await tester.pumpAndSettle();
        expect(
          tester.getRect(find.byKey(const Key('primary'))).bottom,
          lessThanOrEqualTo(size.height),
        );
        expect(tester.takeException(), isNull);
      },
    );
  }
}

class FakeCore extends CoreClient {
  Map<String, dynamic>? diagnosticArguments;
  bool diagnosticFailure = false;

  final calls = <String>[];
  Map<String, dynamic>? autoArguments;
  bool autoRestart = false;
  String? autoError;
  Map<String, dynamic>? launcherArguments;
  bool launcherRestart = false;
  String? launcherError;
  Map<String, dynamic>? resetArguments;
  String? resetError;
  Completer<Map<String, dynamic>>? updating;
  final state = <String, dynamic>{
    'phase': 'ready',
    'message': '客户端检查完成，准备开始冒险',
    'progress': 0.0,
    'downloaded': 0,
    'total': 0,
    'canLogin': true,
    'hasUpdate': false,
    'hasLauncherUpdate': false,
    'source': '本服批准源',
    'version': '测试版本',
    'preferences': <String, dynamic>{
      'clientDirectory': r'D:\Games\DNF90',
      'account': '',
      'rememberAccount': true,
      'autoCheck': false,
      'reducedEffects': false,
    },
    'news': [
      {
        'title': '本服版本发布',
        'body': '来自本服签名发布的测试公告。',
        'category': '更新',
        'date': '10 / 13',
      },
      {
        'title': '客户端更新与文件校验说明',
        'body': '更新前请退出游戏。',
        'category': '更新',
        'date': '10 / 13',
      },
      {
        'title': '常见问题与连接帮助',
        'body': '选择配套客户端并检查连接。',
        'category': '指南',
        'date': '指南',
      },
    ],
  };
  void emit() => onState?.call(state);
  @override
  Future<Map<String, dynamic>> call(
    String method, [
    Map<String, dynamic>? args,
  ]) async {
    calls.add(method);
    switch (method) {
      case 'diagnosticElevate':
        diagnosticArguments = args;
        emit();
        return {'ok': true, 'state': state};
      case 'diagnosticExport':
        diagnosticArguments = args;
        if (diagnosticFailure) throw StateError('诊断写入失败');
        emit();
        return {
          'ok': true,
          'diagnosticExport': {
            'path': args?['path'],
            'bytes': 4096,
            'incidents': 1,
          },
        };
      case 'updateLauncher':
        launcherArguments = args;
        if (launcherError != null) throw StateError(launcherError!);
        emit();
        return {'ok': true, 'state': state, 'restart': launcherRestart};
      case 'autoUpdate':
        autoArguments = args;
        if (autoError != null) throw StateError(autoError!);
        emit();
        return {'ok': true, 'state': state, 'restart': autoRestart};
      case 'resetGameConfiguration':
        resetArguments = args;
        if (resetError != null) throw StateError(resetError!);
        state['phase'] = 'ready';
        emit();
        return {
          'ok': true,
          'state': state,
          'configurationReset': {
            'reset': true,
            'backupPath': r'C:\Fixture\DNF\DNF.cfg.fixture.bak',
          },
        };
      case 'preferences':
        if (args!.containsKey('password')) {
          throw StateError('Password must not be persisted');
        }
        state['preferences'] = Map<String, dynamic>.from(args);
        state['diagnostics'] = {
          ...?state['diagnostics'] as Map<String, dynamic>?,
          'enabled': args['diagnostics']?['enabled'] == true,
          'message': args['diagnostics']?['enabled'] == true ? '等待游戏' : '已关闭',
        };
      case 'login':
        state['phase'] = 'logging-in';
        state['canLogin'] = false;
        emit();
        await Future<void>.delayed(const Duration(milliseconds: 850));
        state['phase'] = 'ready';
        state['canLogin'] = true;
        state['message'] = '客户端正在运行';
      case 'register':
        await Future<void>.delayed(const Duration(milliseconds: 850));
      case 'check':
        state['hasUpdate'] = true;
        state['canLogin'] = false;
        state['message'] = '发现 2 个待更新文件';
      case 'update':
        state['phase'] = 'downloading';
        state['canLogin'] = false;
        state['progress'] = .3;
        state['downloaded'] = 30000000;
        state['total'] = 100000000;
        state['message'] = '正在下载 Script.pvf';
        emit();
        updating = Completer<Map<String, dynamic>>();
        return updating!.future;
      case 'pause':
        state['phase'] = 'paused';
        updating?.complete({'ok': true});
        updating = null;
    }
    emit();
    return {'ok': true, 'state': state};
  }

  void finishUpdate() {
    state['phase'] = 'ready';
    state['hasUpdate'] = false;
    state['canLogin'] = true;
    state['message'] = '更新完成';
    emit();
    updating?.complete({'ok': true});
    updating = null;
  }

  @override
  void dispose() {
    updating?.complete({'ok': true});
    updating = null;
  }
}
