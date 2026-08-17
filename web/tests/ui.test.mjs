import { readdir, readFile } from 'node:fs/promises';
import { test } from 'node:test';
import assert from 'node:assert/strict';

const dist = new URL('../../internal/server/ui/dist/', import.meta.url);
const html = await readFile(new URL('index.html', dist), 'utf8');
const assets = await Promise.all((await readdir(new URL('assets/', dist))).map(name => readFile(new URL(`assets/${name}`, dist), 'utf8')));
const output = [html, ...assets].join('\n');

test('built workspace exposes the branded incident review landmarks', () => {
  assert.match(output, /data-testid="incident-summary"/);
  assert.match(output, /data-testid="evidence-timeline"/);
  assert.match(output, /aria-label="Rewind home"/);
  assert.doesNotMatch(output, /kindGlyph/);
});
