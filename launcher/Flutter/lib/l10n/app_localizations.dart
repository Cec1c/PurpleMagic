import 'dart:ui';

import 'package:flutter/foundation.dart';

import 'app_localizations_data.dart';

Locale _systemLocale() =>
    PlatformDispatcher.instance.locale.languageCode == 'ko'
    ? const Locale('ko', 'KR')
    : const Locale('zh', 'CN');

final ValueNotifier<Locale> launcherLocale = ValueNotifier(_systemLocale());

String preferredLauncherLanguage() =>
    _systemLocale().languageCode == 'ko' ? 'ko-KR' : 'zh-CN';

void setLauncherLanguage(String value) {
  launcherLocale.value = value == 'ko-KR'
      ? const Locale('ko', 'KR')
      : const Locale('zh', 'CN');
}

class AppLocalizations {
  static String translate(String source) {
    final korean = launcherLocale.value.languageCode == 'ko';
    final exact = (korean ? launcherKorean : launcherChinese)[source];
    if (exact != null) return exact;
    if (korean) {
      final binding = RegExp(r'^账号已绑定 QQ (.+)，可以登录游戏$').firstMatch(source);
      if (binding != null) {
        return 'QQ ${binding.group(1)}에 계정이 연결되어 있어 게임에 로그인할 수 있습니다.';
      }
      final seconds = RegExp(r'^有效期剩余 (\d+) 秒$').firstMatch(source);
      if (seconds != null) return '유효 시간 ${seconds.group(1)}초 남음';
      final cooldown = RegExp(r'^验证冷却：?(\d+) 秒$').firstMatch(source);
      if (cooldown != null) return '인증 대기 ${cooldown.group(1)}초';
      final input = RegExp(r'^请输入(.+)$').firstMatch(source);
      if (input != null) return '${translate(input.group(1)!)} 입력';
      if (source.startsWith('原配置已完整备份。')) {
        final pathStart = source.indexOf('备份位置：\n');
        final restoreStart = source.indexOf('\n\n如需恢复');
        if (pathStart >= 0 && restoreStart > pathStart) {
          final path = source.substring(
            pathStart + '备份位置：\n'.length,
            restoreStart,
          );
          return '기존 게임 설정을 백업했습니다. 다시 로그인하면 채널 기록이 복원됩니다. 게임에 들어간 뒤 개인 설정을 확인하세요.\n\n백업 위치:\n$path\n\n복원하려면 모든 DNF 게임을 종료한 다음 백업 파일을 DNF.cfg로 되돌리세요.';
        }
      }
    }
    return source;
  }
}
