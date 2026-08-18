import { readdir, readFile } from 'node:fs/promises';
import { test } from 'node:test';
import assert from 'node:assert/strict';

const dist = new URL('../../internal/server/ui/dist/', import.meta.url);
const html = await readFile(new URL('index.html', dist), 'utf8');
const assets = await Promise.all((await readdir(new URL('assets/', dist))).map(name => readFile(new URL(`assets/${name}`, dist), 'utf8')));
const source = await Promise.all([
  readFile(new URL('../src/main.ts', import.meta.url), 'utf8'),
  readFile(new URL('../src/styles/tokens.css', import.meta.url), 'utf8'),
]);
const output = [html, ...assets, ...source].join('\n');

test('built workspace exposes the branded incident review landmarks', () => {
  assert.match(output, /data-testid="incident-summary"/);
  assert.match(output, /data-testid="evidence-timeline"/);
  assert.match(output, /aria-label="Rewind home"/);
  assert.match(output, /theme-toggle/);
  assert.match(output, /rewind\.theme/);
  assert.match(output, /data-theme/);
  assert.doesNotMatch(output, /kindGlyph/);
});
