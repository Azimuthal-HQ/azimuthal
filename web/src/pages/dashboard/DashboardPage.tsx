import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Ticket, FileText, ListTodo, Plus, BarChart3, BookOpen, Zap, AlertCircle } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge } from '../../components/ui/badge';
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const SPACE_ICON_MAP: Record<SpaceType, typeof Ticket> = {
  service_desk: Ticket,
  wiki: FileText,
  project: ListTodo,
};

const SPACE_BADGE_LABEL: Record<SpaceType, string> = {
  service_desk: 'Service Desk',
  wiki: 'Wiki',
  project: 'Project',
};

function linkForSpace(space: Space): string {
  switch (space.space_type) {
    case 'service_desk':
      return `/spaces/${space.id}/tickets`;
    case 'wiki':
      return `/spaces/${space.id}/wiki`;
    case 'project':
      return `/spaces/${space.id}/backlog`;
  }
}

function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Main dashboard page showing spaces, quick stats, and navigation. */
export function DashboardPage() {
  const navigate = useNavigate();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const { data: spaces, isLoading, error } = useSpaces(orgId);

  const [dialogOpen, setDialogOpen] = useState(false);
  const [formName, setFormName] = useState('');
  const [formType, setFormType] = useState<SpaceType>('service_desk');
  const [formDescription, setFormDescription] = useState('');

  const createSpaceMutation = useCreateSpace(orgId);

  function resetForm() {
    setFormName('');
    setFormType('service_desk');
    setFormDescription('');
  }

  async function handleCreate() {
    const name = formName.trim();
    if (!name) return;

    const slug = slugify(name);

    try {
      const created = await createSpaceMutation.mutateAsync({
        name,
        slug,
        space_type: formType,
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

      {/* Quick stats */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard icon={Ticket} label="Spaces" value={spaces?.length ?? 0} />
        <StatCard icon={BookOpen} label="Service Desks" value={spaces?.filter(s => s.space_type === 'service_desk').length ?? 0} />
        <StatCard icon={Zap} label="Projects" value={spaces?.filter(s => s.space_type === 'project').length ?? 0} />
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

      {/* Space cards */}
      {spaces && (
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
          {spaces.length === 0 && !isLoading && (
            <div className="col-span-full flex h-32 items-center justify-center text-[var(--color-text-muted)]">
              No spaces yet. Create one to get started.
            </div>
          )}
          {spaces.map((space) => {
            const Icon = SPACE_ICON_MAP[space.space_type];
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
                          <Badge variant="secondary">
                            {SPACE_BADGE_LABEL[space.space_type]}
                          </Badge>
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
                placeholder="e.g. Backend API"
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
                {([
                  { value: 'service_desk' as const, label: 'Service Desk', icon: Ticket },
                  { value: 'wiki' as const, label: 'Wiki', icon: FileText },
                  { value: 'project' as const, label: 'Project', icon: ListTodo },
                ]).map((opt) => (
                  <button
                    key={opt.value}
                    type="button"
                    onClick={() => setFormType(opt.value)}
                    className={cn(
                      'flex flex-col items-center gap-2 rounded-[var(--radius-lg)] border p-3 transition-colors',
                      formType === opt.value
                        ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                        : 'border-[var(--color-border)] text-[var(--color-text-muted)] hover:border-[var(--color-text-muted)] hover:text-[var(--color-text)]',
                    )}
                  >
                    <opt.icon className="h-5 w-5" />
                    <span className="text-[var(--text-xs)] font-medium">{opt.label}</span>
                  </button>
                ))}
              </div>
            </div>

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
            <Button onClick={handleCreate} disabled={createSpaceMutation.isPending || !formName.trim()}>
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
