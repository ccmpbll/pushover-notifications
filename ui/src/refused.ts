/**
 * Whether Connect refused a request because of who is signed in. It is a
 * refusal of the person, not a fault: the server answers 403, with the code
 * core.auth/ACCESS_DENIED, before this module's own code runs. It is read off
 * the error's fields, and never off its wording.
 */
export const isRefused = (error: unknown): boolean => {
  if (typeof error !== 'object' || error === null) return false;
  const { status, code } = error as { status?: unknown; code?: unknown };

  return status === 403 || code === 'core.auth/ACCESS_DENIED';
};
