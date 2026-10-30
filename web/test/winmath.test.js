import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resizeRect, fitRect, MIN_W, MIN_H, MIN_Y } from '../src/lib/winmath.js';

const VW = 1280, VH = 800;
const r = { x: 100, y: 100, w: 800, h: 500 };

test('every edge and corner resizes, the opposite edge stays put', () => {
  assert.deepEqual(resizeRect(r, 'e', 50, 0, VW, VH), { ...r, w: 850 });
  assert.deepEqual(resizeRect(r, 's', 0, 40, VW, VH), { ...r, h: 540 });
  assert.deepEqual(resizeRect(r, 'w', -60, 0, VW, VH), { x: 40, y: 100, w: 860, h: 500 });
  assert.deepEqual(resizeRect(r, 'n', 0, -30, VW, VH), { x: 100, y: 70, w: 800, h: 530 });
  assert.deepEqual(resizeRect(r, 'nw', -10, -10, VW, VH), { x: 90, y: 90, w: 810, h: 510 });
  assert.deepEqual(resizeRect(r, 'se', 10, 10, VW, VH), { ...r, w: 810, h: 510 });
  assert.deepEqual(resizeRect(r, 'ne', 10, -10, VW, VH), { x: 100, y: 90, w: 810, h: 510 });
  assert.deepEqual(resizeRect(r, 'sw', -10, 10, VW, VH), { x: 90, y: 100, w: 810, h: 510 });
});

test('a window that fills the screen still grows to the top and left', () => {
  const big = { x: 16, y: 60, w: 1248, h: 724 }; // the App Store window on a small screen
  const n = resizeRect(big, 'nw', -500, -500, VW, VH);
  assert.equal(n.x, 4);
  assert.equal(n.y, MIN_Y);
  assert.equal(n.x + n.w, big.x + big.w);
  assert.equal(n.y + n.h, big.y + big.h);
});

test('never smaller than the minimum, never past the viewport', () => {
  const s = resizeRect(r, 'se', -2000, -2000, VW, VH);
  assert.equal(s.w, MIN_W);
  assert.equal(s.h, MIN_H);
  const w = resizeRect(r, 'w', 2000, 0, VW, VH);
  assert.equal(w.w, MIN_W);
  assert.equal(w.x + w.w, r.x + r.w);
  const e = resizeRect(r, 'e', 5000, 0, VW, VH);
  assert.equal(e.x + e.w, VW - 4);
});

test('a window already past an edge does not jump when grabbed', () => {
  const off = { x: -200, y: 100, w: 800, h: 500 };
  assert.deepEqual(resizeRect(off, 'w', 0, 0, VW, VH), off);
});

test('a smaller browser window shrinks the window to fit', () => {
  const f = fitRect({ x: 300, y: 100, w: 1200, h: 900 }, 900, 600);
  assert.ok(f.w <= 900 - 16 && f.h <= 600 - MIN_Y - 8);
  assert.ok(f.x <= 900 - 120);
});
