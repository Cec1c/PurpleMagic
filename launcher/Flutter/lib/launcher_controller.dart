import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';

enum UpdatePhase {
  ready,
  checking,
  downloading,
  paused,
  installing,
  loggingIn,
  resetting,
  error,
}

class AccountFailure implements Exception {
  AccountFailure(this.message, this.code, this.retryAfter);
  final String message, code;
  final int retryAfter;
  @override
  String toString() => message;
}

abstract class CoreClient {
  void Function(Map<String, dynamic>)? onState;
  Future<Map<String, dynamic>> call(
    String method, [
    Map<String, dynamic>? args,
  ]);
  void dispose();
}

/// Only an owned child's standard streams carry account requests. No localhost
/// listener, shell interpolation, credential arguments or credential logging.
class ProcessCoreClient extends CoreClient {
  Process? _process;
  Future<void>? _starting;
  int _sequence = 0;
  final _pending = <int, Completer<Map<String, dynamic>>>{};
  bool _disposed = false;
  Future<void> _start() => _starting ??= _launch();
  Future<void> _launch() async {
    final directory = File(Platform.resolvedExecutable).parent.path;
    final executable = '$directory${Platform.pathSeparator}DNF90.Core.exe';
    if (!await File(executable).exists()) {
      throw StateError('缺少登录器核心文件，请解压完整安装包后重试。');
    }
    final process = await Process.start(
      executable,
      const [],
      workingDirectory: directory,
    );
    if (_disposed) {
      process.kill();
      throw StateError('登录器已关闭。');
    }
    _process = process;
    process.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen((line) {
          try {
            final reply = jsonDecode(line) as Map<String, dynamic>;
            final state = reply['state'];
            if (state is Map<String, dynamic>) onState?.call(state);
            final completer = _pending.remove(reply['id']);
            if (completer == null) return;
            if (reply['ok'] == true) {
              completer.complete(reply);
            } else {
              completer.completeError(
                AccountFailure(
                  reply['error'] as String? ?? '操作未完成。',
                  reply['errorCode'] as String? ?? '',
                  (reply['retryAfter'] as num?)?.toInt() ?? 0,
                ),
              );
            }
          } catch (_) {
            _failPending('核心通信中断，请重新打开登录器。');
          }
        }, onError: (_) => _failPending('核心通信中断。'));
    // Drain diagnostic output, but never persist it or expose paths/credentials.
    process.stderr.drain<void>();
    unawaited(
      process.exitCode.then((_) {
        _failPending('登录器核心已退出；请确认没有重复运行后重新打开。');
      }),
    );
  }

  void _failPending(String message) {
    for (final pending in _pending.values) {
      if (!pending.isCompleted) pending.completeError(StateError(message));
    }
    _pending.clear();
  }

  @override
  Future<Map<String, dynamic>> call(
    String method, [
    Map<String, dynamic>? args,
  ]) async {
    await _start();
    if (_disposed) throw StateError('登录器已关闭。');
    final id = ++_sequence;
    final response = Completer<Map<String, dynamic>>();
    _pending[id] = response;
    _process!.stdin.writeln(
      jsonEncode({'id': id, 'method': method, 'args': args ?? {}}),
    );
    return response.future;
  }

  @override
  void dispose() {
    _disposed = true;
    // EOF asks the host to cancel downloads and finish any atomic file commit.
    // Do not kill a helper that may be restoring a transaction.
    _process?.stdin.close();
    _failPending('登录器已关闭。');
  }
}

class LauncherController extends ChangeNotifier {
  LauncherController({CoreClient? client})
    : client = client ?? ProcessCoreClient() {
    this.client.onState = _receive;
  }
  final CoreClient client;
  UpdatePhase phase = UpdatePhase.ready;
  double progress = 0;
  int downloaded = 0, total = 0;
  bool reducedEffects = false, autoUpdate = true, canLogin = false;
  bool hasUpdate = false, hasLauncherUpdate = false, connected = false;
  String source = '本服批准源', message = '正在连接登录器核心…', version = '';
  String launcherVersion = '';
  String clientVersion = '';
  bool qqBindingEnabled = false;
  bool requireQQBinding = false;
  final _accountCooldowns = <String, DateTime>{};
  int accountCooldown(String account) {
    final remaining =
        _accountCooldowns[account.trim().toLowerCase()]
            ?.difference(DateTime.now())
            .inSeconds ??
        0;
    return remaining > 0 ? remaining : 0;
  }

  Future<Map<String, dynamic>> _accountCall(
    String method,
    String account,
    String password, [
    String newPassword = '',
  ]) async {
    final remaining = accountCooldown(account);
    if (remaining > 0) {
      throw AccountFailure(
        '请等待 $remaining 秒后重试',
        'account_cooldown',
        remaining,
      );
    }
    try {
      return await client.call(method, {
        'account': account.trim(),
        'password': password,
        'newPassword': newPassword,
      });
    } on AccountFailure catch (error) {
      if (error.retryAfter > 0) {
        _accountCooldowns[account.trim().toLowerCase()] = DateTime.now().add(
          Duration(seconds: error.retryAfter),
        );
      }
      if (error.code == 'qq_binding_required') requireQQBinding = true;
      if (!_disposed) notifyListeners();
      rethrow;
    }
  }

  Future<Map<String, dynamic>> accountSecurity(
    bool changing,
    String account,
    String password,
    String next,
  ) async {
    final reply = await _accountCall(
      changing ? 'changePassword' : 'bindingCode',
      account,
      password,
      next,
    );
    return Map<String, dynamic>.from(reply['accountResult'] as Map? ?? {});
  }

  String clientDirectory = '', account = '';
  bool rememberAccount = true;
  bool diagnosticsEnabled = false, diagnosticBusy = false;
  int diagnosticConsentVersion = 0;
  String diagnosticType = 'auto';
  Map<String, dynamic> diagnostics = const {};
  String diagnosticError = '';

  List<Map<String, dynamic>> news = [];
  bool _disposed = false, resettingConfiguration = false;
  bool _automaticUpdating = false;
  bool get busy =>
      _automaticUpdating ||
      resettingConfiguration ||
      {
        UpdatePhase.checking,
        UpdatePhase.downloading,
        UpdatePhase.installing,
        UpdatePhase.loggingIn,
        UpdatePhase.resetting,
      }.contains(phase);
  String get status => switch (phase) {
    UpdatePhase.checking => '正在检查更新',
    UpdatePhase.downloading => '正在下载更新',
    UpdatePhase.paused => '更新已暂停',
    UpdatePhase.installing => '正在安装更新',
    UpdatePhase.loggingIn => '正在连接游戏',
    UpdatePhase.resetting => '正在重置本机配置',
    UpdatePhase.error => '操作未完成',
    UpdatePhase.ready =>
      hasUpdate
          ? '有客户端更新'
          : canLogin
          ? '客户端已就绪'
          : '请选择客户端目录',
  };
  void _receive(Map<String, dynamic> state) {
    if (_disposed) return;
    qqBindingEnabled = state["qqBindingEnabled"] == true;
    requireQQBinding = state["requireQQBinding"] == true;
    phase = switch (state['phase']) {
      'checking' => UpdatePhase.checking,
      'downloading' => UpdatePhase.downloading,
      'paused' => UpdatePhase.paused,
      'installing' => UpdatePhase.installing,
      'logging-in' => UpdatePhase.loggingIn,
      'resetting' => UpdatePhase.resetting,
      'error' => UpdatePhase.error,
      _ => UpdatePhase.ready,
    };
    progress = (state['progress'] as num?)?.toDouble() ?? 0;
    downloaded = (state['downloaded'] as num?)?.toInt() ?? 0;
    total = (state['total'] as num?)?.toInt() ?? 0;
    canLogin = state['canLogin'] == true;
    hasUpdate = state['hasUpdate'] == true;
    hasLauncherUpdate = state['hasLauncherUpdate'] == true;
    message = state['message'] as String? ?? '';
    source = state['source'] as String? ?? '本服批准源';
    version = state['version'] as String? ?? '';
    launcherVersion = state['launcherVersion'] as String? ?? '';
    clientVersion = state['clientVersion'] as String? ?? '';
    news = (state['news'] as List? ?? []).cast<Map<String, dynamic>>();
    final prefs = state['preferences'] as Map<String, dynamic>? ?? {};
    clientDirectory = prefs['clientDirectory'] as String? ?? '';
    account = prefs['account'] as String? ?? '';
    rememberAccount = prefs['rememberAccount'] != false;
    autoUpdate = prefs['autoCheck'] != false;
    reducedEffects = prefs['reducedEffects'] == true;
    final diagnosticPrefs = prefs['diagnostics'] as Map<String, dynamic>? ?? {};
    diagnosticsEnabled = diagnosticPrefs['enabled'] == true;
    diagnosticConsentVersion = diagnosticPrefs['consentVersion'] as int? ?? 0;
    diagnosticType = diagnosticPrefs['problemType'] as String? ?? 'auto';
    diagnostics = state['diagnostics'] as Map<String, dynamic>? ?? const {};

    connected = true;
    notifyListeners();
  }

  Future<void> initialize() async {
    await client.call('initialize');
  }

  Future<void> reportHealthy(String path) async {
    await client.call('health', {'path': path});
  }

  Future<bool> automaticUpdate() async {
    if (_disposed || !connected || !autoUpdate || busy) return false;
    _automaticUpdating = true;
    notifyListeners();
    try {
      final result = await client.call('autoUpdate', {'uiPid': pid});
      return result['restart'] == true;
    } catch (error) {
      if (!_disposed) {
        message = error.toString().replaceFirst('Bad state: ', '');
        phase = UpdatePhase.error;
      }
      return false;
    } finally {
      _automaticUpdating = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> _background(String method) async {
    try {
      await client.call(method);
    } catch (error) {
      if (!_disposed) {
        message = error.toString().replaceFirst('Bad state: ', '');
        phase = UpdatePhase.error;
        notifyListeners();
      }
    }
  }

  Future<bool> beginUpdate() async {
    if (busy || !connected) return false;
    try {
      if (hasLauncherUpdate) return await updateLauncher();
      await _background(
        hasUpdate || phase == UpdatePhase.paused ? 'update' : 'check',
      );
      return false;
    } catch (error) {
      if (!_disposed) {
        message = error.toString().replaceFirst('Bad state: ', '');
        phase = UpdatePhase.error;
        notifyListeners();
      }
      return false;
    }
  }

  void pauseOrResume() {
    unawaited(_background(phase == UpdatePhase.paused ? 'update' : 'pause'));
  }

  Future<void> save({
    String? directory,
    String? remembered,
    bool? remember,
    bool? effects,
    bool? check,
    bool? diagnosticEnabled,
    int? diagnosticConsent,
    String? problemType,
  }) async {
    await client.call('preferences', {
      'clientDirectory': directory ?? clientDirectory,
      'account': remembered ?? account,
      'rememberAccount': remember ?? rememberAccount,
      'autoCheck': check ?? autoUpdate,
      'reducedEffects': effects ?? reducedEffects,
      'diagnostics': {
        'enabled': diagnosticEnabled ?? diagnosticsEnabled,
        'consentVersion': diagnosticConsent ?? diagnosticConsentVersion,
        'problemType': problemType ?? diagnosticType,
      },
    });
  }

  void preferences({bool? effects, bool? check}) {
    unawaited(
      save(effects: effects, check: check).catchError((Object error) {
        if (!_disposed) {
          message = error.toString().replaceFirst('Bad state: ', '');
          notifyListeners();
        }
      }),
    );
  }

  Future<Map<String, dynamic>?> diagnosticAction(
    String method, [
    Map<String, dynamic>? arguments,
  ]) async {
    if (diagnosticBusy) return null;
    diagnosticBusy = true;
    diagnosticError = '';
    notifyListeners();
    try {
      return await client.call(method, arguments);
    } catch (error) {
      diagnosticError = error.toString().replaceFirst('Bad state: ', '');
      return null;
    } finally {
      diagnosticBusy = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> diagnosticPreferences({
    bool? enabled,
    String? type,
    int? consent,
  }) async {
    if (diagnosticBusy) return;
    diagnosticBusy = true;
    diagnosticError = '';
    notifyListeners();
    try {
      await save(
        diagnosticEnabled: enabled,
        problemType: type,
        diagnosticConsent: consent,
      );
    } catch (error) {
      diagnosticError = error.toString().replaceFirst('Bad state: ', '');
    } finally {
      diagnosticBusy = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> submit(bool register, String account, String password) async {
    await _accountCall(register ? 'register' : 'login', account, password);
  }

  Future<bool> updateLauncher() async {
    final result = await client.call('updateLauncher', {'uiPid': pid});
    return result['restart'] == true;
  }

  Future<Map<String, dynamic>> resetGameConfiguration() async {
    if (busy) throw StateError('当前操作完成后再重置游戏配置。');
    resettingConfiguration = true;
    notifyListeners();
    try {
      final result = await client.call('resetGameConfiguration', {
        'confirmed': true,
      });
      return result['configurationReset'] as Map<String, dynamic>;
    } finally {
      resettingConfiguration = false;
      if (!_disposed) notifyListeners();
    }
  }

  @override
  void dispose() {
    _disposed = true;
    client.dispose();
    super.dispose();
  }
}
