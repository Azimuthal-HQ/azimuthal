/**
 * The inline `#tag` token.
 *
 * ## Why it is a node and not a mark
 *
 * A tag is a thing, not formatting applied to a run of text. Making it an
 * inline ATOM means the label cannot be edited out from under the token by
 * typing inside it — which matters because the label is not decoration: the
 * server reads it at publish and aggregates it into the page's tags. A token
 * whose visible text and whose stored `label` disagreed would tag the page with
 * something nobody typed.
 *
 * ## The heading collision, and what actually separates the two
 *
 * `#` at the start of a line is markdown's heading. `#` in front of a word is a
 * tag. Both are wanted, and the disambiguator is **the space**:
 *
 *     "# "        -> heading      (TipTap's own rule: /^(#{1,6})\s$/)
 *     "#design "  -> tag          (this rule)
 *     "## "       -> heading 2
 *     "#design"   -> nothing yet; neither rule has fired
 *
 * The two regexes cannot both match the same input, and that is a property of
 * their shapes rather than of the order they are registered in: the heading
 * rule requires whitespace immediately after the hashes, and this one requires
 * at least one word character there. `extensions.test.ts` and the input-rule
 * tests assert the collision in both directions, because "it happens to work
 * today" and "it cannot happen" look identical until somebody widens a regex.
 *
 * ## Why a tag must contain a letter
 *
 * `#42` stays text. Issue and ticket numbers are the single most common `#`
 * in ordinary prose ("see issue #42"), and silently turning one into an
 * org-scoped tag — creating a tag row called "42" that then appears in every
 * autocomplete for everyone — is both wrong and hard to undo. Requiring a
 * letter costs `#42` as a tag and buys back every sentence that mentions a
 * number.
 */
import { InputRule, Node, mergeAttributes } from '@tiptap/core';
import { ReactNodeViewRenderer } from '@tiptap/react';

import { NODE_INLINE_TAG, TAG_ATTRS } from '../../../lib/codex/schema';
import { InlineTagView } from '../nodeviews/InlineTagView';

/**
 * The characters a tag label may contain, and the requirement that at least one
 * of them is a letter.
 *
 * Exported so the tests can state the rule they are pinning rather than
 * repeating a regex, and so the paste converter can ask the same question.
 */
export const TAG_LABEL_PATTERN = /^[A-Za-z0-9_-]*[A-Za-z][A-Za-z0-9_-]*$/;

/**
 * The type-time rule.
 *
 * The lookbehind is what keeps `#` inside a word from starting a tag — `a#b`
 * is not one — while still allowing a tag at the very start of a line. It fires
 * on the trailing space, like every other input rule in the editor
 * (`/^\s*>\s$/`, `/^(\d+)\.\s$/`, ``/^```([a-z]+)?[\s\n]$/``), rather than on
 * the last character of the word: firing on the word itself would convert
 * "#design" the moment it was complete and leave no way to type the literal
 * text.
 */
export const TAG_INPUT_REGEX = /(?<=^|\s)#([A-Za-z0-9_-]*[A-Za-z][A-Za-z0-9_-]*)(\s)$/;

/** Reports whether a label is one the tag rules would accept. */
export function isTagLabel(label: string): boolean {
  return TAG_LABEL_PATTERN.test(label);
}

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    codexTags: {
      insertInlineTag: (label: string) => ReturnType;
    };
  }
}

/**
 * The inline tag node. `label` is read by the markdown projection
 * (`internal/core/wiki/doc/text.go`) and by the publish-time aggregation, so it
 * is in `projectedAttrs` in the schema manifest and its name is checked in both
 * languages.
 */
export const InlineTag = Node.create({
  name: NODE_INLINE_TAG,
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      [TAG_ATTRS.label]: {
        default: '',
        parseHTML: (el: HTMLElement) => el.getAttribute('data-label') ?? '',
        renderHTML: (attrs: Record<string, unknown>) => ({
          'data-label': String(attrs[TAG_ATTRS.label] ?? ''),
        }),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'span[data-codex-tag]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['span', mergeAttributes(HTMLAttributes, { 'data-codex-tag': '' })];
  },

  addNodeView() {
    return ReactNodeViewRenderer(InlineTagView, { as: 'span' });
  },

  addInputRules() {
    return [
      new InputRule({
        find: TAG_INPUT_REGEX,
        handler: ({ range, match, chain }) => {
          const label = match[1];
          // The trailing space is put back rather than swallowed. It is what
          // separates the token from the next word, and an author who typed it
          // did not ask for it to disappear.
          const trailing = match[2] ?? ' ';
          chain()
            .insertContentAt({ from: range.from, to: range.to }, [
              { type: NODE_INLINE_TAG, attrs: { [TAG_ATTRS.label]: label } },
              { type: 'text', text: trailing },
            ])
            .run();
        },
      }),
    ];
  },

  addCommands() {
    return {
      insertInlineTag:
        (label: string) =>
        ({ commands }) =>
          commands.insertContent({
            type: this.name,
            attrs: { [TAG_ATTRS.label]: label },
          }),
    };
  },
});
