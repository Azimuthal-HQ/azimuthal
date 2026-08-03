import { type ClassValue, clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Formats an RFC3339 timestamp as its UTC calendar date (YYYY-MM-DD).
 *
 * Calendar-date fields (sprint starts_at/ends_at, item and ticket due_at) are
 * stored as UTC midnight, but the server may serialize them with a non-UTC
 * offset — slicing the raw string would then show the previous day.
 *
 * This is also the right way to seed an `<input type="date">` from a stored
 * value, for the same reason.
 */
export function formatUTCDate(iso: string): string {
  return new Date(iso).toISOString().slice(0, 10);
}

/**
 * The inverse of {@link formatUTCDate}: turns the `YYYY-MM-DD` an
 * `<input type="date">` produces into the RFC3339 timestamp the API decodes.
 * A bare `YYYY-MM-DD` is rejected with 400.
 *
 * Returns `undefined` for an empty input, which `JSON.stringify` then drops
 * from the request body. That is right for a create form — an untouched date
 * field should not reach the wire at all — but it is the WRONG sentinel for
 * clearing a stored value through a PATCH, where an absent key means "leave it
 * alone" and only an explicit `null` clears. A caller that must be able to
 * clear writes `date ? toRFC3339Date(date) : null` rather than leaning on this
 * return value.
 */
export function toRFC3339Date(date: string): string | undefined {
  return date ? `${date}T00:00:00Z` : undefined;
}
