import { describe, expect, it } from 'vitest';

import { isRefused } from './refused';

describe('a refusal of the person', () => {
  it('is a 403, or the access denied code', () => {
    expect(isRefused({ status: 403 })).toBe(true);
    expect(isRefused({ status: 200, code: 'core.auth/ACCESS_DENIED' })).toBe(
      true,
    );
  });

  it('is not any other failure', () => {
    expect(isRefused({ status: 502, code: 'x.y/SEND_FAILED' })).toBe(false);
    expect(isRefused({ status: 409 })).toBe(false);
    expect(isRefused(new Error('forbidden'))).toBe(false);
    expect(isRefused(undefined)).toBe(false);
    expect(isRefused('403')).toBe(false);
  });
});
