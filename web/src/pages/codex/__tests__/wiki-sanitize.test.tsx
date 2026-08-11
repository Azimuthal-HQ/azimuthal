import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { WikiPage as WikiPageType } from '../../../lib/api';
import { WikiPage } from '../WikiPage';

/**
 * The legacy markdown reading path renders raw HTML on purpose (`rehype-raw`),
 * and a Codex page body is untrusted markup: anyone who can write a page in a
 * space can put whatever they like in it, and every reader of that space
 * renders it. This file is the proof that `rehype-sanitize` runs behind
 * `rehype-raw` in `WikiPage`.
 *
 * It renders the real page component, not a helper, so it cannot pass by
 * exercising a chain the page has stopped using.
 *
 * WHICH ASSERTIONS ACTUALLY DISCRIMINATE — measured, not assumed, by deleting
 * `rehypeSanitize` from `WIKI_REHYPE_PLUGINS` in `WikiPage` and re-running:
 *
 *   FAILS without the sanitiser (these are the proof)
 *     - `<script>` reaches the DOM
 *     - `<iframe>`, `<object>` and `<style>` reach the DOM
 *     - React logs a listener-type error, because `onerror="…"` reached it
 *
 *   STILL PASSES without the sanitiser (kept, but they are not evidence)
 *     - the rendered `<img>` carries no `onerror` ATTRIBUTE. React maps
 *       `onerror` to its own `onError` prop and refuses to attach a string as
 *       a listener, so the attribute never lands in the DOM either way. The
 *       handler cannot fire — but the sanitiser is not what stopped it, and a
 *       reader must not mistake this line for the one that guards the plugin.
 *       The console assertion beside it is the discriminating half.
 *     - no `javascript:` href survives. react-markdown v10's own
 *       `defaultUrlTransform` already drops that scheme.
 *
 *   ASSERTS THE OPPOSITE DIRECTION (must keep passing either way)
 *     - the benign-markup and code-fence cases. A sanitiser that stripped
 *       everything would satisfy every negative assertion above while
 *       destroying the feature `rehype-raw` is here to provide. These query
 *       the DOM by shape rather than by accessible role on purpose: an
 *       unsanitised `<style>body{display:none}</style>` hides the whole body
 *       from role queries, which would make them fail for a reason that has
 *       nothing to do with what they are checking.
 *
 * One assertion was written and then deleted rather than left in: a check that
 * `window.__xss` stayed undefined. Under mutation the injected `<script>` lands
 * in the DOM but jsdom does not run it, so that assertion passed with the
 * sanitiser gone. It read as an execution check and was not one.
 */

const HOSTILE = [
  '# Runbook',
  '',
  '<script>window.__xss = "executed";</script>',
  '',
  '<img src="x" onerror="window.__xss = \'executed\'" alt="broken">',
  '',
  '<a href="javascript:window.__xss=1">click me</a>',
  '',
  '<iframe src="https://evil.example.com/steal"></iframe>',
  '',
  '<object data="https://evil.example.com/steal"></object>',
  '',
  // Present because the assertion below names it. Without it in the fixture,
  // `querySelector('embed')` returns null whatever the schema says, and
  // widening `tagNames` to permit <embed> would not fail a single test.
  '<embed src="https://evil.example.com/steal">',
  '',
  '<style>body { display: none }</style>',
  '',
  // The benign half. Raw HTML is a feature of this surface; a sanitiser that
  // ate these would be a regression dressed up as a fix.
  '<p>Escalate to <b>the on-call</b> first.</p>',
  '',
  '<a href="https://runbooks.example.com/oncall">the rota</a>',
  '',
  '```js',
  'const answer = 42;',
  '```',
].join('\n');

const HOSTILE_PAGE: WikiPageType = {
  id: 'p1',
  space_id: 'space-1',
  title: 'Runbook',
  content: HOSTILE,
  doc: null,
  version: 1,
  parent_id: null,
  author_id: 'u1',
  path: 'runbook',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
};

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useWikiPages: () => ({ data: [HOSTILE_PAGE], isLoading: false, error: null }),
    useWikiPage: () => ({ data: HOSTILE_PAGE }),
    useWikiRevisions: () => ({ data: [], isLoading: false }),
    useEntityTags: () => ({ data: [], isLoading: false, error: null }),
    usePageDocument: () => ({ data: undefined, isLoading: false, error: null, refetch: vi.fn() }),
    useSpaceDrafts: () => ({ data: [] }),
    useMe: () => ({ data: { id: 'u1', org_id: 'org-1', display_name: 'T' } }),
    useComments: () => ({ data: [], refetch: vi.fn() }),
    useCreateComment: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useEffectiveAccess: () => ({ data: { org_admin: false, role: 'member' } }),
    useSpacePageShares: () => ({ data: [] }),
    // C4 mounts RelationsSection on the page read surface, so WikiPage now
    // reaches these relation hooks and the page picker's suggest. Stubbed to
    // empty — this is a sanitiser test, not a relations one. (RELATION_KINDS
    // comes through from `...actual`, so the kind select still renders.)
    useRelations: () => ({ data: [] }),
    useCreateRelation: () => ({ mutate: vi.fn(), mutateAsync: vi.fn() }),
    useDeleteRelation: () => ({ mutate: vi.fn() }),
    useItemSearch: () => ({ data: [] }),
    usePageSuggestions: () => ({ data: [], isLoading: false }),
  };
});

vi.mock('../../../components/codex/PageEditor', () => ({
  PageEditor: () => <div data-testid="codex-page-editor-stub" />,
}));

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/codex/space-1/pages/p1']}>
      <Routes>
        <Route path="/codex/:spaceId/pages/:pageId" element={<WikiPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('the wiki reading surface sanitises raw HTML', () => {
  // Captured rather than read back off the spy: `vi.spyOn(console, 'error')`
  // does not carry its argument types through a `let` annotation, and an
  // `any`-typed `mock.calls` fails the type-check gate.
  const consoleErrors: unknown[][] = [];

  beforeEach(() => {
    consoleErrors.length = 0;
    vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
      consoleErrors.push(args);
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('drops <script> entirely', async () => {
    const { container } = renderPage();
    await screen.findByTestId('wiki-page-title');

    expect(container.querySelector('script')).toBeNull();
  });

  it('drops <iframe>, <object>, <embed> and <style>', async () => {
    const { container } = renderPage();
    await screen.findByTestId('wiki-page-title');

    expect(container.querySelector('iframe')).toBeNull();
    expect(container.querySelector('object')).toBeNull();
    expect(container.querySelector('embed')).toBeNull();
    expect(container.querySelector('style')).toBeNull();
  });

  it('takes the CSS with the <style> element rather than unwrapping it', async () => {
    // The schema's one deviation from the library default, and the reason for
    // it. `strip` removes an element WITH its subtree; anything else outside
    // `tagNames` is unwrapped, children and all. By default only `<script>` is
    // stripped, so a `<style>` block was rendering its own stylesheet into the
    // page as visible body text — safe, since the CSS is never applied, but
    // not what anybody wants to read. Adding `style` to `strip` is a
    // tightening; if it is ever reverted, this fails.
    const { container } = renderPage();
    await screen.findByTestId('wiki-page-title');

    expect(container.textContent).not.toContain('display: none');
  });

  it('strips event-handler attributes before React ever sees them', async () => {
    const { container } = renderPage();
    await screen.findByTestId('wiki-page-title');

    // The discriminating half. An `onerror="…"` that survives sanitisation
    // reaches React as a string-valued `onError` prop, and React complains:
    // "Expected `onError` listener to be a function, instead got a value of
    // `string` type." Silence here means the attribute was gone before the
    // hast reached React at all. (If React ever reworded that message this
    // stops discriminating rather than failing — the DOM checks below still
    // pin the property.)
    const listenerComplaints = consoleErrors.filter((call) =>
      call.some((arg) => typeof arg === 'string' && /listener/i.test(arg)),
    );
    expect(listenerComplaints, 'a handler attribute reached React').toEqual([]);

    const img = container.querySelector('img[alt="broken"]');
    expect(img, 'the <img> itself is allowed markup and must survive').not.toBeNull();
    expect(img).not.toHaveAttribute('onerror');
    expect(container.querySelector('[onerror], [onclick], [onload]')).toBeNull();
  });

  it('does not emit a javascript: URL', async () => {
    const { container } = renderPage();
    await screen.findByTestId('wiki-page-title');

    const hrefs = [...container.querySelectorAll('a')].map((a) => a.getAttribute('href') ?? '');
    expect(hrefs.some((h) => h.toLowerCase().startsWith('javascript:'))).toBe(false);
  });

  it('keeps the benign markup raw HTML is enabled for', async () => {
    const { container } = renderPage();
    await screen.findByTestId('wiki-page-title');

    // An inline element from raw HTML, still an element and not escaped text.
    const bold = container.querySelector('b');
    expect(bold).not.toBeNull();
    expect(bold).toHaveTextContent('the on-call');

    // A real link keeps its href.
    const rota = container.querySelector('a[href="https://runbooks.example.com/oncall"]');
    expect(rota).not.toBeNull();
    expect(rota).toHaveTextContent('the rota');
  });

  it('leaves the syntax-highlighted code block intact', async () => {
    // The `code` component override picks a highlighter from
    // `className="language-js"`. The default schema allows that className by a
    // /^language-./ pattern; a schema narrowed the wrong way would silently
    // downgrade every fenced block to plain text.
    const { container } = renderPage();
    await screen.findByTestId('wiki-page-title');

    expect(container.textContent).toContain('const answer = 42;');
  });
});
