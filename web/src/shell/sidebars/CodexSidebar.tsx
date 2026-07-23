import { useEffect, useMemo, useRef, useState } from 'react';
import { NavLink, useNavigate, useSearchParams } from 'react-router-dom';
import { Clock, FileText, PenLine, Plus, Search, Star } from 'lucide-react';
import { cn } from '../../lib/utils';
import {
  friendlyErrorMessage,
  useCreateWikiPage,
  useWikiPages,
  useWikiSearch,
  type Space,
  type WikiPage,
} from '../../lib/api';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import { spacePath } from '../modules';
import { SidebarChrome, SidebarNavItem, useSidebarIsCollapsed } from './SidebarChrome';
import { SpacePicker } from '../SpacePicker';
import { useSidebarCollapsed } from '../hooks/useSidebarCollapsed';

interface TreeNode {
  page: WikiPage;
  depth: number;
}

/** flattenTree orders pages parent-first with depth, tolerating orphaned parents. */
function flattenTree(pages: WikiPage[]): TreeNode[] {
  const byParent = new Map<string | null, WikiPage[]>();
  const ids = new Set(pages.map((p) => p.id));
  for (const page of pages) {
    const parent = page.parent_id !== null && ids.has(page.parent_id) ? page.parent_id : null;
    const bucket = byParent.get(parent);
    if (bucket) bucket.push(page);
    else byParent.set(parent, [page]);
  }
  const out: TreeNode[] = [];
  const walk = (parent: string | null, depth: number) => {
    for (const page of byParent.get(parent) ?? []) {
      out.push({ page, depth });
      walk(page.id, depth + 1);
    }
  };
  walk(null, 0);
  return out;
}

/**
 * CodexSidebar is the wiki module's ONE navigation panel (ADR-0005 point 6):
 * a fixed region — space picker, the scoped page search, Recent, Starred,
 * Drafts — above the page tree in its own scroll container, so a large wiki
 * can never push the fixed items out of reach. The page search, tree, and
 * create-page affordance live here and nowhere else; the pre-P1 in-content
 * page panel that duplicated all three is deleted.
 */
export function CodexSidebar({ space, spaceId }: { space: Space | undefined; spaceId: string }) {
  const [collapsed] = useSidebarCollapsed();
  const [search, setSearch] = useState('');
  const [searchDebounced, setSearchDebounced] = useState('');
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  function handleSearchChange(v: string) {
    setSearch(v);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => setSearchDebounced(v), 300);
  }

  return (
    <SidebarChrome
      testId="space-sidebar"
      module="codex"
      header={<SpacePicker module="codex" currentSpace={space} collapsed={collapsed} />}
      settingsTo={spacePath('codex', spaceId, 'settings')}
      scrollableNav={false}
    >
      {/* Fixed region: never scrolls away */}
      <div className="shrink-0">
        <WikiSearchInput value={search} onChange={handleSearchChange} />
        <nav className="mt-[var(--space-2)] flex flex-col gap-[2px]">
          <SidebarNavItem to={spacePath('codex', spaceId, 'recent')} icon={Clock} label="Recent" />
          <SidebarNavItem to={spacePath('codex', spaceId, 'starred')} icon={Star} label="Starred" />
          <SidebarNavItem to={spacePath('codex', spaceId, 'drafts')} icon={PenLine} label="Drafts" />
        </nav>
      </div>

      <PageTree spaceId={spaceId} query={search.trim()} queryDebounced={searchDebounced.trim()} />
    </SidebarChrome>
  );
}

/**
 * The scoped page search ("Search this wiki"), relocated from the deleted
 * in-content panel. While a query is active the tree zone shows flat matches
 * instead of the hierarchy — same behaviour, new home. Hidden when the
 * sidebar collapses to the icon rail, like the tree itself.
 */
function WikiSearchInput({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const collapsed = useSidebarIsCollapsed();
  if (collapsed) return null;
  return (
    <div className="relative">
      <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--color-text-muted)]" />
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="Search this wiki"
        aria-label="Search this wiki"
        data-testid="codex-page-search"
        className={cn(
          'h-8 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
          'bg-[var(--color-input)] pl-8 pr-2 text-[var(--text-xs)] text-[var(--color-text)]',
          'placeholder:text-[var(--color-text-muted)]',
          'focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]',
        )}
      />
    </div>
  );
}

function PageTree({
  spaceId,
  query,
  queryDebounced,
}: {
  spaceId: string;
  query: string;
  queryDebounced: string;
}) {
  const collapsed = useSidebarIsCollapsed();
  const navigate = useNavigate();
  const pagesQuery = useWikiPages(spaceId);
  const pages = useMemo(() => pagesQuery.data ?? [], [pagesQuery.data]);
  const nodes = useMemo(() => flattenTree(pages), [pages]);
  const searching = query.length > 1;
  const { data: searchResults } = useWikiSearch(spaceId, queryDebounced, {
    enabled: queryDebounced.length > 1,
  });

  // Create-page dialog: the + on this zone's header is the one create
  // affordance (the deleted panel's + and dialog moved here with it).
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');
  const [formParentId, setFormParentId] = useState('');
  const createMutation = useCreateWikiPage(spaceId);

  // The top bar's contextual Create lands on the codex space index as
  // ?create=page; the sidebar is always mounted there, so open the dialog.
  const [searchParams, setSearchParams] = useSearchParams();
  useEffect(() => {
    if (searchParams.get('create') === 'page') {
      setDialogOpen(true);
      setSearchParams({}, { replace: true });
    }
  }, [searchParams, setSearchParams]);

  async function handleCreatePage() {
    const title = formTitle.trim();
    if (!title) return;
    try {
      const created = await createMutation.mutateAsync({
        title,
        content: '',
        parent_id: formParentId || null,
      });
      setDialogOpen(false);
      setFormTitle('');
      setFormParentId('');
      navigate(spacePath('codex', spaceId, `pages/${created.id}`));
    } catch {
      // Surfaced in the dialog through friendlyErrorMessage.
    }
  }

  if (collapsed) return null;

  // Search results stay flat — hierarchy only applies to the tree view.
  const rows: TreeNode[] = searching
    ? (searchResults ?? []).map((page) => ({ page, depth: 0 }))
    : nodes;
  const emptyText = searching
    ? searchResults
      ? 'No results.'
      : 'Searching…'
    : 'No pages yet.';

  return (
    <>
      <div className="mt-[var(--space-3)] flex shrink-0 items-center border-t border-[var(--color-border)] px-[var(--space-3)] pb-[var(--space-1)] pt-[var(--space-3)]">
        <p className="text-[10px] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
          Pages
        </p>
        <button
          type="button"
          onClick={() => setDialogOpen(true)}
          aria-label="New page"
          title="New page"
          className="ml-auto rounded p-0.5 text-[var(--color-text-muted)] transition-colors hover:text-[var(--color-primary)]"
        >
          <Plus className="h-3.5 w-3.5" />
        </button>
      </div>
      <div data-testid="codex-page-tree" className="min-h-0 flex-1 overflow-y-auto pr-[var(--space-1)]">
        {rows.length === 0 ? (
          <p className="px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-xs)] text-[var(--color-text-muted)]">
            {emptyText}
          </p>
        ) : (
          rows.map(({ page, depth }) => (
            // NavLink, not Link: this component renders from the LAYOUT
            // route, whose params never include the child :pageId — a
            // useParams comparison here is always false. NavLink derives
            // active from the location and sets aria-current="page".
            <NavLink
              key={page.id}
              to={spacePath('codex', spaceId, `pages/${page.id}`)}
              end
              data-tree-depth={depth}
              title={page.title}
              style={{ paddingLeft: `calc(var(--space-3) + ${depth} * var(--space-4))` }}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-[var(--space-2)] rounded-[var(--radius-md)] py-[6px] pr-[var(--space-2)]',
                  'text-[13px] transition-colors duration-150',
                  isActive
                    ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                    : 'text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
                )
              }
            >
              <FileText className="h-3.5 w-3.5 shrink-0" />
              <span className="truncate">{page.title}</span>
            </NavLink>
          ))
        )}
      </div>

      <Dialog open={dialogOpen} onOpenChange={(open) => { setDialogOpen(open); if (!open) setFormTitle(''); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New Page</DialogTitle>
            <DialogDescription>Create a new wiki page.</DialogDescription>
          </DialogHeader>
          <div className="py-2">
            <Field>
              <FieldLabel htmlFor="page-title">Title</FieldLabel>
              <Input
                id="page-title"
                placeholder="e.g. Getting Started Guide"
                value={formTitle}
                onChange={(e) => setFormTitle(e.target.value)}
                autoFocus
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="page-parent" optional>Parent page</FieldLabel>
              <select
                id="page-parent"
                value={formParentId}
                onChange={(e) => setFormParentId(e.target.value)}
                className="w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]"
              >
                <option value="">None (top level)</option>
                {pages.map((p) => (
                  <option key={p.id} value={p.id}>{p.title}</option>
                ))}
              </select>
            </Field>
            {createMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {friendlyErrorMessage(createMutation.error, 'The page could not be created.')}
              </p>
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
    </>
  );
}
