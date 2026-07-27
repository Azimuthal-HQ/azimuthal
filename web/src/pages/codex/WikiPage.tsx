import { useState, useEffect } from 'react';
import { useParams } from 'react-router-dom';
import { Edit, AlertCircle, History, X, ChevronRight, PenLine, Share2, FolderInput } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import rehypeRaw from 'rehype-raw';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { Button } from '../../components/ui/button';
import { cn } from '../../lib/utils';
import {
  friendlyErrorMessage,
  useWikiPages,
  useWikiPage,
  useWikiRevisions,
  usePageDocument,
  useSpaceDrafts,
  useMe,
  useComments,
  useCreateComment,
  useEffectiveAccess,
  useSpacePageShares,
  pageShareState,
} from '../../lib/api';
import { ShareBadge } from '../../components/ShareBadge';
import { ShareDialog } from '../../components/ShareDialog';
import { MovePageDialog } from '../../components/MovePageDialog';
import { CodexDocRenderer } from '../../components/codex/CodexDocRenderer';
import { PageEditor } from '../../components/codex/PageEditor';

// ---------------------------------------------------------------------------
// Revisions panel
// ---------------------------------------------------------------------------

interface RevisionsPanelProps {
  spaceId: string;
  pageId: string;
  currentVersion: number;
  onClose: () => void;
}

function RevisionsPanel({ spaceId, pageId, currentVersion, onClose }: RevisionsPanelProps) {
  const { data: revisions = [], isLoading } = useWikiRevisions(spaceId, pageId);
  const [selected, setSelected] = useState<string | null>(null);
  const selectedRev = revisions.find(r => r.id === selected);

  return (
    <div className="flex h-full flex-col border-l border-[var(--color-border)] bg-[var(--color-surface)]">
      <div className="flex items-center justify-between border-b border-[var(--color-border)] px-4 py-3">
        <h3 className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">Revision History</h3>
        <button onClick={onClose} className="rounded p-1 text-[var(--color-text-muted)] hover:text-[var(--color-text)]">
          <X className="h-4 w-4" />
        </button>
      </div>
      {isLoading ? (
        <div className="flex flex-1 items-center justify-center text-[var(--color-text-muted)] text-[var(--text-sm)]">Loading…</div>
      ) : revisions.length === 0 ? (
        <div className="flex flex-1 items-center justify-center text-[var(--color-text-muted)] text-[var(--text-sm)]">No revisions yet.</div>
      ) : (
        <div className="flex flex-1 flex-col overflow-hidden">
          <div className="overflow-y-auto border-b border-[var(--color-border)]" style={{ maxHeight: '50%' }}>
            {revisions.map(rev => (
              <button
                key={rev.id}
                type="button"
                onClick={() => setSelected(selected === rev.id ? null : rev.id)}
                className={cn(
                  'flex w-full items-start gap-2 px-4 py-2.5 text-left transition-colors border-b border-[var(--color-border)] last:border-0',
                  selected === rev.id
                    ? 'bg-[var(--color-primary-muted)]'
                    : 'hover:bg-[var(--color-surface-hover)]',
                )}
              >
                <ChevronRight className={cn('mt-0.5 h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)] transition-transform', selected === rev.id && 'rotate-90')} />
                <div className="min-w-0">
                  <p className="text-[var(--text-sm)] text-[var(--color-text)]">
                    v{rev.version}
                    {rev.version === currentVersion && (
                      <span className="ml-2 rounded-full bg-[var(--color-primary-muted)] px-1.5 py-0.5 text-[var(--text-xs)] text-[var(--color-primary)]">current</span>
                    )}
                  </p>
                  <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                    {(rev.created_at ?? '').slice(0, 10)}
                  </p>
                </div>
              </button>
            ))}
          </div>
          {selectedRev && (
            <div className="flex-1 overflow-y-auto p-4">
              <p className="mb-2 text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
                v{selectedRev.version} — {selectedRev.title}
              </p>
              <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">{selectedRev.created_at.slice(0, 10)}</p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

/**
 * WikiPage renders the reading/editing surface for one page. Navigation —
 * the page tree, the scoped search, and the create-page affordance — lives
 * in the shell's CodexSidebar (ADR-0005), not here: the pre-P1 in-content
 * page panel this component once rendered is deleted, so the content column
 * starts immediately after the sidebar at full width.
 */
export function WikiPage() {
  const { spaceId = '', pageId } = useParams<{ spaceId: string; pageId: string }>();
  const { data: pages = [], isLoading, error } = useWikiPages(spaceId);

  const [activeId, setActiveId] = useState<string | null>(pageId ?? null);
  const [revisionsOpen, setRevisionsOpen] = useState(false);

  // The Codex sidebar tree navigates to pages/:pageId; follow the URL when
  // it changes so sidebar selection and page content stay in sync.
  useEffect(() => {
    if (pageId) setActiveId(pageId);
  }, [pageId]);

  // Inline edit state. The document editor owns everything about an editing
  // session — the draft, the version it started from, and every failure it
  // can report — so this only records whether one is open.
  const [editMode, setEditMode] = useState(false);

  // Selecting a different page (via the sidebar tree) leaves edit mode and
  // closes the revisions panel — the behaviour the in-content panel's
  // click handler used to provide, now keyed off the selection itself.
  useEffect(() => {
    setEditMode(false);
    setRevisionsOpen(false);
  }, [activeId]);

  // Auto-select first page once pages load (e.g. after a reload with no page
  // in the URL). This is a side effect, so it belongs in useEffect — a
  // useMemo runs inconsistently and made the selection (and the comments/
  // content that depend on it) race on reload.
  useEffect(() => {
    if (pages.length > 0 && !activeId) {
      setActiveId(pages[0].id);
    }
  }, [pages, activeId]);

  const { data: activePage } = useWikiPage(spaceId, activeId ?? '', { enabled: !!activeId });
  const { data: me } = useMe();

  /**
   * The page's shielded document (issue #15, ADR-0012).
   *
   * Fetched for reading as well as editing, and that is deliberate.
   * `activePage.doc` is the *stored* document, which still contains node types
   * outside the editor's schema — handing it to ProseMirror would drop them
   * silently and show a reader a blank where content exists. This route
   * shields first, so every preserved item arrives labelled. It needs only
   * space-read, so a reader who cannot edit can still open it.
   *
   * Legacy markdown pages (`doc` null) never reach it: they keep their
   * existing markdown rendering, unchanged, per migration 036's dual-format
   * contract. The one exception is entering edit mode, where the same route
   * performs the convert-on-first-edit that turns a markdown page into a
   * document — without writing anything until the author publishes.
   */
  const isDocumentPage = !!activePage?.doc;
  const {
    data: pageDocument,
    isLoading: documentLoading,
    error: documentError,
    refetch: refetchDocument,
  } = usePageDocument(spaceId, activeId ?? '', {
    enabled: !!activeId && (isDocumentPage || editMode),
  });

  // Which pages the viewer holds an unpublished draft on. One query for the
  // whole space — the index on page_drafts (author_id) exists for exactly this.
  const { data: drafts = [] } = useSpaceDrafts(spaceId, { enabled: !!spaceId });
  const hasDraftHere = drafts.some((d) => d.page_id === activeId);
  const orgId = me?.org_id ?? '';
  const { data: comments, refetch: refetchComments } = useComments(orgId, spaceId, 'page', activeId ?? '', { enabled: !!activeId && !!orgId });
  const createCommentMutation = useCreateComment(orgId, spaceId, 'page', activeId ?? '');
  const [newComment, setNewComment] = useState('');

  async function handleAddComment() {
    if (!newComment.trim()) return;
    await createCommentMutation.mutateAsync({ content: newComment.trim() });
    setNewComment('');
    refetchComments();
  }

  // Concurrent editing is handled by per-author drafts plus the `base_version`
  // guard: two people editing the same page hold two private drafts, and the
  // second to publish is told, by name, what changed. There is no lock to take.
  // The advisory page-lock API this flow never used was retired in S2.

  // Share affordances (P3, ADR-0008). The badge is space-read (any reader
  // sees which pages are shared, incl. cascade coverage). Managing shares and
  // cross-space moves are space-admin actions, gated on effective access.
  const { data: effAccess } = useEffectiveAccess(orgId, spaceId, undefined, { enabled: !!orgId && !!spaceId });
  const canManageShares = effAccess?.org_admin || effAccess?.role === 'space_admin';
  const { data: spaceShares } = useSpacePageShares(orgId, spaceId, { enabled: !!orgId && !!spaceId });
  const shareState = pageShareState(spaceShares, activeId ?? '', activePage?.path ?? '');
  const [shareOpen, setShareOpen] = useState(false);
  const [moveOpen, setMoveOpen] = useState(false);

  function startEdit() {
    if (!activePage) return;
    setEditMode(true);
  }

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Loading wiki...
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4">
        <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(error, 'The wiki could not be loaded.')}
        </p>
      </div>
    );
  }

  return (
    // The dashboards concept renders the document directly on the content
    // ground — no full card border. The actions bar keeps its own bottom
    // delineation; the title lives once, as the document H1 in the body.
    <div className="flex h-[calc(100vh-var(--topnav-height)-2rem)] flex-col overflow-hidden">
      {activePage ? (
        <>
          {/* Actions bar */}
          <div className="flex items-center gap-3 border-b border-[var(--color-border)] px-1 py-2">
            <span className="flex flex-1 items-center gap-2">
              <ShareBadge
                shared={shareState.shared}
                detail={shareState.viaCascade ? 'via a shared folder' : undefined}
              />
            </span>
            {hasDraftHere && !editMode && (
              <span
                data-testid="codex-unpublished-badge"
                className="flex items-center gap-1.5 rounded-[var(--radius-md)] bg-[color-mix(in_srgb,var(--module-codex)_20%,transparent)] px-2.5 py-1 text-[var(--text-xs)] text-[var(--module-codex)]"
              >
                <PenLine className="h-3 w-3" aria-hidden="true" />
                You have unpublished changes
              </span>
            )}
            {!editMode && (
              <>
                <button
                  type="button"
                  onClick={() => setRevisionsOpen(o => !o)}
                  className={cn(
                    'flex items-center gap-1.5 rounded-[var(--radius-md)] px-2.5 py-1.5 text-[var(--text-sm)] transition-colors',
                    revisionsOpen
                      ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                      : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
                  )}
                >
                  <History className="h-3.5 w-3.5" />
                  History
                </button>
                {canManageShares && (
                  <>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setShareOpen(true)}
                      disabled={!activePage}
                      data-testid="wiki-share-button"
                    >
                      <Share2 className="mr-1.5 h-3.5 w-3.5" />
                      Share
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setMoveOpen(true)}
                      disabled={!activePage}
                      data-testid="wiki-move-button"
                    >
                      <FolderInput className="mr-1.5 h-3.5 w-3.5" />
                      Move
                    </Button>
                  </>
                )}
                <Button variant="secondary" size="sm" onClick={startEdit} disabled={!activePage}>
                  <Edit className="mr-1.5 h-3.5 w-3.5" />
                  Edit
                </Button>
              </>
            )}
          </div>
          {shareOpen && activePage && (
            <ShareDialog
              orgId={orgId}
              entityType="page"
              entityId={activePage.id}
              entityLabel={activePage.title}
              onClose={() => setShareOpen(false)}
            />
          )}
          {moveOpen && activePage && (
            <MovePageDialog
              orgId={orgId}
              spaceId={spaceId}
              pageId={activePage.id}
              pageTitle={activePage.title}
              onClose={() => setMoveOpen(false)}
            />
          )}

          <div className="flex flex-1 overflow-hidden">
            {/* Page content */}
            <div className="flex-1 overflow-y-auto p-6">
              {editMode ? (
                /* Opening the editor is what converts a legacy markdown page:
                   GET …/document renders one from the page's markdown on the
                   way out and writes nothing until the author publishes
                   (migration 036 — conversion is per-page and on first edit,
                   never a bulk migration). */
                documentLoading ? (
                  <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
                    Opening the editor…
                  </p>
                ) : documentError ? (
                  <div className="space-y-3">
                    <p
                      data-testid="codex-document-error"
                      className="text-[var(--text-sm)] text-[var(--color-danger)]"
                    >
                      {friendlyErrorMessage(documentError, 'This page could not be opened for editing.')}
                    </p>
                    <Button variant="outline" onClick={() => setEditMode(false)}>
                      Back to the page
                    </Button>
                  </div>
                ) : pageDocument ? (
                  <PageEditor
                    key={activeId ?? ''}
                    spaceId={spaceId}
                    pageId={activeId ?? ''}
                    document={pageDocument}
                    pages={pages}
                    onClose={() => setEditMode(false)}
                    onReloadDocument={async () => (await refetchDocument()).data}
                  />
                ) : null
              ) : (
                <>
                  {/* Reading surface: a bounded measure — full-width prose
                      is unreadable past ~75ch. Scale per the dashboards
                      concept: 22px title, 1.78 body line-height. */}
                  <div className="mx-auto w-full max-w-[76ch]">
                  <div className="mb-6">
                    <h1
                      data-testid="wiki-page-title"
                      className="text-[22px] font-semibold leading-[1.3] tracking-[-.01em] text-[var(--color-text)]"
                    >
                      {activePage.title}
                    </h1>
                    <p className="mt-1.5 text-[var(--text-sm)] text-[var(--color-text-muted)]">
                      Last edited {(activePage.updated_at ?? '').slice(0, 10)}
                      {activePage.version != null && (
                        <span className="ml-2 text-[var(--text-xs)]">· v{activePage.version}</span>
                      )}
                    </p>
                  </div>

                  {/* The dual-format read path (migration 036).

                      A document-backed page renders through the shared
                      renderer — the editor's own extensions, not editable — so
                      macros and, above all, labelled preserved blocks look to
                      a reader exactly as they did to the author. A page that
                      has only ever held markdown (`doc` null) renders through
                      the markdown path below, byte for byte as it always has.
                      Both are tested. */}
                  {isDocumentPage && pageDocument ? (
                    <CodexDocRenderer
                      doc={pageDocument.doc}
                      spaceId={spaceId}
                      pageId={activePage.id}
                      pages={pages}
                    />
                  ) : isDocumentPage && documentLoading ? (
                    <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
                  ) : activePage.content ? (
                    <article
                      className={cn(
                        'prose prose-sm max-w-none leading-[1.78]',
                        'prose-headings:text-[var(--color-text)] prose-headings:font-semibold prose-headings:tracking-[-.01em]',
                        'prose-h1:text-[19px] prose-h2:text-[16px] prose-h3:text-[14px]',
                        'prose-p:text-[var(--color-text)]',
                        'prose-a:text-[var(--color-primary)] prose-strong:text-[var(--color-text)]',
                        'prose-code:font-[var(--font-mono)] prose-code:text-[var(--color-text)] prose-code:bg-[var(--color-input)] prose-code:rounded prose-code:px-1.5 prose-code:py-0.5',
                        'prose-pre:bg-[var(--color-input)] prose-pre:border prose-pre:border-[var(--color-border)]',
                        'prose-th:text-[var(--color-text-muted)] prose-td:text-[var(--color-text)]',
                        'prose-li:text-[var(--color-text)]',
                        'dark:prose-invert',
                      )}
                    >
                      <ReactMarkdown
                        rehypePlugins={[rehypeRaw]}
                        components={{
                          code({ className, children, ...props }: any) {
                            const match = /language-(\w+)/.exec(className || '');
                            const isBlock = !props.inline && match;
                            return isBlock ? (
                              <SyntaxHighlighter
                                style={oneDark}
                                language={match[1]}
                                PreTag="div"
                                customStyle={{ borderRadius: '0.5rem', margin: '0.75rem 0', fontSize: '0.875rem' }}
                              >
                                {String(children).replace(/\n$/, '')}
                              </SyntaxHighlighter>
                            ) : (
                              <code className={className} {...props}>{children}</code>
                            );
                          },
                        }}
                      >
                        {activePage.content}
                      </ReactMarkdown>
                    </article>
                  ) : (
                    <div className="flex min-h-[300px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
                      <p className="text-[var(--text-lg)] text-[var(--color-text-muted)]">
                        This page is empty. Click Edit to start writing.
                      </p>
                    </div>
                  )}

                  {/* Comments */}
                  <div className="mt-10 border-t border-[var(--color-border)] pt-8">
                    <h3 className="mb-4 text-[var(--text-sm)] font-semibold text-[var(--color-text)]">Comments</h3>
                    <div className="mb-6 space-y-4">
                      {(comments ?? []).length === 0 ? (
                        <p className="text-[var(--text-sm)] italic text-[var(--color-text-muted)]">No comments yet.</p>
                      ) : (comments ?? []).map((comment) => (
                        <div key={comment.id} className="flex gap-3">
                          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[var(--color-primary)] text-[var(--text-sm)] font-medium text-white">
                            {comment.author_name?.[0]?.toUpperCase() ?? '?'}
                          </div>
                          <div className="min-w-0 flex-1">
                            <div className="mb-1 flex items-center gap-2">
                              <span className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                                {comment.author_name ?? 'Unknown'}
                              </span>
                              <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                                {new Date(comment.created_at).toLocaleDateString()}
                              </span>
                            </div>
                            <p className="whitespace-pre-wrap text-[var(--text-sm)] text-[var(--color-text-muted)]">
                              {comment.content ?? comment.body}
                            </p>
                          </div>
                        </div>
                      ))}
                    </div>
                    <div className="flex gap-3">
                      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[var(--color-primary)] text-[var(--text-sm)] font-medium text-white">
                        {me?.display_name?.[0]?.toUpperCase() ?? 'U'}
                      </div>
                      <div className="flex-1">
                        <textarea
                          value={newComment}
                          onChange={(e) => setNewComment(e.target.value)}
                          placeholder="Add a comment..."
                          className={cn(
                            'w-full resize-none rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
                            'placeholder:text-[var(--color-text-muted)] focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]',
                          )}
                          rows={3}
                        />
                        <button
                          onClick={handleAddComment}
                          disabled={!newComment.trim() || createCommentMutation.isPending}
                          className="mt-2 rounded-[var(--radius-lg)] bg-[var(--color-primary)] px-4 py-1.5 text-[var(--text-sm)] font-medium text-white transition-colors hover:bg-[var(--color-primary-hover)] disabled:opacity-50"
                        >
                          {createCommentMutation.isPending ? 'Posting…' : 'Comment'}
                        </button>
                      </div>
                    </div>
                  </div>
                  </div>
                </>
              )}
            </div>

            {/* Revisions panel */}
            {revisionsOpen && activeId && (
              <div className="w-64 shrink-0 overflow-hidden">
                <RevisionsPanel
                  spaceId={spaceId}
                  pageId={activeId}
                  currentVersion={activePage.version ?? 0}
                  onClose={() => setRevisionsOpen(false)}
                />
              </div>
            )}
          </div>
        </>
      ) : (
        <div className="flex flex-1 items-center justify-center text-[var(--color-text-muted)]">
          {pages.length > 0 ? 'Select a page from the sidebar.' : 'No pages yet. Create one from the sidebar to get started.'}
        </div>
      )}
    </div>
  );
}
