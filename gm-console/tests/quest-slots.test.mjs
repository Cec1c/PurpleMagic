import test from 'node:test';
import assert from 'node:assert/strict';
import {questSlotSummary} from '../src/quest-slots.ts';

test('slot confirmation and receipt describe only PVF requested unlocks', () => {
  assert.equal(questSlotSummary(), '');
  assert.equal(questSlotSummary({before: 128, after: 131, requested: 3}), '辅助装备（左槽）：将开启 · 魔法石（右槽）：将开启');
  assert.equal(questSlotSummary({before: 1, after: 19, requested: 19}), '辅助装备（左槽）：已开启 · 魔法石（右槽）：将开启 · 耳环：将开启');
  assert.equal(questSlotSummary({before: 0, after: 16, requested: 16}, true), '耳环：已开启');
});
