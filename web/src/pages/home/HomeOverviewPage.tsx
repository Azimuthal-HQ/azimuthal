import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { Plus, BarChart3, Columns3, LayoutGrid, AlertCircle, Compass, LifeBuoy } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '../../components/ui/card';
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
import { useSpaces, useCreateSpace, type Space, type SpaceType } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { MODULE_KEYS, MODULES, spacePath } from '../../shell/modules';
import { ModuleChip } from '../../shell/ModuleChip';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function linkForSpace(space: Space): string {
  return spacePath(space.type, space.id, MODULES[space.type].defaultSubpath);
}

const MODULE_TAGLINE: Record<SpaceType, string> = {
  beacon: 'track and resolve customer issues',
  codex: "document your team's knowledge",
  vector: 'plan and track work with sprints and backlogs',
};

function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
}

function deriveKey(name: string): string {
  const first = name.trim().split(/\s+/)[0] ?? '';
  const upper = first.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8);
  return upper || 'SPACE';
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** HomeOverviewPage is the post-login landing: spaces, quick stats, and creation. */
export function HomeOverviewPage() {
  const navigate = useNavigate();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const { data: rawSpaces, isLoading, error } = useSpaces(orgId);
  const spaces = rawSpaces ? (Array.isArray(rawSpaces) ? rawSpaces : [rawSpaces]) : undefined;

  const [searchParams, setSearchParams] = useSearchParams();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formName, setFormName] = useState('');
  const [formType, setFormType] = useState<SpaceType>('beacon');
  const [formDescription, setFormDescription] = useState('');
  const [formKey, setFormKey] = useState('');
  const createSpaceMutation = useCreateSpace(orgId);

  // The top bar's Create button lands here as /?create=space.
  useEffect(() => {
    if (searchParams.get('create') === 'space') {
      setDialogOpen(true);
      setSearchParams({}, { replace: true });
    }
  }, [searchParams, setSearchParams]);

  function resetForm() {
    setFormName('');
    setFormType('beacon');
    setFormDescription('');
    setFormKey('');
  }

  const keyError = formType === 'beacon' && formKey && !/^[A-Z0-9]{1,10}$/.test(formKey)
    ? 'Abbreviation must be 1–10 uppercase letters or digits'
    : null;

  const keyMissing = formType === 'beacon' && !formKey.trim();

  async function handleCreate() {
    const name = formName.trim();
    if (!name) return;
    if (keyError || keyMissing) return;

    const slug = slugify(name);
    const key = formType === 'beacon' ? formKey : deriveKey(name);

    try {
      const created = await createSpaceMutation.mutateAsync({
        name,
        slug,
        key,
        type: formType,
        description: formDescription.trim() || undefined,
      });
      setDialogOpen(false);
      resetForm();
      navigate(linkForSpace(created));
    } catch {
      // Error is handled by mutation state
    }
  }

  return (
    <div className="space-y-8">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
            Welcome back
          </h1>
          <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            Here is an overview of your spaces and activity.
          </p>
        </div>
        <Button onClick={() => setDialogOpen(true)}>
          <Plus className="mr-2 h-4 w-4" />
          Create Space
        </Button>
      </div>

      {/* Loading state */}
      {isLoading && (
        <div className="flex h-32 items-center justify-center text-[var(--color-text-muted)]">
          Loading spaces...
        </div>
      )}

      {/* Error state */}
      {error && (
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            Failed to load spaces: {error.message}
          </p>
        </div>
      )}

      {/* Empty state onboarding */}
      {spaces && spaces.length === 0 && !isLoading && (
        <div className="flex flex-col items-center justify-center py-16 text-center">
          <div className="mb-6 flex h-24 w-24 items-center justify-center rounded-full bg-[var(--color-primary-muted)]">
            <Compass className="h-12 w-12 text-[var(--color-primary)]" />
          </div>
          <h2 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
            Welcome to Azimuthal
          </h2>
          <p className="mt-2 max-w-md text-[var(--text-sm)] text-[var(--color-text-muted)]">
            You don't have any spaces yet. Create your first space to get started.
          </p>
          <Button
            size="lg"
            className="mt-6"
            onClick={() => setDialogOpen(true)}
          >
            <Plus className="mr-2 h-5 w-5" />
            Create your first space
          </Button>
          <div className="mt-8 max-w-md text-left">
            <p className="mb-3 text-[var(--text-sm)] font-medium text-[var(--color-text-muted)]">
              Not sure where to start?
            </p>
            <ul className="space-y-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
              {MODULE_KEYS.map((key) => {
                const def = MODULES[key];
                return (
                  <li key={key} className="flex items-start gap-2">
                    <def.icon className="mt-0.5 h-4 w-4 shrink-0 text-[var(--color-primary)]" />
                    <span>
                      <strong className="text-[var(--color-text)]">{def.name}</strong> &mdash; {MODULE_TAGLINE[key]}
                    </span>
                  </li>
                );
              })}
            </ul>
          </div>
        </div>
      )}

      {/* Quick stats — only shown when spaces exist */}
      {spaces && spaces.length > 0 && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <StatCard icon={LayoutGrid} label="Spaces" value={spaces.length} />
          <StatCard icon={LifeBuoy} label="Beacon spaces" value={spaces.filter(s => s.type === 'beacon').length} />
          <StatCard icon={Columns3} label="Vector spaces" value={spaces.filter(s => s.type === 'vector').length} />
        </div>
      )}

      {/* Space cards */}
      {spaces && spaces.length > 0 && (
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
          {spaces.map((space) => {
            const Icon = MODULES[space.type].icon;
            return (
              <Link key={space.id} to={linkForSpace(space)} className="group">
                <Card className="h-full transition-shadow group-hover:shadow-[var(--shadow-md)]">
                  <CardHeader>
                    <div className="flex items-center gap-3">
                      <div className="flex h-10 w-10 items-center justify-center rounded-[var(--radius-md)] bg-[var(--color-primary-muted)]">
                        <Icon className="h-5 w-5 text-[var(--color-primary)]" />
                      </div>
                      <div className="min-w-0 flex-1">
                        <CardTitle className="truncate">{space.name}</CardTitle>
                        <div className="mt-1 flex items-center gap-2">
                          <ModuleChip module={space.type} />
                        </div>
                      </div>
                    </div>
                  </CardHeader>
                  <CardContent>
                    <CardDescription>{space.description}</CardDescription>
                  </CardContent>
                </Card>
              </Link>
            );
          })}
        </div>
      )}

      {/* Create Space dialog */}
      <Dialog open={dialogOpen} onOpenChange={(open) => { setDialogOpen(open); if (!open) resetForm(); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create a new space</DialogTitle>
            <DialogDescription>
              Spaces are where your team organises work. Choose a type to get started.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-2">
            {/* Name */}
            <div className="space-y-2">
              <label htmlFor="space-name" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Name
              </label>
              <Input
                id="space-name"
                placeholder="e.g. HR Support"
                value={formName}
                onChange={(e) => setFormName(e.target.value)}
                autoFocus
              />
            </div>

            {/* Type */}
            <div className="space-y-2">
              <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Type
              </label>
              <div className="grid grid-cols-3 gap-2">
                {MODULE_KEYS.map((key) => {
                  const def = MODULES[key];
                  return (
                    <button
                      key={key}
                      type="button"
                      onClick={() => setFormType(key)}
                      className={cn(
                        'flex flex-col items-center gap-2 rounded-[var(--radius-lg)] border p-3 transition-colors',
                        formType === key
                          ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                          : 'border-[var(--color-border)] text-[var(--color-text-muted)] hover:border-[var(--color-text-muted)] hover:text-[var(--color-text)]',
                      )}
                    >
                      <def.icon className="h-5 w-5" />
                      <span className="text-[var(--text-xs)] font-medium">{def.name}</span>
                    </button>
                  );
                })}
              </div>
            </div>

            {/* Key — required for Beacon spaces only */}
            {formType === 'beacon' && (
              <div className="space-y-2">
                <label htmlFor="space-key" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                  Ticket abbreviation <span className="text-[var(--color-danger)]">*</span>
                </label>
                <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  A short prefix used to number tickets — e.g. <span className="font-mono">HR</span> → <span className="font-mono">HR-1</span>, <span className="font-mono">HR-2</span>
                </p>
                <Input
                  id="space-key"
                  placeholder="e.g. HR, SUPPORT, IT"
                  value={formKey}
                  onChange={(e) =>
                    setFormKey(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 10))
                  }
                  className="w-40 font-mono"
                />
                {keyError && (
                  <p className="text-[var(--text-xs)] text-[var(--color-danger)]">{keyError}</p>
                )}
              </div>
            )}

            {/* Description */}
            <div className="space-y-2">
              <label htmlFor="space-desc" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Description <span className="text-[var(--color-text-muted)] font-normal">(optional)</span>
              </label>
              <Input
                id="space-desc"
                placeholder="What is this space for?"
                value={formDescription}
                onChange={(e) => setFormDescription(e.target.value)}
              />
            </div>

            {/* Error */}
            {createSpaceMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{createSpaceMutation.error.message}</p>
            )}
          </div>

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">Cancel</Button>
            </DialogClose>
            <Button onClick={handleCreate} disabled={createSpaceMutation.isPending || !formName.trim() || !!keyMissing || !!keyError}>
              {createSpaceMutation.isPending ? 'Creating...' : 'Create Space'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Internal stat card
// ---------------------------------------------------------------------------

interface StatCardProps {
  icon: typeof BarChart3;
  label: string;
  value: number;
}

function StatCard({ icon: Icon, label, value }: StatCardProps) {
  return (
    <Card>
      <CardContent className={cn('flex items-center gap-4 p-5')}>
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[var(--radius-md)] bg-[var(--color-primary-muted)]">
          <Icon className="h-5 w-5 text-[var(--color-primary)]" />
        </div>
        <div>
          <p className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
            {value}
          </p>
          <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">{label}</p>
        </div>
      </CardContent>
    </Card>
  );
}
