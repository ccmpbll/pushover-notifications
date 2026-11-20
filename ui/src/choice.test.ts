import { describe, expect, it } from 'vitest';

import { EMPTY, fromOption, toOption } from './choice';
import { SOUNDS } from './sounds';

describe('an option with an empty value', () => {
  it('is given a value the select can hold, and gets its own back', () => {
    expect(toOption('')).toBe(EMPTY);
    expect(fromOption(EMPTY)).toBe('');
    expect(toOption('magic')).toBe('magic');
    expect(fromOption('magic')).toBe('magic');
  });

  it('is never the empty string by the time it reaches the select', () => {
    for (const [value] of SOUNDS) {
      expect(toOption(value)).not.toBe('');
    }
    expect(new Set(SOUNDS.map(([v]) => toOption(v))).size).toBe(SOUNDS.length);
  });
});
