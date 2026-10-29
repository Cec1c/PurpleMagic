import 'package:flutter/material.dart';

class LauncherAccountField extends StatefulWidget {
  const LauncherAccountField({
    super.key,
    required this.label,
    required this.hint,
    required this.controller,
    required this.icon,
    required this.fieldKey,
    this.secret = false,
    this.toggle = true,
    this.enabled = true,
    this.onSubmitted,
    this.onChanged,
  });
  final String label, hint, fieldKey;
  final TextEditingController controller;
  final IconData icon;
  final bool secret, toggle, enabled;
  final ValueChanged<String>? onSubmitted, onChanged;
  @override
  State<LauncherAccountField> createState() => _LauncherAccountFieldState();
}

class _LauncherAccountFieldState extends State<LauncherAccountField> {
  bool obscure = true;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        widget.label,
        style: const TextStyle(fontSize: 11, color: Color(0xffd0c8db)),
      ),
      const SizedBox(height: 7),
      SizedBox(
        height: 42,
        child: TextField(
          key: Key(widget.fieldKey),
          controller: widget.controller,
          obscureText: widget.secret && obscure,
          enabled: widget.enabled,
          autocorrect: false,
          enableSuggestions: false,
          textInputAction: widget.secret
              ? TextInputAction.done
              : TextInputAction.next,
          onSubmitted: widget.onSubmitted,
          onChanged: widget.onChanged,
          style: const TextStyle(fontSize: 12, color: Color(0xfff0edf5)),
          cursorColor: const Color(0xffe3cfa9),
          decoration: InputDecoration(
            hintText: widget.hint,
            hintStyle: const TextStyle(fontSize: 12, color: Color(0xff8f879e)),
            contentPadding: const EdgeInsets.symmetric(
              horizontal: 14,
              vertical: 12,
            ),
            filled: true,
            fillColor: const Color(0x58201e2b),
            prefixIcon: Icon(
              widget.icon,
              size: 17,
              color: const Color(0xffa99fb8),
            ),
            prefixIconConstraints: const BoxConstraints(minWidth: 42),
            suffixIcon: widget.secret && widget.toggle
                ? IconButton(
                    key: Key('${widget.fieldKey}-visibility'),
                    tooltip: obscure ? '显示密码' : '隐藏密码',
                    onPressed: () => setState(() => obscure = !obscure),
                    icon: Icon(
                      obscure
                          ? Icons.visibility_off_outlined
                          : Icons.visibility_outlined,
                      size: 16,
                      color: const Color(0xffb3aebf),
                    ),
                  )
                : null,
            enabledBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(9),
              borderSide: const BorderSide(color: Color(0x33c8bdd9)),
            ),
            focusedBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(9),
              borderSide: const BorderSide(
                color: Color(0xffb7a9dd),
                width: 1.2,
              ),
            ),
            disabledBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(9),
              borderSide: const BorderSide(color: Color(0x22c8bdd9)),
            ),
          ),
        ),
      ),
    ],
  );
}
