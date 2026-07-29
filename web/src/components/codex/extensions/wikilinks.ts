/**
 * `[[wikilinks]]`, `[[target|display]]` aliases, and `![[transclusion]]`.
 *
 * ## No new node or mark type
 *
 * A resolved wikilink is the existing `link` mark carrying `page_id` — the same
 * thing the toolbar's internal-link button has always produced. A transclusion
 * is the existing `pageInclude` macro. Only the UNRESOLVED state needed
 * vocabulary, and it is one attribute on the link mark rather than a type of its
 * own (`target_title`; see `LINK_ATTRS` in `lib/codex/schema.ts`). Everything
 * here is therefore an input affordance over machinery that already shipped.
 *
 * ## Two mechanisms, and why both
 *
 * The `[[` **autocomplete** is the affordance: it opens on `[[`, offers the
 * pages this reader can see, and completes the whole reference on selection.
 *
 * The **input rules** are the guarantee. They fire on the closing `]]` and
 * handle every case the popup does not: an author who types the whole
 * reference from memory, one who dismissed the popup with Escape, one pasting
 * plain text containing a wikilink, and — the case that matters — a target
 * that names no existing page at all. Without them, dismissing the popup would
 * leave literal `[[Runbook]]` text in the document.
 *
 * ## What happens when nothing matches
 *
 * An unresolved link, not a refusal and not a guess. `[[Runbook]]` with no page
 * called Runbook stores a link mark carrying `target_title: "Runbook"` and no
 * `page_id`; it renders visibly distinct, and clicking it offers to create the
 * page. That is the whole point of the feature — writing the link first and
 * making the page later is how a wiki grows.
 *
 * `![[Runbook]]` with no match degrades to the same unresolved LINK rather than
 * to an unresolved embed. There is deliberately no unresolved-embed state: an
 * embed renders another page's body, and a placeholder claiming to be one would
 * be a hole in the page rather than a promise. The UI says so when it happens,
 * through `onNotice`, rather than silently substituting something else.
 */
import { Extension, InputRule } from '@tiptap/core';
import type { ChainedCommands } from '@tiptap/core';
import Suggestion from '@tiptap/suggestion';
import type { SuggestionOptions } from '@tiptap/suggestion';
import { PluginKey } from '@tiptap/pm/state';

import type { WikiPage } from '../../../lib/api';
import { LINK_ATTRS, MARK_LINK } from '../../../lib/codex/schema';
import { filterPages, findPageByTitle } from '../pageSearch';

/** The suggestion plugin's key, exported so tests can address the plugin. */
export const wikilinkSuggestionKey = new PluginKey('codexWikilinkSuggestion');

/** What the editor needs to render the `[[` popup. */
export interface WikilinkSuggestionState {
  /** The candidates, already filtered and capped. */
  items: WikiPage[];
  /** What the author has typed since `[[`. */
  query: string;
  /** Which candidate is highlighted. Owned by the plugin, which handles keys. */
  activeIndex: number;
  /** Where to put the popup, in viewport coordinates. */
  rect: DOMRect | null;
  /** Complete the reference with this page, or with null to leave it unresolved. */
  onChoose: (page: WikiPage | null) => void;
}

export interface WikilinkOptions {
  /** The space's pages, read fresh on every keystroke rather than captured. */
  getPages: () => WikiPage[];
  /** The page being edited, which cannot link to itself. */
  getCurrentPageId: () => string;
  /** Pushes the popup's state out to the editor component, or null to close. */
  onSuggestionChange: (state: WikilinkSuggestionState | null) => void;
  /** Reports something the author should be told, such as a degraded embed. */
  onNotice: (message: string) => void;
}

/**
 * The two halves of a wikilink target.
 *
 * `[[target|display]]` links to `target` and reads as `display`. The pipe is an
 * alias, which means the display text is the AUTHOR'S WORDS: renaming the target
 * page later deliberately does not rewrite it (the hover tooltip shows the
 * page's current title instead, so the drift is discoverable without being
 * corrected behind the author's back).
 */
export interface ParsedWikilink {
  target: string;
  display: string;
}

/**
 * Splits a wikilink's inner text, degrading both halves of the pipe mistake.
 *
 * `[[x|]]` and `[[|x]]` are both typos with an obvious intention — the author
 * named one page — so both become `[[x]]`. `[[|]]` and `[[   ]]` name nothing at
 * all and produce no link; the literal text stays, because inventing a link to
 * nowhere is worse than leaving what was typed.
 *
 * Only the FIRST pipe separates. A display half may contain pipes; a target,
 * being a page title, is the half that must not be ambiguous.
 */
export function parseWikilink(inner: string): ParsedWikilink | null {
  const pipe = inner.indexOf('|');
  const target = (pipe === -1 ? inner : inner.slice(0, pipe)).trim();
  const display = (pipe === -1 ? '' : inner.slice(pipe + 1)).trim();

  if (target && display) return { target, display };
  if (target) return { target, display: target };
  if (display) return { target: display, display };
  return null;
}

/**
 * The link mark's attributes for a reference.
 *
 * `href` stays null in both states. A page's URL depends on the space it is
 * being read in, so a document must not bake one in — the reading surface turns
 * `page_id` into a route. An unresolved link has no destination at all yet.
 */
export function wikilinkAttrs(page: WikiPage | null, target: string) {
  return page
    ? { [LINK_ATTRS.href]: null, [LINK_ATTRS.pageId]: page.id, [LINK_ATTRS.targetTitle]: null }
    : { [LINK_ATTRS.href]: null, [LINK_ATTRS.pageId]: null, [LINK_ATTRS.targetTitle]: target };
}

// `![[Title]]`, fired by the closing bracket. The inner text excludes brackets
// and pipes: a transclusion has no display half to alias, because it renders
// the other page's body rather than any text of its own.
const EMBED_RULE = /!\[\[([^[\]|]+)\]\]$/;

// `[[Target]]` and `[[Target|Display]]`. The negative lookbehind is what keeps
// this from also firing on the embed form above.
const LINK_RULE = /(?<!!)\[\[([^[\]]+)\]\]$/;

/**
 * Every option has an inert default, so the extension can be registered
 * unconditionally.
 *
 * That matters for two callers. The READING surface builds the same extension
 * list as the editor, deliberately — a reader must see exactly what the author
 * saw — and it has no popup to drive. And the schema drift guard builds the list
 * with no arguments at all; it compares registered TYPES, and this extension
 * registers none, so it must not be the reason the two lists differ.
 */
export function wikilinkExtension(options: Partial<WikilinkOptions> = {}): Extension {
  const resolved: WikilinkOptions = {
    getPages: options.getPages ?? (() => []),
    getCurrentPageId: options.getCurrentPageId ?? (() => ''),
    onSuggestionChange: options.onSuggestionChange ?? (() => {}),
    onNotice: options.onNotice ?? (() => {}),
  };
  return buildWikilinkExtension(resolved);
}

function buildWikilinkExtension(options: WikilinkOptions): Extension {
  const resolve = (title: string) =>
    findPageByTitle(options.getPages(), title, options.getCurrentPageId());

  return Extension.create({
    name: 'codexWikilinks',

    addInputRules() {
      return [
        new InputRule({
          find: EMBED_RULE,
          handler: ({ range, match, chain }) => {
            const title = match[1].trim();
            const page = resolve(title);
            if (page) {
              chain()
                .insertContentAt(
                  { from: range.from, to: range.to },
                  { type: 'pageInclude', attrs: { page_id: page.id } },
                )
                .run();
              return;
            }
            // No page to embed. A link the author can click to create one is
            // the honest substitute, and they are told that is what happened
            // rather than left to notice the shape changed.
            insertReference(chain(), range, null, title, title);
            options.onNotice(
              `There is no page called “${title}” to embed yet, so a link was inserted instead. ` +
                'Click it to create the page, then replace the link with an embed.',
            );
          },
        }),

        new InputRule({
          find: LINK_RULE,
          handler: ({ range, match, chain }) => {
            const parsed = parseWikilink(match[1]);
            if (!parsed) return;
            insertReference(chain(), range, resolve(parsed.target), parsed.target, parsed.display);
          },
        }),
      ];
    },

    addProseMirrorPlugins() {
      return [
        Suggestion({
          editor: this.editor,
          pluginKey: wikilinkSuggestionKey,
          char: '[[',
          // A page title is words with spaces in it. Without this the popup
          // would close on the first space and only ever offer single-word
          // pages, which is most of a wiki's titles missing.
          allowSpaces: true,
          startOfLine: false,
          items: ({ query }) =>
            filterPages(options.getPages(), query, options.getCurrentPageId()),
          render: () => makeSuggestionRenderer(options),
          command: ({ editor, range, props }) => {
            const page = props as unknown as WikiPage | null;
            const target = page?.title ?? '';
            insertReference(editor.chain(), range, page, target, target);
          },
        } as SuggestionOptions),
      ];
    },
  });
}

/**
 * Replaces the typed reference with a link, keeping the author's display text.
 *
 * The trailing space is there so the cursor lands outside the mark. Without it,
 * the next character typed inherits the link — which is how a whole sentence
 * ends up underlined and pointing at a page.
 */
function insertReference(
  chain: ChainedCommands,
  range: { from: number; to: number },
  page: WikiPage | null,
  target: string,
  display: string,
): void {
  chain
    .insertContentAt({ from: range.from, to: range.to }, [
      {
        type: 'text',
        text: display,
        marks: [{ type: MARK_LINK, attrs: wikilinkAttrs(page, target) }],
      },
      { type: 'text', text: ' ' },
    ])
    .run();
}

/**
 * The suggestion popup's lifecycle, pushed out to React.
 *
 * The plugin keeps `activeIndex` rather than the component, because arrow keys
 * have to be intercepted before ProseMirror moves the cursor — which happens in
 * `onKeyDown` here, inside the plugin. Two owners of that index would let the
 * highlighted row and the row that Enter selects disagree.
 */
function makeSuggestionRenderer(options: WikilinkOptions) {
  let items: WikiPage[] = [];
  let activeIndex = 0;
  let command: (page: WikiPage | null) => void = () => {};

  const push = (query: string, rect: DOMRect | null) => {
    options.onSuggestionChange({
      items,
      query,
      activeIndex,
      rect,
      onChoose: (page) => command(page),
    });
  };

  /**
   * The caret's position, or null.
   *
   * `clientRect` asks the view for `coordsAtPos`, and TipTap throws rather than
   * returning nothing when there is no view — which happens both before the
   * editor mounts and while it is being torn down. Neither is a failure worth
   * propagating: a popup with no position is a popup that has not been placed
   * yet, and throwing from a suggestion callback surfaces as an unhandled
   * rejection with nothing to catch it.
   */
  const rectOf = (props: SuggestionRenderProps): DOMRect | null => {
    try {
      return props.clientRect?.() ?? null;
    } catch {
      return null;
    }
  };

  return {
    onStart: (props: SuggestionRenderProps) => {
      items = props.items as WikiPage[];
      activeIndex = 0;
      command = (page) => props.command(page as never);
      push(props.query, rectOf(props));
    },
    onUpdate: (props: SuggestionRenderProps) => {
      items = props.items as WikiPage[];
      // Clamp rather than reset: the author narrowing a query should not lose
      // their place every keystroke, but an index past the end would select
      // nothing on Enter.
      activeIndex = Math.min(activeIndex, Math.max(items.length - 1, 0));
      command = (page) => props.command(page as never);
      push(props.query, rectOf(props));
    },
    onKeyDown: (props: { event: KeyboardEvent }) => {
      if (props.event.key === 'ArrowDown') {
        activeIndex = items.length === 0 ? 0 : (activeIndex + 1) % items.length;
        options.onSuggestionChange(null);
        return true;
      }
      if (props.event.key === 'ArrowUp') {
        activeIndex = items.length === 0 ? 0 : (activeIndex - 1 + items.length) % items.length;
        options.onSuggestionChange(null);
        return true;
      }
      if (props.event.key === 'Enter') {
        // Enter with nothing highlighted leaves the reference unresolved rather
        // than doing nothing — the author typed a name, and a page by that name
        // is exactly what they can now create.
        command(items[activeIndex] ?? null);
        return true;
      }
      if (props.event.key === 'Escape') {
        options.onSuggestionChange(null);
        return true;
      }
      return false;
    },
    onExit: () => {
      options.onSuggestionChange(null);
    },
  };
}

/** The subset of `@tiptap/suggestion`'s render props this renderer reads. */
interface SuggestionRenderProps {
  items: unknown[];
  query: string;
  command: (props: never) => void;
  clientRect?: (() => DOMRect | null) | null;
}
