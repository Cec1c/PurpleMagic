import ko from "./locales/ko-KR.json" with { type: "json" };

const koMessages = ko as Record<string, string>;

export type Locale = "ko-KR" | "zh-CN";

const preferenceKey = "purplemagic-gm-locale-v1";

function readLocale(): Locale {
  if (typeof window === "undefined") return "zh-CN";
  try {
    const saved = window.localStorage.getItem(preferenceKey);
    if (saved === "ko-KR" || saved === "zh-CN") return saved;
  } catch {
    // Storage can be disabled by browser policy.
  }
  return navigator.language.toLowerCase().startsWith("ko")
    ? "ko-KR"
    : "zh-CN";
}

let locale = readLocale();

function syncDocumentLocale(value: Locale): void {
  if (typeof document === "undefined") return;
  document.documentElement.lang = value;
  document.title = value === "ko-KR"
    ? "PurpleMagic · GM 운영 도구"
    : "PurpleMagic · GM 工作台";
}

syncDocumentLocale(locale);

export function getLocale(): Locale {
  return locale;
}

export function setLocale(value: Locale): void {
  locale = value;
  try {
    if (typeof window !== "undefined") window.localStorage.setItem(preferenceKey, value);
  } catch {
    // Keep the selection for this page session when storage is unavailable.
  }
  syncDocumentLocale(value);
}

export function t(source: string): string {
  if (locale !== "ko-KR") return source;
  const exact = koMessages[source];
  if (exact) return exact;
  const dynamic: Array<[RegExp, (match: RegExpMatchArray) => string]> = [
    [/^选择账号 (.+)$/, (m) => `${m[1]} 계정 선택`],
    [/^选择角色 (.+)$/, (m) => `${m[1]} 캐릭터 선택`],
    [/^选择邮件 (.+)$/, (m) => `우편 ${m[1]} 선택`],
    [/^选择任务 (.+)$/, (m) => `퀘스트 ${m[1]} 선택`],
    [/^收件角色 (.+)$/, (m) => `수신 캐릭터 ${m[1]}`],
    [/^移除群 (.+)$/, (m) => `그룹 ${m[1]} 제거`],
    [/^移除 (.+)$/, (m) => `${m[1]} 제거`],
    [/^选择 (.+)$/, (m) => `${m[1]} 선택`],
    [/^角色邮箱 \((\d+)\)$/, (m) => `캐릭터 우편함 (${m[1]})`],
    [/^(\d+) 个活动已选$/, (m) => `${m[1]}개 이벤트 선택됨`],
    [/^剩余 (\d+) 天$/, (m) => `남은 기간 ${m[1]}일`],
    [/^选择操作角色（(\d+)）$/, (m) => `작업할 캐릭터 선택 (${m[1]}명)`],
    [/^重试 (\d+) 个失败收件人$/, (m) => `실패 수신자 ${m[1]}명 다시 시도`],
    [/^继续发放 (\d+) 个收件人$/, (m) => `수신자 ${m[1]}명 지급 계속`],
  ];
  for (const [pattern, format] of dynamic) {
    const match = source.match(pattern);
    if (match) return format(match);
  }
  return source;
}
