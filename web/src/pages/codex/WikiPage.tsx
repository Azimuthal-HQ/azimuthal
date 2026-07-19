import { useState, useEffect, useRef, type ReactElement } from 'react';
import { useParams } from 'react-router-dom';
import { FileText, Edit, Plus, AlertCircle, Search, History, X, ChevronRight, Lock } from 'lucide-react';
import { MarkdownEditor } from '../../components/ui/MarkdownEditor';
import ReactMarkdown from 'react-markdown';
import rehypeRaw from 'rehype-raw';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import { cn } from '../../lib/utils';
import {
  useWikiPages,
  useWikiPage,
  useCreateWikiPage,
  useUpdateWikiPage,
  useWikiSearch,
  useWikiRevisions,
  usePageLock,
  useAcquirePageLock,
  useReleasePageLock,
  useMe,
  useComments,
  useCreateComment,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Sidebar
// ---------------------------------------------------------------------------

interface SidebarProps {
  pages: { id: string; title: string; parent_id?: string | null }[];
  activeId: string | null;
  onSelect: (id: string) => void;
  spaceId: string;
}

function WikiSidebar({ pages, activeId, onSelect, spaceId }: SidebarProps) {
  const [search, setSearch] = useState('');
  const [searchDebounced, setSearchDebounced] = useState('');
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const { data: searchResults } = useWikiSearch(spaceId, searchDebounced, { enabled: searchDebounced.length > 1 });

  function handleSearchChange(v: string) {
    setSearch(v);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => setSearchDebounced(v), 300);
  }

  const showing = search.length > 1 && searchResults ? searchResults : pages;

  return (
    <div className="flex h-full flex-col gap-2">
      <div className="relative">
        <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--color-text-muted)]" />
        <input
          value={search}
          onChange={e => handleSearchChange(e.target.value)}
          placeholder="Search pages…"
          className="w-full rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface-hover)] py-1.5 pl-8 pr-3 text-[var(--text-sm)] text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] focus:outline-none focus:ring-1 focus:ring-[var(--color-primary)]"
        />
      </div>
      <div className="flex-1 overflow-y-auto space-y-0.5">
        {showing.length === 0 ? (
          <p className="px-2 py-3 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {search.length > 1 ? 'No results.' : 'No pages yet.'}
          </p>
        ) : search.length > 1 ? (
          // Search results stay flat — hierarchy only applies to the tree view.
          showing.map(p => (
            <SidebarPageButton key={p.id} page={p} depth={0} activeId={activeId} onSelect={onSelect} />
          ))
        ) : (
          <SidebarTree pages={showing} activeId={activeId} onSelect={onSelect} />
        )}
      </div>
    </div>
  );
}

interface SidebarPageButtonProps {
  page: { id: string; title: string };
  depth: number;
  activeId: string | null;
  onSelect: (id: string) => void;
}

function SidebarPageButton({ page, depth, activeId, onSelect }: SidebarPageButtonProps) {
  return (
    <button
      type="button"
      onClick={() => onSelect(page.id)}
      style={{ paddingLeft: `${8 + depth * 16}px` }}
      className={cn(
        'flex w-full items-center gap-2 rounded-[var(--radius-md)] px-2 py-1.5 text-left text-[var(--text-sm)] transition-colors',
        activeId === page.id
          ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)] font-medium'
          : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
      )}
    >
      <FileText className="h-3.5 w-3.5 shrink-0" />
      <span className="truncate">{page.title}</span>
    </button>
  );
}

// SidebarTree renders the real page hierarchy: children nest (indented)
// under their parent via parent_id — never a flat list.
function SidebarTree({ pages, activeId, onSelect }: Omit<SidebarProps, 'spaceId'>) {
  const byParent = new Map<string, typeof pages>();
  const ids = new Set(pages.map(p => p.id));
  for (const p of pages) {
    // Pages whose parent isn't in this space's list render at the root.
    const key = p.parent_id && ids.has(p.parent_id) ? p.parent_id : '';
    const list = byParent.get(key) ?? [];
    list.push(p);
    byParent.set(key, list);
  }

  function renderLevel(parentKey: string, depth: number): ReactElement[] {
    return (byParent.get(parentKey) ?? []).map(p => (
      <div key={p.id} data-tree-depth={depth}>
        <SidebarPageButton page={p} depth={depth} activeId={activeId} onSelect={onSelect} />
        {renderLevel(p.id, depth + 1)}
      </div>
    ));
  }

  return <>{renderLevel('', 0)}</>;
}

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
                      <span className="ml-2 rounded-full bg-[var(--color-primary)] px-1.5 py-0.5 text-[var(--text-xs)] text-white">current</span>
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

/** Two-panel wiki page with sidebar list and markdown content. */
export function WikiPage() {
  const { spaceId = '', pageId } = useParams<{ spaceId: string; pageId: string }>();
  const { data: pages = [], isLoading, error } = useWikiPages(spaceId);
  const createMutation = useCreateWikiPage(spaceId);

  const [activeId, setActiveId] = useState<string | null>(pageId ?? null);
  const [revisionsOpen, setRevisionsOpen] = useState(false);

  // The Codex sidebar tree navigates to pages/:pageId; follow the URL when
  // it changes so sidebar selection and page content stay in sync.
  useEffect(() => {
    if (pageId) setActiveId(pageId);
  }, [pageId]);

  // Inline edit state
  const [editMode, setEditMode] = useState(false);
  const [editTitle, setEditTitle] = useState('');
  const [editContent, setEditContent] = useState('');
  const editContentRef = useRef('');
  const [editError, setEditError] = useState<string | null>(null);

  // New Page modal state
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');
  const [formParentId, setFormParentId] = useState('');

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
  const updateMutation = useUpdateWikiPage(spaceId, activeId ?? '');
  const { data: me } = useMe();
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

  const { data: pageLock } = usePageLock(spaceId, activeId ?? '', { enabled: !!activeId });
  const acquireLock = useAcquirePageLock(spaceId, activeId ?? '');
  const releaseLock = useReleasePageLock(spaceId, activeId ?? '');

  const lockedByOther = pageLock && me && pageLock.user_id !== me.id;

  async function startEdit() {
    if (!activePage) return;
    try {
      await acquireLock.mutateAsync();
    } catch {
      // If lock acquisition fails, lock banner will show — don't enter edit mode
      return;
    }
    setEditTitle(activePage.title);
    const initial = activePage.content ?? '';
    setEditContent(initial);
    editContentRef.current = initial;
    setEditError(null);
    setEditMode(true);
  }

  function cancelEdit() {
    setEditMode(false);
    setEditError(null);
    releaseLock.mutate();
  }

  async function handleSave() {
    if (!activePage) return;
    setEditError(null);
    try {
      await updateMutation.mutateAsync({
        title: editTitle.trim() || activePage.title,
        content: editContentRef.current,
        expected_version: activePage.version,
      });
      setEditMode(false);
      releaseLock.mutate();
    } catch (err) {
      setEditError(err instanceof Error ? err.message : 'Save failed');
    }
  }

  async function handleCreatePage() {
    const title = formTitle.trim();
    if (!title) return;
    try {
      const created = await createMutation.mutateAsync({
        title,
        content: '',
        parent_id: formParentId || null,
      });
      setActiveId(created.id);
      setDialogOpen(false);
      setFormTitle('');
      setFormParentId('');
    } catch (err) {
      console.error('[WikiPage] Create page error:', err);
    }
  }

  function handleSelectPage(id: string) {
    setActiveId(id);
    setEditMode(false);
    setRevisionsOpen(false);
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
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
        <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          Failed to load wiki: {error.message}
        </p>
      </div>
    );
  }

  return (
    <div className="flex h-[calc(100vh-var(--topnav-height)-2rem)] gap-0 overflow-hidden rounded-[var(--radius-lg)] border border-[var(--color-border)]">
      {/* ── Left sidebar ── */}
      <div className="flex w-56 shrink-0 flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)] p-3">
        <div className="mb-3 flex items-center justify-between">
          <span className="text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">Pages</span>
          <button
            type="button"
            onClick={() => setDialogOpen(true)}
            className="rounded p-0.5 text-[var(--color-text-muted)] hover:text-[var(--color-primary)] transition-colors"
            title="New page"
            aria-label="New page"
          >
            <Plus className="h-4 w-4" />
          </button>
        </div>
        <WikiSidebar pages={pages} activeId={activeId} onSelect={handleSelectPage} spaceId={spaceId} />
      </div>

      {/* ── Main content ── */}
      <div className="flex flex-1 flex-col min-w-0 overflow-hidden">
        {activePage ? (
          <>
            {/* Top bar */}
            <div className="flex items-center gap-3 border-b border-[var(--color-border)] bg-[var(--color-surface)] px-5 py-2.5">
              <span className="flex-1 truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                {activePage.title}
              </span>
              {lockedByOther && (
                <span className="flex items-center gap-1.5 rounded-[var(--radius-md)] bg-[var(--color-warning)]/15 px-2.5 py-1 text-[var(--text-xs)] text-[var(--color-warning)]">
                  <Lock className="h-3 w-3" />
                  {pageLock!.user_name} is editing
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
                  <Button variant="secondary" size="sm" onClick={startEdit} disabled={!activePage || !!lockedByOther}>
                    <Edit className="mr-1.5 h-3.5 w-3.5" />
                    Edit
                  </Button>
                </>
              )}
            </div>

            <div className="flex flex-1 overflow-hidden">
              {/* Page content */}
              <div className="flex-1 overflow-y-auto p-6">
                {editMode ? (
                  <div className="space-y-3">
                    <Input
                      value={editTitle}
                      onChange={(e) => setEditTitle(e.target.value)}
                      className="text-[var(--text-xl)] font-bold"
                      placeholder="Page title"
                    />
                    <MarkdownEditor
                      key={activeId ?? ''}
                      value={editContent}
                      onChange={(md) => {
                        editContentRef.current = md;
                        setEditContent(md);
                      }}
                      disabled={updateMutation.isPending}
                    />
                    {editError && (
                      <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{editError}</p>
                    )}
                    <div className="flex gap-2">
                      <Button onClick={handleSave} disabled={updateMutation.isPending}>
                        {updateMutation.isPending ? 'Saving…' : 'Save'}
                      </Button>
                      <Button variant="outline" onClick={cancelEdit} disabled={updateMutation.isPending}>
                        Cancel
                      </Button>
                    </div>
                  </div>
                ) : (
                  <>
                    <div className="mb-4">
                      <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
                        {activePage.title}
                      </h1>
                      <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
                        Last edited {(activePage.updated_at ?? '').slice(0, 10)}
                        {activePage.version != null && (
                          <span className="ml-2 text-[var(--text-xs)]">· v{activePage.version}</span>
                        )}
                      </p>
                    </div>

                    {activePage.content ? (
                      <article
                        className={cn(
                          'prose max-w-none',
                          'prose-headings:text-[var(--color-text)] prose-p:text-[var(--color-text)]',
                          'prose-a:text-[var(--color-primary)] prose-strong:text-[var(--color-text)]',
                          'prose-code:text-[var(--color-primary)] prose-code:bg-[var(--color-surface-hover)] prose-code:rounded prose-code:px-1',
                          'prose-pre:bg-[var(--color-surface)] prose-pre:border prose-pre:border-[var(--color-border)]',
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
                    <div className="mt-8 border-t border-[var(--color-border)] pt-6">
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
                              'w-full resize-none rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
                              'placeholder:text-[var(--color-text-muted)] focus:outline-none focus:ring-1 focus:ring-[var(--color-primary)]',
                            )}
                            rows={3}
                          />
                          <button
                            onClick={handleAddComment}
                            disabled={!newComment.trim() || createCommentMutation.isPending}
                            className="mt-2 rounded-[var(--radius-md)] bg-[var(--color-primary)] px-4 py-1.5 text-[var(--text-sm)] font-medium text-white transition-colors hover:opacity-90 disabled:opacity-50"
                          >
                            {createCommentMutation.isPending ? 'Posting…' : 'Comment'}
                          </button>
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
            {pages.length > 0 ? 'Select a page from the sidebar.' : 'No pages yet. Create one to get started.'}
          </div>
        )}
      </div>

      {/* New Page dialog */}
      <Dialog open={dialogOpen} onOpenChange={(open) => { setDialogOpen(open); if (!open) setFormTitle(''); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New Page</DialogTitle>
            <DialogDescription>Create a new wiki page.</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <label htmlFor="page-title" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Title</label>
              <Input
                id="page-title"
                placeholder="e.g. Getting Started Guide"
                value={formTitle}
                onChange={(e) => setFormTitle(e.target.value)}
                autoFocus
              />
            </div>
            <div className="space-y-2">
              <label htmlFor="page-parent" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Parent page</label>
              <select
                id="page-parent"
                value={formParentId}
                onChange={(e) => setFormParentId(e.target.value)}
                className="w-full rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] focus:outline-none focus:ring-1 focus:ring-[var(--color-primary)]"
              >
                <option value="">None (top level)</option>
                {pages.map((p) => (
                  <option key={p.id} value={p.id}>{p.title}</option>
                ))}
              </select>
            </div>
            {createMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{createMutation.error.message}</p>
            )}
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">Cancel</Button>
            </DialogClose>
            <Button onClick={handleCreatePage} disabled={createMutation.isPending || !formTitle.trim()}>
              {createMutation.isPending ? 'Creating...' : 'Create Page'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
