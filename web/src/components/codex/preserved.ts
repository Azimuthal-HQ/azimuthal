/**
 * Naming for preserved content.
 *
 * ADR-0012 requires a preserved block to say what it was and where it came
 * from. `az_name` carries the original node or mark type verbatim, which is
 * the honest answer but not always a legible one — the markdown converter's
 * own catch-alls are called `legacyBlock` and `legacyInline`, and a reader
 * shown that string learns nothing.
 *
 * So the type name is translated where a translation exists and passed through
 * where none does. It is never replaced by a generic word: "something was
 * preserved here" without saying what is precisely the vagueness ADR-0012
 * exists to prevent.
 */

/** The `az_source` value the document model itself produces. */
const SOURCE_DOCUMENT = 'document';

/**
 * The internal type names `wiki/doc`'s markdown converter emits for source it
 * could not map onto the schema (`internal/core/wiki/doc/markdown.go`).
 */
const LEGACY_TYPE_LABELS: Record<string, string> = {
  legacyBlock: 'Original markdown block',
  legacyInline: 'Original markdown formatting',
};

/** A human label for a preserved item's original type. */
export function preservedTypeLabel(name: string | null | undefined): string {
  if (!name) return 'Unrecognised content';
  return LEGACY_TYPE_LABELS[name] ?? name;
}

/**
 * A human label for where preserved content came from.
 *
 * `document` means it was already inside a stored Codex document — it reached
 * this page from somewhere this deployment no longer knows about, typically an
 * import. Any other value is an importer's own label and is shown as given.
 */
export function preservedSourceLabel(source: string | null | undefined): string {
  if (!source) return 'unknown source';
  return source === SOURCE_DOCUMENT ? 'this page' : source;
}

/** The one-line summary shown for a preserved item, e.g. in a dialog list. */
export function preservedSummary(name: string | null | undefined, text: string | null | undefined): string {
  const label = preservedTypeLabel(name);
  const body = (text ?? '').trim();
  if (!body) return label;
  const trimmed = body.length > 80 ? `${body.slice(0, 80)}…` : body;
  return `${label} — ${trimmed}`;
}
