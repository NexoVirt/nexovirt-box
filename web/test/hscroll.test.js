import { test } from 'node:test';
import assert from 'node:assert/strict';
import { wheelShift, overflowSides } from '../src/lib/hscroll.js';

const strip = (scrollLeft) => ({ scrollLeft, scrollWidth: 1000, clientWidth: 400 });

test('a vertical wheel scrolls the strip sideways', () => {
  assert.equal(wheelShift({ deltaX: 0, deltaY: 100, deltaMode: 0 }, strip(0)), 100);
  assert.equal(wheelShift({ deltaX: 0, deltaY: -100, deltaMode: 0 }, strip(300)), -100);
  assert.equal(wheelShift({ deltaX: 0, deltaY: 3, deltaMode: 1 }, strip(0)), 48); // lines (Firefox)
  assert.equal(wheelShift({ deltaX: 0, deltaY: 1, deltaMode: 2 }, strip(0)), 400); // pages
});

test('the page keeps the wheel when there is nothing to scroll or the strip is at its end', () => {
  assert.equal(wheelShift({ deltaX: 0, deltaY: 100, deltaMode: 0 }, { scrollLeft: 0, scrollWidth: 400, clientWidth: 400 }), 0);
  assert.equal(wheelShift({ deltaX: 0, deltaY: -100, deltaMode: 0 }, strip(0)), 0);
  assert.equal(wheelShift({ deltaX: 0, deltaY: 100, deltaMode: 0 }, strip(600)), 0);
});

test('sideways gestures (trackpad, tilt wheel) stay native', () => {
  assert.equal(wheelShift({ deltaX: 40, deltaY: 10, deltaMode: 0 }, strip(100)), 0);
});

test('overflow sides', () => {
  assert.deepEqual(overflowSides(strip(0)), { any: true, start: false, end: true });
  assert.deepEqual(overflowSides(strip(600)), { any: true, start: true, end: false });
  assert.deepEqual(overflowSides({ scrollLeft: 0, scrollWidth: 400, clientWidth: 400 }), { any: false, start: false, end: false });
});
