import { readFileSync, writeFileSync } from "node:fs";

const korean = JSON.parse(readFileSync("lib/l10n/app_ko.arb", "utf8"));
const chinese = JSON.parse(readFileSync("lib/l10n/app_zh.arb", "utf8"));

function readMessages(arb) {
  const result = new Map();
  for (const [id, value] of Object.entries(arb)) {
    if (!/^m\d+$/.test(id) || typeof value !== "string") continue;
    const source = arb[`@${id}`]?.source;
    if (typeof source !== "string") throw new Error(`Missing source metadata for ${id}`);
    result.set(source, value);
  }
  return result;
}

const ko = readMessages(korean);
const zh = readMessages(chinese);
for (const source of ko.keys()) {
  if (!zh.has(source)) throw new Error(`Chinese locale is missing source message: ${source}`);
}
for (const source of zh.keys()) {
  if (!ko.has(source)) throw new Error(`Korean locale is missing source message: ${source}`);
}

function dartMap(name, messages) {
  const rows = [...messages]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([source, translation]) => `  ${JSON.stringify(source)}: ${JSON.stringify(translation)},`);
  return `const Map<String, String> ${name} = {\n${rows.join("\n")}\n};\n`;
}

writeFileSync(
  "lib/l10n/app_localizations_data.dart",
  `${dartMap("launcherKorean", ko)}\n${dartMap("launcherChinese", zh)}`,
  "utf8",
);
console.log(`Generated ${ko.size} Korean and Chinese launcher messages.`);
