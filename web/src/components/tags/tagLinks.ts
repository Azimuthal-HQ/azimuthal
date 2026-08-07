/**
 * Where a tag chip goes when it is clicked.
 *
 * ## Why the client does not slugify
 *
 * A tag's identity is its slug, derived server-side by `itemtypes.Slugify` —
 * the repository's one slug helper, with a database CHECK constraint written
 * against its exact output. Reimplementing it in TypeScript would be a second
 * implementation of a rule that has to hold identically in two languages, with
 * no compiler and no test between them; the schema manifest exists because that
 * class of drift is silent.
 *
 * So the path carries the LABEL, and the server slugifies whatever it is given.
 * `Slugify` is idempotent, so a real slug passes through unchanged and both
 * forms work — which also means an old link keeps working if the label a chip
 * was rendered from is not the tag's current display name.
 *
 * ## Why the route is under a module and a space
 *
 * A tag is org-scoped and its results span spaces and modules, so the module
 * and space in the path describe the reader's context rather than the query's
 * scope. They are there because the browse renders inside `SpaceLayout`, which
 * is what keeps the sidebar and the module nav on screen — landing on a bare
 * org-level route would drop the reader out of the space they were reading and
 * give them nothing to go back to. The results themselves are filtered to
 * every space the reader can see, not to this one, and cover all three entity
 * kinds whatever module the reader came from.
 */

/** The route a tag chip links to. */
export function tagBrowsePath(module: string, spaceId: string, label: string): string {
  return `/${module}/${spaceId}/tags/${encodeURIComponent(label)}`;
}
