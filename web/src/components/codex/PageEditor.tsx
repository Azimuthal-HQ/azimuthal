/**
 * Editing one Codex page: autosave, publish, and the refusals in between
 * (issue #15 B4).
 *
 * ## The two version numbers, and why only one of them may move
 *
 * `base_version` is the published version this editing session started from.
 * The preservation ids in the document were minted against *that* document, so
 * publish re-derives it to resolve them. It is therefore fixed for the life of
 * a session and travels unchanged into every autosave and every publish —
 * including an overwrite, where the whole point is to resolve against the
 * revision the author actually edited rather than against whatever the page
 * has become. Bumping it to the current version to "fix" a conflict would
 * splice the wrong bytes into somebody's page.
 *
 * ## Why autosave is guarded by a comparison
 *
 * A draft is the author's work, and this component can put a *different*
 * document into the editor — after a conflict reload, that is exactly what it
 * does. If autosave fired on load, the reloaded published document would be
 * written over the draft it was supposed to leave alone, and the author's
 * unpublished work would be gone with no warning. So nothing is saved until
 * the document actually differs from what was loaded. That invariant is what
 * makes "reload, your draft survives" true rather than aspirational.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { Check, CloudOff, Loader2, Save, Trash2 } from 'lucide-react';

import {
  APIError,
  PublishConflictError,
  PublishLostContentError,
  friendlyErrorMessage,
  useDiscardPageDraft,
  usePublishPage,
  useSavePageDraft,
  useUploadPageImage,
} from '../../lib/api';
import type {
  CodexEditableDocument,
  CodexPublishConflict,
  CodexPublishLostContent,
  WikiPage,
} from '../../lib/api';
import type { CodexDoc } from '../../lib/codex/schema';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../ui/dialog';
import { CodexEditor } from './CodexEditor';
import {
  LostContentDialog,
  PublishConflictDialog,
  UnresolvablePreservedDialog,
} from './PublishDialogs';

/** How long the editor waits after the last keystroke before autosaving. */
export const AUTOSAVE_DELAY_MS = 1200;

type SaveState = 'idle' | 'saving' | 'saved' | 'failed';

/** The publish refusal being answered, if any. Mutually exclusive by design. */
type Refusal =
  | { kind: 'conflict'; detail: CodexPublishConflict }
  | { kind: 'lost'; detail: CodexPublishLostContent }
  | { kind: 'unresolvable'; message: string }
  | null;

interface PageEditorProps {
  spaceId: string;
  pageId: string;
  /** The document surface's payload; the session's starting point. */
  document: CodexEditableDocument;
  pages: WikiPage[];
  /** Leave edit mode. */
  onClose: () => void;
  /** Refetch the document — used by the conflict and 422 reload paths. */
  onReloadDocument: () => Promise<CodexEditableDocument | undefined>;
}

export function PageEditor({
  spaceId,
  pageId,
  document,
  pages,
  onClose,
  onReloadDocument,
}: PageEditorProps) {
  // The session's starting point. A draft wins over the published document —
  // that is what "your draft is restored when you come back" means — and it
  // brings its own base_version, which is the version IT was started from.
  const opened = document.draft ?? { doc: document.doc, base_version: document.base_version };

  const [title, setTitle] = useState(document.draft?.title ?? document.title);
  const [doc, setDoc] = useState<CodexDoc>(opened.doc);
  const [baseVersion, setBaseVersion] = useState(opened.base_version);
  /** Remounts the editor when a genuinely different document is loaded. */
  const [editorGeneration, setEditorGeneration] = useState(0);
  const [initialDoc, setInitialDoc] = useState<CodexDoc>(opened.doc);

  const [restoredDraft] = useState(() => document.draft != null);
  const [staleDraft, setStaleDraft] = useState(document.draft?.stale ?? false);

  const [saveState, setSaveState] = useState<SaveState>('idle');
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  /**
   * The refusal currently being answered. One at a time and mutually
   * exclusive — an overwrite can be refused again for lost content, and two
   * modal dialogs stacked on top of each other is not a dialogue.
   */
  const [refusal, setRefusal] = useState<Refusal>(null);
  /**
   * The author has said, in those words, to replace a newer published version.
   * It outlives the conflict dialog because a subsequent lost-content
   * confirmation has to carry the decision forward — without it the republish
   * hits the version guard again and the two dialogs bounce off each other
   * forever.
   */
  const [overwriteAgreed, setOverwriteAgreed] = useState(false);
  const [discardOpen, setDiscardOpen] = useState(false);

  const saveDraft = useSavePageDraft(spaceId, pageId);
  const discardDraft = useDiscardPageDraft(spaceId, pageId);
  const publish = usePublishPage(spaceId, pageId);
  const uploadImage = useUploadPageImage(spaceId, pageId);

  /**
   * What was last loaded into the editor, as a string. Autosave compares
   * against it, so a session that only opened a page never writes a draft —
   * and a conflict reload never overwrites the draft it was meant to keep.
   */
  const loadedRef = useRef(
    JSON.stringify({ title: document.draft?.title ?? document.title, doc: opened.doc }),
  );
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latestRef = useRef({ title, doc, baseVersion });

  useEffect(() => {
    latestRef.current = { title, doc, baseVersion };
  }, [title, doc, baseVersion]);

  /**
   * The mutation, behind a ref.
   *
   * `useMutation` returns a new object on every render, so a `flushSave` that
   * closed over it directly would itself be a new function on every render —
   * and the leave-the-page effect below would then tear down and re-register
   * on every render, firing its cleanup save each time. That defeats the
   * debounce completely: every keystroke saves. Reading it through a ref
   * keeps `flushSave` stable, which is what lets that effect run exactly
   * twice — once on mount, once on unmount.
   */
  const saveDraftRef = useRef(saveDraft);
  useEffect(() => {
    saveDraftRef.current = saveDraft;
  });

  const flushSave = useCallback(async () => {
    const current = latestRef.current;
    const snapshot = JSON.stringify({ title: current.title, doc: current.doc });
    if (snapshot === loadedRef.current) return;
    // Claim the snapshot before awaiting, so two flushes racing (the debounce
    // firing as the component unmounts) do not both send the same draft.
    loadedRef.current = snapshot;
    setSaveState('saving');
    try {
      await saveDraftRef.current.mutateAsync({
        title: current.title,
        doc: current.doc,
        base_version: current.baseVersion,
      });
      setSaveState('saved');
    } catch (err) {
      // Autosave failing is not the moment to interrupt somebody who is
      // typing; the indicator says so, and publish will report properly.
      // The snapshot is released so the next change retries this content
      // rather than treating it as already saved.
      loadedRef.current = '';
      setSaveState('failed');
      setErrorMessage(friendlyErrorMessage(err, 'Your draft was not saved. Check your connection.'));
    }
  }, []);

  /** Debounced autosave on every change. */
  useEffect(() => {
    const snapshot = JSON.stringify({ title, doc });
    if (snapshot === loadedRef.current) return;
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => void flushSave(), AUTOSAVE_DELAY_MS);
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [title, doc, flushSave]);

  /**
   * Leaving with a pending edit must still save it.
   *
   * The debounce means "leave the page" and "the draft is safe" are otherwise
   * up to 1.2 seconds apart, and closing the tab in that window loses the last
   * thing typed. `pagehide` is the event that survives a bfcache navigation,
   * where `beforeunload` does not fire at all.
   */
  useEffect(() => {
    const onHide = () => void flushSave();
    window.addEventListener('pagehide', onHide);
    return () => {
      window.removeEventListener('pagehide', onHide);
      // Unmounting is leaving too — the sidebar navigating to another page
      // unmounts this component without any window event at all.
      void flushSave();
    };
  }, [flushSave]);

  /** Load a document into the editor and reset the autosave baseline. */
  function loadIntoEditor(nextDoc: CodexDoc, nextTitle: string, nextBase: number) {
    setDoc(nextDoc);
    setInitialDoc(nextDoc);
    setTitle(nextTitle);
    setBaseVersion(nextBase);
    loadedRef.current = JSON.stringify({ title: nextTitle, doc: nextDoc });
    setEditorGeneration((n) => n + 1);
  }

  async function doPublish(options: { overwrite?: boolean; acknowledgedLostIds?: string[] } = {}) {
    setErrorMessage(null);
    if (timerRef.current) clearTimeout(timerRef.current);
    const overwrite = options.overwrite ?? overwriteAgreed;

    try {
      await publish.mutateAsync({
        title,
        doc,
        // Unchanged, always. See the note at the top of this file.
        base_version: baseVersion,
        ...(options.acknowledgedLostIds?.length
          ? { acknowledged_lost_ids: options.acknowledgedLostIds }
          : {}),
        ...(overwrite ? { overwrite: true } : {}),
      });
      setRefusal(null);
      setOverwriteAgreed(false);
      onClose();
    } catch (err) {
      if (err instanceof PublishConflictError) {
        setRefusal({ kind: 'conflict', detail: err.detail });
        return;
      }
      if (err instanceof PublishLostContentError) {
        setRefusal({ kind: 'lost', detail: err.detail });
        return;
      }
      // The 422s. `handleDocumentError` gives both of them VALIDATION_ERROR
      // and prose written for a person, so friendlyErrorMessage passes them.
      if (err instanceof APIError && err.status === 422) {
        setRefusal({
          kind: 'unresolvable',
          message: friendlyErrorMessage(
            err,
            'This edit does not match the page it started from.',
          ),
        });
        return;
      }
      setErrorMessage(friendlyErrorMessage(err, 'The page was not published.'));
    }
  }

  async function reloadFromServer() {
    setRefusal(null);
    // Reloading abandons an overwrite decision: the author is now looking at
    // the newer version and has not agreed to replace anything.
    setOverwriteAgreed(false);
    const fresh = await onReloadDocument();
    if (!fresh) return;
    // Deliberately the PUBLISHED document, not the draft: the author asked to
    // see what changed. Their draft row is untouched, and the autosave guard
    // means nothing overwrites it until they type.
    loadIntoEditor(fresh.doc, fresh.title, fresh.base_version);
    setStaleDraft(false);
  }

  async function handleDiscard() {
    try {
      await discardDraft.mutateAsync();
      setDiscardOpen(false);
      onClose();
    } catch (err) {
      setDiscardOpen(false);
      setErrorMessage(friendlyErrorMessage(err, 'The draft was not discarded.'));
    }
  }

  const busy = publish.isPending;

  return (
    <div className="space-y-3" data-testid="codex-page-editor">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          className="flex-1 text-[var(--text-xl)] font-bold"
          placeholder="Page title"
          aria-label="Page title"
          data-testid="codex-title-input"
        />
        <SaveIndicator state={saveState} />
      </div>

      {restoredDraft && (
        <p
          data-testid="codex-draft-restored"
          className="rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface-hover)] px-3 py-1.5 text-[var(--text-sm)] text-[var(--color-text-muted)]"
        >
          Your unpublished draft was restored. Readers still see the published version until you
          publish.
          {staleDraft && (
            <strong className="ml-1 text-[var(--color-warning)]">
              This page has been published again since you started — publishing will ask you what to
              do.
            </strong>
          )}
        </p>
      )}

      <CodexEditor
        key={`${pageId}-${editorGeneration}`}
        initialDoc={initialDoc}
        spaceId={spaceId}
        pageId={pageId}
        pages={pages}
        onChange={setDoc}
        onUploadImage={(file) => uploadImage.mutateAsync(file)}
        onImageError={(message) => setErrorMessage(message)}
        disabled={busy}
      />

      {errorMessage && (
        <p data-testid="codex-editor-error" className="text-[var(--text-sm)] text-[var(--color-danger)]">
          {errorMessage}
        </p>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <Button onClick={() => void doPublish()} disabled={busy} data-testid="codex-publish">
          {busy ? 'Publishing…' : 'Publish'}
        </Button>
        <Button variant="outline" onClick={onClose} disabled={busy} data-testid="codex-close-editor">
          Done for now
        </Button>
        <Button
          variant="ghost"
          onClick={() => setDiscardOpen(true)}
          disabled={busy}
          data-testid="codex-discard-draft"
          className="text-[var(--color-danger)]"
        >
          <Trash2 className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
          Discard draft
        </Button>
        <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
          Your changes are saved as a private draft. Readers see the published page until you
          publish.
        </p>
      </div>

      {refusal?.kind === 'conflict' && (
        <PublishConflictDialog
          detail={refusal.detail}
          busy={busy}
          onReload={() => void reloadFromServer()}
          onOverwrite={() => {
            setOverwriteAgreed(true);
            void doPublish({ overwrite: true });
          }}
          onCancel={() => setRefusal(null)}
        />
      )}

      {refusal?.kind === 'lost' && (
        <LostContentDialog
          detail={refusal.detail}
          busy={busy}
          onConfirm={() => void doPublish({ acknowledgedLostIds: refusal.detail.lost_ids })}
          onCancel={() => setRefusal(null)}
        />
      )}

      {refusal?.kind === 'unresolvable' && (
        <UnresolvablePreservedDialog
          message={refusal.message}
          onReload={() => void reloadFromServer()}
          onCancel={() => setRefusal(null)}
        />
      )}

      <Dialog open={discardOpen} onOpenChange={setDiscardOpen}>
        <DialogContent data-testid="codex-discard-dialog">
          <DialogHeader>
            <DialogTitle>Discard this draft?</DialogTitle>
            <DialogDescription>
              Everything you have written since the last publish will be removed. The published page
              is not affected.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setDiscardOpen(false)}>
              Keep the draft
            </Button>
            <Button
              variant="destructive"
              onClick={() => void handleDiscard()}
              disabled={discardDraft.isPending}
              data-testid="codex-discard-confirm"
            >
              {discardDraft.isPending ? 'Discarding…' : 'Discard draft'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function SaveIndicator({ state }: { state: SaveState }) {
  const content = {
    idle: { icon: Save, text: 'Draft', tone: 'var(--color-text-muted)' },
    saving: { icon: Loader2, text: 'Saving…', tone: 'var(--color-text-muted)' },
    saved: { icon: Check, text: 'Draft saved', tone: 'var(--color-success)' },
    failed: { icon: CloudOff, text: 'Not saved', tone: 'var(--color-danger)' },
  }[state];
  const Icon = content.icon;

  return (
    <span
      data-testid="codex-save-state"
      data-state={state}
      aria-live="polite"
      className="flex shrink-0 items-center gap-1.5 text-[var(--text-xs)]"
      style={{ color: content.tone }}
    >
      <Icon className={`h-3.5 w-3.5 ${state === 'saving' ? 'animate-spin' : ''}`} aria-hidden="true" />
      {content.text}
    </span>
  );
}
