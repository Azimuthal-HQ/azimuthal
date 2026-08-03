import { describe, expect, it } from 'vitest';

/**
 * Raw HTML may only be enabled where a sanitiser is enabled with it.
 *
 * `wiki-sanitize.test.tsx` proves the sanitiser is wired at the ONE call site
 * that exists today. It cannot say anything about the second one. Somebody
 * supporting an embedded snippet adds `rehypePlugins={[rehypeRaw]}` to the
 * shared `Markdown` component — used by SharedEntityPage,
 * PortalRequestDetailPage, TicketDetailPage, ItemDetailPage and the dashboard
 * note gadget — and every gate stays green while the hole reopens on five
 * surfaces at once, including one whose entire content is written by external
 * customers.
 *
 * So the rule is mechanical rather than a convention to remember, in the same
 * shape as `no-direct-fetch.test.ts`: any module that pulls in `rehype-raw`
 * must pull in `rehype-sanitize` too. That is deliberately cruder than
 * checking the plugin ORDER — a static scan cannot know the order, and
 * pretending otherwise would be worse than admitting the limit. Order is
 * covered by `wiki-sanitize.test.tsx`, which renders the real page.
 *
 * Failing this test is not a licence to add a second call site with a
 * sanitiser beside it. It is a prompt to ask whether that surface needs markup
 * at all — see docs/design/shared-surfaces.md §19, where the answer for every
 * surface but Codex is no.
 */

const sources = import.meta.glob('../**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

const RAW = /from\s+['"]rehype-raw['"]/;
const SANITIZE = /from\s+['"]rehype-sanitize['"]/;

function isProductionSource(path: string): boolean {
  if (path.endsWith('.d.ts')) return false;
  if (/\.(test|spec)\.tsx?$/.test(path)) return false;
  if (path.startsWith('../test/')) return false;
  return true;
}

describe('raw HTML never renders unsanitised', () => {
  it('every module importing rehype-raw also imports rehype-sanitize', () => {
    const unsanitised = Object.entries(sources)
      .filter(([path]) => isProductionSource(path))
      .filter(([, content]) => RAW.test(content) && !SANITIZE.test(content))
      .map(([path]) => path);

    expect(
      unsanitised,
      'rehype-raw turns a page body into markup; without rehype-sanitize behind it, ' +
        'anyone who can write that body can run script in every reader\'s session',
    ).toEqual([]);
  });

  it('finds the call site it is meant to be guarding', () => {
    // Without this, the test above passes vacuously the day the glob pattern,
    // the import spelling or the file layout changes — reporting "no
    // unsanitised call sites" because it can no longer see any call sites.
    const withRaw = Object.entries(sources)
      .filter(([path]) => isProductionSource(path))
      .filter(([, content]) => RAW.test(content))
      .map(([path]) => path);

    expect(withRaw).toContain('../pages/codex/WikiPage.tsx');
  });
});
