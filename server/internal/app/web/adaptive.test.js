'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { AdaptiveTierController } = require('./adaptive.js');

const profiles = [
  { minBandwidth: 7_000_000 },
  { minBandwidth: 4_000_000 },
  { minBandwidth: 0 },
];

test('requires five consecutive bad samples before degrading', () => {
  const controller = new AdaptiveTierController(profiles);
  for (let second = 0; second < 4; second++) assert.equal(controller.sample(4_000_000, .06), null);
  assert.equal(controller.sample(4_000_000, .06), 1);
});

test('a good sample resets the degradation window', () => {
  const controller = new AdaptiveTierController(profiles);
  for (let second = 0; second < 4; second++) controller.sample(4_000_000, .06);
  assert.equal(controller.sample(10_000_000, .01), null);
  assert.equal(controller.sample(4_000_000, .06), null);
});

test('requires twenty consecutive healthy samples before upgrading', () => {
  const controller = new AdaptiveTierController(profiles, 1);
  for (let second = 0; second < 19; second++) assert.equal(controller.sample(10_000_000, .01), null);
  assert.equal(controller.sample(10_000_000, .01), 0);
});
