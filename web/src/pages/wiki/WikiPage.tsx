import { useState, useMemo, useRef } from 'react';
import { useParams } from 'react-router-dom';
import { FileText, Edit, Plus, AlertCircle, ChevronDown, BookOpen } from 'lucide-react';
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
import { useWikiPages, useWikiPage, useCreateWikiPage, useUpdateWikiPage } from '../../lib/api';

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

/** Two-panel wiki page with sidebar list and markdown content. */
export function WikiPage() {
  const { spaceId = '', pageId } = useParams<{ spaceId: string; pageId: string }>();
  const { data: pages, isLoading, error } = useWikiPages(spaceId);
  const createMutation = useCreateWikiPage(spaceId);

  const [activeId, setActiveId] = useState<string | null>(pageId ?? null);

  // Inline edit state
  const [editMode, setEditMode] = useState(false);
  const [editTitle, setEditTitle] = useState('');
  const [editContent, setEditContent] = useState('');
  // Ref always holds the latest content from the editor — avoids stale closure on save
  const editContentRef = useRef('');
  const [editError, setEditError] = useState<string | null>(null);

  // Page picker dropdown
  const [pickerOpen, setPickerOpen] = useState(false);

  // New Page modal state
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');

  // If we have pages but no active selection, select the first one
  useMemo(() => {
    if (pages && pages.length > 0 && !activeId) {
      setActiveId(pages[0].id);
    }
  }, [pages, activeId]);

  // P2.1: fetch full page via dedicated endpoint — list response omits content
  const { data: activePage } = useWikiPage(spaceId, activeId ?? '', {
    enabled: !!activeId,
  });
  const updateMutation = useUpdateWikiPage(spaceId, activeId ?? '');

  function startEdit() {
    if (!activePage) return;
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
    } catch (err) {
      setEditError(err instanceof Error ? err.message : 'Save failed');
    }
  }

  function resetForm() {
    setFormTitle('');
  }

  async function handleCreatePage() {
    const title = formTitle.trim();
    if (!title) return;

    const body = {
      title,
      content: '',
    };
    console.log('[WikiPage] Creating page:', JSON.stringify(body));

    try {
      const created = await createMutation.mutateAsync(body);
      setActiveId(created.id);
      setDialogOpen(false);
      resetForm();
    } catch (err) {
      console.error('[WikiPage] Create page error:', err);
    }
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
    <div className="flex flex-col gap-4">

      {/* ── Page nav bar ── */}
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2.5">
        {/* Page picker */}
        <div className="relative">
          <button
            type="button"
            onClick={() => setPickerOpen(o => !o)}
            className="flex items-center gap-2 rounded-[var(--radius-md)] px-3 py-1.5 text-[var(--text-sm)] font-medium text-[var(--color-text)] transition-colors hover:bg-[var(--color-surface-hover)]"
          >
            <BookOpen className="h-4 w-4 text-[var(--color-primary)]" />
            <span className="max-w-[200px] truncate">
              {activePage?.title ?? 'Select a page'}
            </span>
            <ChevronDown className={cn('h-3.5 w-3.5 text-[var(--color-text-muted)] transition-transform', pickerOpen && 'rotate-180')} />
          </button>

          {pickerOpen && (
            <div className="absolute left-0 top-full z-50 mt-1 w-64 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] p-1.5 shadow-xl">
              {pages && pages.length > 0 ? pages.map((page) => (
                <button
                  key={page.id}
                  type="button"
                  onClick={() => { setActiveId(page.id); setPickerOpen(false); }}
                  className={cn(
                    'flex w-full items-center gap-2 rounded-[var(--radius-md)] px-3 py-2 text-left text-[var(--text-sm)] transition-colors',
                    activeId === page.id
                      ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)] font-medium'
                      : 'text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
                  )}
                >
                  <FileText className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
                  <span className="truncate">{page.title}</span>
                </button>
              )) : (
                <p className="px-3 py-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">No pages yet.</p>
              )}
            </div>
          )}
        </div>

        {/* Page count pill */}
        {pages && pages.length > 0 && (
          <span className="rounded-full bg-[var(--color-surface-hover)] px-2 py-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
            {pages.length} {pages.length === 1 ? 'page' : 'pages'}
          </span>
        )}

        <div className="flex-1" />

        {/* New page */}
        <Button variant="outline" size="sm" onClick={() => setDialogOpen(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          New Page
        </Button>
      </div>

      {/* ── Content ── */}
      <main className="min-w-0">
        {activePage ? (
          <div className="space-y-4">
            {editMode ? (
              /* ── Inline editor ── */
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
              /* ── Read view ── */
              <>
                <div className="flex items-start justify-between">
                  <div>
                    <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
                      {activePage.title}
                    </h1>
                    <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
                      Last edited {(activePage.updated_at ?? '').slice(0, 10)}
                    </p>
                  </div>
                  <Button variant="secondary" size="sm" onClick={startEdit}>
                    <Edit className="mr-2 h-4 w-4" />
                    Edit
                  </Button>
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
                  <div className="flex min-h-[300px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)] bg-[var(--color-surface)]">
                    <p className="text-[var(--text-lg)] text-[var(--color-text-muted)]">
                      This page is empty. Click Edit to start writing.
                    </p>
                  </div>
                )}
              </>
            )}
          </div>
        ) : (
          <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
            {pages && pages.length > 0 ? 'Select a page from the sidebar.' : 'No pages yet. Create one to get started.'}
          </div>
        )}
      </main>

      {/* New Page dialog */}
      <Dialog open={dialogOpen} onOpenChange={(open) => { setDialogOpen(open); if (!open) resetForm(); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New Page</DialogTitle>
            <DialogDescription>
              Create a new wiki page.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <label htmlFor="page-title" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Title
              </label>
              <Input
                id="page-title"
                placeholder="e.g. Getting Started Guide"
                value={formTitle}
                onChange={(e) => setFormTitle(e.target.value)}
                autoFocus
              />
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
