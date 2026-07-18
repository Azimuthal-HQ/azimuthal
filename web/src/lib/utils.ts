import { type ClassValue, clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Formats an RFC3339 timestamp as its UTC calendar date (YYYY-MM-DD).
 *
 * Calendar-date fields (sprint starts_at/ends_at) are stored as UTC midnight,
 * but the server may serialize them with a non-UTC offset — slicing the raw
 * string would then show the previous day.
 */
export function formatUTCDate(iso: string): string {
  return new Date(iso).toISOString().slice(0, 10);
}
