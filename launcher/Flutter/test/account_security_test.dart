import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:purplemagic/account_security_panel.dart';
import 'package:purplemagic/launcher_controller.dart';

class BindingCore extends CoreClient {
  final calls = <String>[];
  bool fail = false;
  @override
  Future<Map<String, dynamic>> call(
    String method, [
    Map<String, dynamic>? args,
  ]) async {
    calls.add(method);
    if (fail) throw AccountFailure('冷却中', 'account_cooldown', 600);
    return {
      'accountResult': {
        'message': '绑定码已生成',
        'bindingCode': 'a' * 32,
        'expiresAt': DateTime.now().millisecondsSinceEpoch ~/ 1000 + 600,
      },
    };
  }

  @override
  void dispose() {}
}

void main() {
  test(
    'login and change-password honor server cooldown for the same account',
    () async {
      final core = BindingCore()..fail = true;
      final model = LauncherController(client: core);
      addTearDown(model.dispose);
      await expectLater(
        model.submit(false, 'Player', 'secret'),
        throwsA(isA<AccountFailure>()),
      );
      await expectLater(
        model.accountSecurity(true, 'PLAYER', 'secret', 'changed'),
        throwsA(isA<AccountFailure>()),
      );
      expect(core.calls, ['login']);
      expect(model.accountCooldown('player'), greaterThan(590));
      core.fail = false;
      await model.accountSecurity(false, 'other', 'secret', '');
      expect(core.calls.length, 2);
    },
  );
  testWidgets(
    'binding panel gets a code without a game installation and clears password',
    (tester) async {
      final core = BindingCore();
      final model = LauncherController(client: core);
      addTearDown(model.dispose);
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: AccountSecurityPanel(
              model: model,
              changing: false,
              initialAccount: 'player',
            ),
          ),
        ),
      );
      await tester.enterText(find.byType(TextField).at(1), 'secret-password');
      await tester.tap(find.widgetWithText(FilledButton, '获取绑定码'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 200));
      expect(core.calls, ['bindingCode']);
      expect(find.textContaining('/绑定 player '), findsOneWidget);
      expect(
        tester.widget<TextField>(find.byType(TextField).at(1)).controller!.text,
        isEmpty,
      );
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
  testWidgets('new passwords must match before calling server', (tester) async {
    final core = BindingCore();
    final model = LauncherController(client: core);
    addTearDown(model.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: AccountSecurityPanel(
            model: model,
            changing: true,
            initialAccount: 'player',
          ),
        ),
      ),
    );
    await tester.enterText(find.byType(TextField).at(1), 'secret-password');
    await tester.enterText(find.byType(TextField).at(2), 'changed-password');
    await tester.tap(find.widgetWithText(FilledButton, '确认修改'));
    await tester.pump();
    expect(core.calls, isEmpty);
    await tester.pumpWidget(const SizedBox.shrink());
  });
}
