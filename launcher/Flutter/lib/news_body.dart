import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
import 'package:url_launcher/url_launcher.dart';

Uri? newsLink(String value) {
  final uri = Uri.tryParse(value);
  return uri != null &&
          (uri.scheme == 'https' || uri.scheme == 'http') &&
          uri.host.isNotEmpty &&
          uri.userInfo.isEmpty
      ? uri
      : null;
}

class NewsBody extends StatelessWidget {
  const NewsBody({super.key, required this.body, this.markdown = false});
  final String body;
  final bool markdown;

  @override
  Widget build(BuildContext context) {
    const text = TextStyle(fontSize: 13, height: 1.8, color: Color(0xffddd5e7));
    if (!markdown) return SelectableText(body, style: text);
    return MarkdownBody(
      data: body,
      selectable: true,
      styleSheet: MarkdownStyleSheet.fromTheme(Theme.of(context)).copyWith(
        p: text,
        h1: text.copyWith(fontSize: 24, fontWeight: FontWeight.bold),
        h2: text.copyWith(fontSize: 21, fontWeight: FontWeight.bold),
        h3: text.copyWith(fontSize: 17, fontWeight: FontWeight.bold),
        a: text.copyWith(
          color: const Color(0xffcfade8),
          decoration: TextDecoration.underline,
        ),
        code: text.copyWith(
          fontFamily: 'Consolas',
          backgroundColor: const Color(0x332c213b),
        ),
        blockquoteDecoration: const BoxDecoration(
          border: Border(left: BorderSide(color: Color(0xffb7a9dd), width: 3)),
        ),
        blockquotePadding: const EdgeInsets.symmetric(
          horizontal: 16,
          vertical: 8,
        ),
        tableBody: text,
        tableHead: text.copyWith(fontWeight: FontWeight.bold),
        tableCellsPadding: const EdgeInsets.all(8),
      ),
      onTapLink: (_, href, _) async {
        final uri = newsLink(href ?? '');
        if (uri == null) return;
        try {
          final opened = await launchUrl(
            uri,
            mode: LaunchMode.externalApplication,
          );
          if (!opened && context.mounted) {
            ScaffoldMessenger.maybeOf(context)
                ?.showSnackBar(const SnackBar(content: Text('链接未能打开')));
          }
        } catch (_) {
          if (context.mounted) {
            ScaffoldMessenger.maybeOf(context)
                ?.showSnackBar(const SnackBar(content: Text('链接未能打开')));
          }
        }
      },
      imageBuilder: (uri, title, alt) =>
          newsLink(uri.toString()) == null || uri.scheme != 'https'
          ? Text(alt ?? '', style: text)
          : ConstrainedBox(
              constraints: const BoxConstraints(maxHeight: 400),
              child: Image.network(
                uri.toString(),
                fit: BoxFit.contain,
                errorBuilder: (_, _, _) => Text(alt ?? '图片暂时无法读取', style: text),
              ),
            ),
    );
  }
}
