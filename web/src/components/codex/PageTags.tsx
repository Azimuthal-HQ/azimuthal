/**
 * A page's tags — the one surface, in both of its modes (U4).
 *
 * ## Why one component and not two
 *
 * The reading view and the editor show the same list of the same tags, and the
 * chips have to look identical in both: a tag that changes shape when you press
 * Edit reads as a different thing. So there is one chip renderer, and `editable`
 * decides what is wrapped around it, rather than a display component and an
 * editor component that drift apart the first time a token changes.
 *
 * The editing affordances live in a nested component rather than behind an `if`
 * in this one, and that is a hook question, not a style question. React runs
 * every hook in a component unconditionally, so writing the editor inline would
 * make a reading surface fetch the org's entire tag list and construct a
 * mutation it can never fire — on every page view, for every reader, including
 * ones with no right to edit anything. Nesting keeps that work in the subtree
 * that actually needs it.
 *
 * ## Why a whole-set PUT and no local copy of the list
 *
 * `useSetPageTags` replaces the page's entire tag set, so every edit here — add
 * or remove — computes the complete list it wants and sends that. There is no
 * add endpoint and no remove endpoint to reach for.
 *
 * The chips are rendered straight from `usePageTags`, with no optimistic
 * mirror, and that is deliberate rather than lazy. A tag's display name is the
 * first spelling anybody used, decided server-side: type `Runbook` for a tag
 * already known as `runbook` and the server returns `runbook`. Echoing the
 * typed text would show a name that is not the tag's, until the refetch
 * silently corrected it. The mutation invalidates the page's tags on success,
 * so the row updates from the server or not at all.
 *
 * ## Why creating a tag is just typing one
 *
 * Tags have no administration surface: they come into existence by use, and
 * this PUT is the only constructor there is. The input therefore has to accept
 * a name that matches nothing in the org — the suggestion list is a
 * convenience, never a whitelist.
 */
import { useMemo, useState, type KeyboardEvent } from 'react';
import { Link } from 'react-router-dom';
import { Tag as TagIcon, X } from 'lucide-react';

import { friendlyErrorMessage, useOrgTags, usePageTags, useSetPageTags } from '../../lib/api';
import type { CodexTag } from '../../lib/api';
import { getCurrentOrgId } from '../../lib/auth';
import { tagBrowsePath } from './tagLinks';

/** How many suggestions the input offers before it stops listing them. */
const MAX_SUGGESTIONS = 8;

const chipClasses =
  'inline-flex items-center gap-1 rounded-full border border-[var(--color-border)] bg-[color-mix(in_srgb,var(--module-codex)_12%,transparent)] px-2 py-0.5 text-[var(--text-xs)] text-[var(--color-text)]';

interface PageTagsProps {
  spaceId: string;
  pageId: string;
  /** Reading surfaces pass false: chips only, and nothing at all when empty. */
  editable: boolean;
}

export function PageTags({ spaceId, pageId, editable }: PageTagsProps) {
  const { data: tags = [] } = usePageTags(spaceId, pageId);

  if (editable) {
    return <TagEditor spaceId={spaceId} pageId={pageId} tags={tags} />;
  }

  // A reading surface with nothing to say says nothing. An empty "Tags:" label
  // on every untagged page is chrome that carries no information, and most
  // pages are untagged.
  if (tags.length === 0) return null;

  return (
    <div data-testid="codex-page-tags" className="mt-2 flex flex-wrap items-center gap-1.5">
      <TagIcon className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" aria-hidden="true" />
      {tags.map((tag) => (
        <Link
          key={tag.id}
          to={tagBrowsePath(spaceId, tag.name)}
          data-testid="codex-page-tag"
          data-slug={tag.slug}
          className={`${chipClasses} transition-colors hover:border-[var(--module-codex)] hover:bg-[color-mix(in_srgb,var(--module-codex)_22%,transparent)]`}
        >
          {tag.name}
        </Link>
      ))}
    </div>
  );
}

interface TagEditorProps {
  spaceId: string;
  pageId: string;
  tags: CodexTag[];
}

function TagEditor({ spaceId, pageId, tags }: TagEditorProps) {
  const orgId = getCurrentOrgId();
  // The opts spread REPLACES the hook's own `enabled: !!orgId` rather than
  // adding to it, so that guard has to be restated here.
  const { data: orgTags = [] } = useOrgTags(orgId, { enabled: !!orgId });
  const setTags = useSetPageTags(spaceId, pageId);

  const [input, setInput] = useState('');
  /**
   * The label the last submission tried to add. It is kept so a refusal can
   * name it: the server's 400 for a name that cannot become a slug says why the
   * name is impossible but not which name it was, and by the time the message
   * appears the input has been cleared.
   */
  const [rejected, setRejected] = useState<string | null>(null);

  const names = useMemo(() => tags.map((t) => t.name), [tags]);

  // The suggestions: org tags this page does not already carry, matched on what
  // has been typed. Filtered here rather than server-side because the org's tag
  // list is one small query the browse surface wants anyway, and a request per
  // keystroke would buy nothing.
  const suggestions = useMemo(() => {
    const typed = input.trim().toLowerCase();
    if (!typed) return [] as CodexTag[];
    const taken = new Set(names.map((n) => n.toLowerCase()));
    return orgTags
      .filter((t) => !taken.has(t.name.toLowerCase()))
      .filter((t) => t.name.toLowerCase().includes(typed) || t.slug.includes(typed))
      .slice(0, MAX_SUGGESTIONS);
  }, [input, names, orgTags]);

  function commit(label: string) {
    const trimmed = label.trim();
    setInput('');
    if (!trimmed) return;
    // A duplicate is not an error and not a write: the page already carries
    // this tag, so the whole-set PUT would send the list it already has.
    if (names.some((n) => n.toLowerCase() === trimmed.toLowerCase())) return;
    setRejected(trimmed);
    setTags.mutate([...names, trimmed]);
  }

  function removeAt(index: number) {
    setRejected(null);
    setTags.mutate(names.filter((_, i) => i !== index));
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter' || event.key === ',') {
      // Enter would submit the surrounding form and a comma would land in the
      // box; both are the commit gesture instead.
      event.preventDefault();
      commit(input);
      return;
    }
    if (event.key === 'Backspace' && input === '' && names.length > 0) {
      // Only on an empty input. Backspace is first and foremost a text-editing
      // key, and deleting a tag mid-typo would be a destructive surprise.
      event.preventDefault();
      removeAt(names.length - 1);
    }
  }

  return (
    <div data-testid="codex-page-tags" className="mt-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <TagIcon
          className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]"
          aria-hidden="true"
        />
        {tags.map((tag, index) => (
          /* The same chip, without the link. Following it would leave an
             editing session — and the draft behind it — on a click that reads
             as incidental. */
          <span
            key={tag.id}
            data-testid="codex-page-tag"
            data-slug={tag.slug}
            className={chipClasses}
          >
            {tag.name}
            <button
              type="button"
              data-testid="codex-tag-remove"
              aria-label={`Remove tag ${tag.name}`}
              onClick={() => removeAt(index)}
              className="rounded-full p-0.5 text-[var(--color-text-muted)] transition-colors hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]"
            >
              <X className="h-3 w-3" aria-hidden="true" />
            </button>
          </span>
        ))}

        <span className="relative">
          <label className="sr-only" htmlFor={`codex-tag-input-${pageId}`}>
            Add a tag
          </label>
          <input
            id={`codex-tag-input-${pageId}`}
            data-testid="codex-tag-input"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={names.length === 0 ? 'Add a tag…' : 'Add another…'}
            className="w-36 rounded-[var(--radius-md)] border border-transparent bg-transparent px-1.5 py-0.5 text-[var(--text-xs)] text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] focus:border-[var(--module-codex)] focus:bg-[var(--color-input)] focus:outline-none"
          />

          {suggestions.length > 0 && (
            <ul className="absolute left-0 top-full z-10 mt-1 w-48 overflow-hidden rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] shadow-lg">
              {suggestions.map((tag) => (
                <li key={tag.id}>
                  <button
                    type="button"
                    data-testid="codex-tag-suggestion"
                    onClick={() => commit(tag.name)}
                    className="block w-full px-2.5 py-1.5 text-left text-[var(--text-xs)] text-[var(--color-text)] transition-colors hover:bg-[var(--color-surface-hover)]"
                  >
                    {tag.name}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </span>
      </div>

      {/* The inline-tag semantic, stated where the tags are edited. Publishing
          a page adds every #tag in its body to this list, and the list is the
          authority afterwards — the body is an input to it, not a mirror of it.
          Somebody who deletes a #tag from their prose and expects the tag to
          disappear needs to be told here rather than to discover it on the
          browse page a week later. */}
      <p className="mt-1.5 max-w-[52ch] text-[var(--text-xs)] leading-[1.5] text-[var(--color-text-muted)]">
        Any #tag you write in the page body is added to this list when you publish. This list is
        what counts from then on: deleting the #tag from the body will not take the tag off the page
        — remove it here.
      </p>

      {setTags.isPending && (
        <p className="mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">Saving tags…</p>
      )}

      {setTags.error && (
        <p
          data-testid="codex-page-tags-error"
          className="mt-1 text-[var(--text-xs)] text-[var(--color-danger)]"
        >
          {rejected ? `“${rejected}” was not added. ` : ''}
          {friendlyErrorMessage(setTags.error, 'The tags could not be saved.')}
        </p>
      )}
    </div>
  );
}
