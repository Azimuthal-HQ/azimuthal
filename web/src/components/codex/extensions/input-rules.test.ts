import { Editor } from '@tiptap/core';
import { describe, expect, it } from 'vitest';

import type { CodexDoc, CodexNode } from '../../../lib/codex/schema';
import { LINK_ATTRS, NODE_INLINE_TAG, TAG_ATTRS } from '../../../lib/codex/schema';
import { codexExtensions } from './index';
import { parseWikilink, wikilinkAttrs } from './wikilinks';
import { isTagLabel, TAG_INPUT_REGEX } from './tags';

/**
 * The type-time rules, driven through a real ProseMirror schema.
 *
 * These are the first custom input rules in this editor, and two of them share
 * a trigger character with something that already worked. Asserting a regex in
 * isolation would prove nothing about that: what matters is which rule wins for
 * a given keystroke, and the only place that is decided is inside a running
 * editor with every rule registered at once.
 *
 * `typeText` routes each character through `handleTextInput`, which is exactly
 * how ProseMirror delivers typing to the input-rule plugin. Typing is what
 * these rules respond to, so anything that inserted the text some other way
 * would test a path no author takes.
 */

const PAGES = [
  page('page-runbook', 'Runbook'),
  page('page-rota', 'On-call rota'),
];

function page(id: string, title: string) {
  return {
    id,
    space_id: 'space-1',
    title,
    content: '',
    doc: null,
    version: 1,
    parent_id: null,
    author_id: 'u1',
    path: id,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  };
}

function makeEditor(): Editor {
  return new Editor({
    extensions: codexExtensions({
      wikilinks: {
        getPages: () => PAGES,
        getCurrentPageId: () => 'page-current',
      },
    }),
    content: { type: 'doc', content: [{ type: 'paragraph' }] },
  });
}

/**
 * Types text one character at a time, the way a keyboard does.
 *
 * `someProp('handleTextInput', …)` is the hook the input-rule plugin registers
 * on, so a character that no rule claims falls through to an ordinary insert —
 * which is what the `if (!handled)` branch is. Inserting the whole string in
 * one transaction instead would fire no rule at all and every test here would
 * pass against an editor with no rules registered.
 */
function typeText(editor: Editor, text: string): void {
  for (const ch of text) {
    const { view } = editor;
    const { from, to } = view.state.selection;
    // The fifth argument is ProseMirror's `deflt` — the transaction the editor
    // would apply if no handler claimed the input. It is passed because the
    // signature requires it, and the fall-through below is what actually
    // applies it when nothing claims the character.
    const handled = view.someProp('handleTextInput', (f) =>
      f(view, from, to, ch, () => view.state.tr.insertText(ch, from, to)),
    );
    if (!handled) {
      view.dispatch(view.state.tr.insertText(ch, from, to));
    }
  }
}

function docOf(editor: Editor): CodexDoc {
  return editor.getJSON() as CodexDoc;
}

/** Every node of a type, anywhere in the document. */
function nodesOfType(node: CodexNode | CodexDoc, type: string): CodexNode[] {
  const found: CodexNode[] = [];
  const walk = (n: CodexNode) => {
    if (n.type === type) found.push(n);
    n.content?.forEach(walk);
  };
  walk(node as CodexNode);
  return found;
}

/** Every text node carrying a link mark, with that mark's attributes. */
function links(doc: CodexDoc): { text: string; attrs: Record<string, unknown> }[] {
  const out: { text: string; attrs: Record<string, unknown> }[] = [];
  const walk = (n: CodexNode) => {
    const link = n.marks?.find((m) => m.type === 'link');
    if (link && typeof n.text === 'string') out.push({ text: n.text, attrs: link.attrs ?? {} });
    n.content?.forEach(walk);
  };
  walk(doc as CodexNode);
  return out;
}

describe('the inline tag rule, against the heading rule it shares a character with', () => {
  it('makes a heading from a hash and a space', () => {
    const editor = makeEditor();
    typeText(editor, '# Runbook');
    const headings = nodesOfType(docOf(editor), 'heading');
    expect(headings).toHaveLength(1);
    expect(headings[0].attrs?.level).toBe(1);
    expect(nodesOfType(docOf(editor), NODE_INLINE_TAG)).toHaveLength(0);
    editor.destroy();
  });

  it('makes a tag from a hash and a word', () => {
    // The other direction of the same collision. Both assertions are needed:
    // a rule that produced a tag AND a heading would satisfy either alone.
    const editor = makeEditor();
    typeText(editor, '#design ');
    const tags = nodesOfType(docOf(editor), NODE_INLINE_TAG);
    expect(tags).toHaveLength(1);
    expect(tags[0].attrs?.[TAG_ATTRS.label]).toBe('design');
    expect(nodesOfType(docOf(editor), 'heading')).toHaveLength(0);
    editor.destroy();
  });

  it('makes a tag mid-sentence and keeps the space that triggered it', () => {
    const editor = makeEditor();
    typeText(editor, 'see #design docs');
    const doc = docOf(editor);
    expect(nodesOfType(doc, NODE_INLINE_TAG)).toHaveLength(1);
    // The trailing space must survive, or the tag runs into the next word.
    expect(JSON.stringify(doc)).toContain('docs');
    expect(JSON.stringify(doc)).not.toContain('#design');
    editor.destroy();
  });

  it('leaves an issue number as text', () => {
    // The single most common hash in ordinary prose. Turning "#42" into an
    // org-scoped tag called "42" would put it in every autocomplete for
    // everyone, and there is no tag-management surface to remove it with.
    const editor = makeEditor();
    typeText(editor, 'see issue #42 today');
    expect(nodesOfType(docOf(editor), NODE_INLINE_TAG)).toHaveLength(0);
    expect(JSON.stringify(docOf(editor))).toContain('#42');
    editor.destroy();
  });

  it('does not make a tag from a hash inside a word', () => {
    const editor = makeEditor();
    typeText(editor, 'C#7 and F#5 ');
    expect(nodesOfType(docOf(editor), NODE_INLINE_TAG)).toHaveLength(0);
    editor.destroy();
  });

  it('accepts a label with digits as long as it has a letter', () => {
    const editor = makeEditor();
    typeText(editor, '#v2_release ');
    const tags = nodesOfType(docOf(editor), NODE_INLINE_TAG);
    expect(tags).toHaveLength(1);
    expect(tags[0].attrs?.[TAG_ATTRS.label]).toBe('v2_release');
    editor.destroy();
  });

  it('agrees with the exported predicate about what a label is', () => {
    // The predicate is what the paste path and the tests reason with; the
    // regex is what the editor runs. They must not drift.
    for (const label of ['design', 'v2', 'a-b', 'a_b']) {
      expect(isTagLabel(label), label).toBe(true);
      expect(TAG_INPUT_REGEX.test(`#${label} `), label).toBe(true);
    }
    for (const label of ['42', '', '-', '_']) {
      expect(isTagLabel(label), label).toBe(false);
      expect(TAG_INPUT_REGEX.test(`#${label} `), label).toBe(false);
    }
  });
});

describe('the wikilink rules', () => {
  it('resolves a bare wikilink to the page it names', () => {
    const editor = makeEditor();
    typeText(editor, '[[Runbook]]');
    const found = links(docOf(editor));
    expect(found).toHaveLength(1);
    expect(found[0].text).toBe('Runbook');
    expect(found[0].attrs[LINK_ATTRS.pageId]).toBe('page-runbook');
    // Never an href: a page's URL depends on the space it is read in.
    expect(found[0].attrs[LINK_ATTRS.href]).toBeNull();
    expect(found[0].attrs[LINK_ATTRS.targetTitle]).toBeNull();
    editor.destroy();
  });

  it('resolves case-insensitively, because two people type a title two ways', () => {
    const editor = makeEditor();
    typeText(editor, '[[on-call ROTA]]');
    expect(links(docOf(editor))[0].attrs[LINK_ATTRS.pageId]).toBe('page-rota');
    editor.destroy();
  });

  it('keeps the display half of an alias and links by the target half', () => {
    const editor = makeEditor();
    typeText(editor, '[[Runbook|the escalation steps]]');
    const found = links(docOf(editor));
    expect(found[0].text).toBe('the escalation steps');
    expect(found[0].attrs[LINK_ATTRS.pageId]).toBe('page-runbook');
    editor.destroy();
  });

  it('leaves a link unresolved when nothing matches, carrying the title', () => {
    const editor = makeEditor();
    typeText(editor, '[[Incident review]]');
    const found = links(docOf(editor));
    expect(found).toHaveLength(1);
    expect(found[0].text).toBe('Incident review');
    expect(found[0].attrs[LINK_ATTRS.pageId]).toBeNull();
    expect(found[0].attrs[LINK_ATTRS.targetTitle]).toBe('Incident review');
    editor.destroy();
  });

  it('turns a matching embed into a page include', () => {
    const editor = makeEditor();
    typeText(editor, '![[Runbook]]');
    const includes = nodesOfType(docOf(editor), 'pageInclude');
    expect(includes).toHaveLength(1);
    expect(includes[0].attrs?.page_id).toBe('page-runbook');
    // And not also a link — the two rules must not both fire.
    expect(links(docOf(editor))).toHaveLength(0);
    editor.destroy();
  });

  it('degrades an unmatched embed to an unresolved link and says so', () => {
    // There is deliberately no unresolved-EMBED state: an embed renders another
    // page's body, and a placeholder claiming to be one would be a hole in the
    // page. The notice is what stops the substitution being silent.
    const notices: string[] = [];
    const editor = new Editor({
      extensions: codexExtensions({
        wikilinks: {
          getPages: () => PAGES,
          getCurrentPageId: () => 'page-current',
          onNotice: (m) => notices.push(m),
        },
      }),
      content: { type: 'doc', content: [{ type: 'paragraph' }] },
    });

    typeText(editor, '![[Incident review]]');
    expect(nodesOfType(docOf(editor), 'pageInclude')).toHaveLength(0);
    expect(links(docOf(editor))[0].attrs[LINK_ATTRS.targetTitle]).toBe('Incident review');
    expect(notices).toHaveLength(1);
    expect(notices[0]).toContain('Incident review');
    editor.destroy();
  });

  it('does not let a page link to itself', () => {
    // An include would be a cycle and a link would go nowhere the reader is not
    // already, so the current page is excluded from resolution — which makes
    // this an UNRESOLVED link rather than a self-reference.
    const editor = new Editor({
      extensions: codexExtensions({
        wikilinks: { getPages: () => PAGES, getCurrentPageId: () => 'page-runbook' },
      }),
      content: { type: 'doc', content: [{ type: 'paragraph' }] },
    });
    typeText(editor, '[[Runbook]]');
    expect(links(docOf(editor))[0].attrs[LINK_ATTRS.pageId]).toBeNull();
    editor.destroy();
  });
});

describe('parseWikilink degrades both halves of the pipe mistake', () => {
  it('reads a plain target', () => {
    expect(parseWikilink('Runbook')).toEqual({ target: 'Runbook', display: 'Runbook' });
  });

  it('reads an alias', () => {
    expect(parseWikilink('Runbook|the steps')).toEqual({
      target: 'Runbook',
      display: 'the steps',
    });
  });

  it('treats a missing display half as no alias at all', () => {
    expect(parseWikilink('Runbook|')).toEqual({ target: 'Runbook', display: 'Runbook' });
  });

  it('treats a missing target half as naming the display', () => {
    // `[[|x]]` is a typo with an obvious intention: the author named one page.
    expect(parseWikilink('|Runbook')).toEqual({ target: 'Runbook', display: 'Runbook' });
  });

  it('produces nothing at all when neither half names anything', () => {
    // No link rather than a link to nowhere. The literal text stays, which is
    // both recoverable and visibly wrong.
    expect(parseWikilink('|')).toBeNull();
    expect(parseWikilink('   ')).toBeNull();
    expect(parseWikilink('  |  ')).toBeNull();
  });

  it('splits on the first pipe only, so a display half may contain one', () => {
    expect(parseWikilink('Runbook|a | b')).toEqual({ target: 'Runbook', display: 'a | b' });
  });
});

describe('wikilinkAttrs', () => {
  it('never carries an href, in either state', () => {
    // A page's URL depends on the space it is read in, so a document that baked
    // one in would be wrong for a reader who arrived another way.
    expect(wikilinkAttrs(PAGES[0], 'Runbook')[LINK_ATTRS.href]).toBeNull();
    expect(wikilinkAttrs(null, 'Nothing')[LINK_ATTRS.href]).toBeNull();
  });

  it('carries exactly one of page_id and target_title', () => {
    const resolved = wikilinkAttrs(PAGES[0], 'Runbook');
    expect(resolved[LINK_ATTRS.pageId]).toBe('page-runbook');
    expect(resolved[LINK_ATTRS.targetTitle]).toBeNull();

    const unresolved = wikilinkAttrs(null, 'Nothing');
    expect(unresolved[LINK_ATTRS.pageId]).toBeNull();
    expect(unresolved[LINK_ATTRS.targetTitle]).toBe('Nothing');
  });
});
