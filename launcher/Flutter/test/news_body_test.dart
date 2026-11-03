import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:purplemagic/news_body.dart';

void main() {
  test('announcement links only open ordinary web URLs', () {
    expect(newsLink('https://example.test/help')?.host, 'example.test');
    for (final link in [
      'javascript:alert(1)',
      'file:///c:/a',
      'data:text/html,x',
      '//example.test',
      'https://user:pass@example.test',
    ]) {
      expect(newsLink(link), isNull);
    }
  });
  testWidgets('Markdown headings, emphasis, list, table and code render', (
    tester,
  ) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: NewsBody(
              markdown: true,
              body: '## 更新内容\n\n**加粗**\n\n- 修复公告\n\n| 功能 | 状态 |\n| --- | --- |\n| 删除 | 完成 |\n\n```text\n测试代码\n```',
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('更新内容', findRichText: true), findsOneWidget);
    expect(find.text('修复公告', findRichText: true), findsOneWidget);
    expect(find.text('删除', findRichText: true), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
  testWidgets('old plain text keeps literal Markdown and newlines', (
    tester,
  ) async {
    await tester.pumpWidget(
      const MaterialApp(home: NewsBody(body: '**原文**\n第二行')),
    );
    expect(find.text('**原文**\n第二行'), findsOneWidget);
  });
  testWidgets('local image paths are not opened', (tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: NewsBody(markdown: true, body: '![本地图片](file:///c:/secret.png)'),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byType(Image), findsNothing);
    expect(tester.takeException(), isNull);
  });
}
