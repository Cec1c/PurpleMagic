import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { build } from 'esbuild';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

const { outputFiles } = await build({
  entryPoints: ['src/App.tsx'], bundle: true, platform: 'node', format: 'cjs', write: false,
  packages: 'external', loader: { '.css': 'empty' },
  define: { 'import.meta.env.VITE_CONSOLE_MODE': '"demo"' },
});
const compiled = { exports: {} };
new Function('require', 'module', 'exports', outputFiles[0].text)(createRequire(import.meta.url), compiled, compiled.exports);

test('rendered sidebar keeps all nine destinations, including benefits and accounts', () => {
  globalThis.location = { hash: '#benefits', search: '?demo=1' };
  globalThis.localStorage = { getItem: () => null, setItem() {} };
  const html = renderToStaticMarkup(createElement(compiled.exports.App));
  const sidebar = html.match(/<aside\b[^>]*>([\s\S]*?)<\/aside>/)[1];
  const navigation = [...sidebar.matchAll(/<nav\b[^>]*>([\s\S]*?)<\/nav>/g)].map(m => m[1]).join('');
  const links = [...navigation.matchAll(/href="#([^"]+)"/g)].map(m => m[1]);
  assert.deepEqual(links, ['query', 'inventory', 'mail', 'quests', 'gm', 'activities', 'benefits', 'accounts', 'settings']);
  assert.match(navigation, /href="#benefits"[^>]*aria-current="page"/);
  assert.match(navigation, /福利规则/);
  assert.match(navigation, /账号与角色/);
});
