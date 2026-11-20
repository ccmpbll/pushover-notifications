// A select cannot hold an empty string as an option's value: an empty value
// reads as "nothing chosen", so the trigger shows no label and choosing the
// option may not report a change. A caller's "no sound" and "no camera" are the
// empty string, so the select is given a stand-in for it and the caller is given
// the empty string back.

/** What the select holds for an option whose value is the empty string. */
export const EMPTY = '__empty__';

/** A caller's value as the select holds it. */
export const toOption = (value: string): string =>
  value === '' ? EMPTY : value;

/** What the select reports, as the caller's value. */
export const fromOption = (value: string): string =>
  value === EMPTY ? '' : value;
