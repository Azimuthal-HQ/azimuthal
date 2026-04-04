import { Link } from 'react-router-dom';
import { Ticket, FileText, ListTodo, Plus, BarChart3, BookOpen, Zap } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge } from '../../components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '../../components/ui/card';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

interface Space {
  id: string;
  name: string;
  type: 'service_desk' | 'wiki' | 'project';
  description: string;
  memberCount: number;
  slug: string;
}

const MOCK_SPACES: Space[] = [
  {
    id: 's1',
    name: 'Customer Support',
    type: 'service_desk',
    description: 'Track and resolve customer issues and service requests.',
    memberCount: 12,
    slug: 'customer-support',
  },
  {
    id: 's2',
    name: 'Engineering Wiki',
    type: 'wiki',
    description: 'Internal documentation, runbooks, and architecture decisions.',
    memberCount: 24,
    slug: 'engineering-wiki',
  },
  {
    id: 's3',
    name: 'Product Roadmap',
    type: 'project',
    description: 'Plan and track product features across sprints.',
    memberCount: 8,
    slug: 'product-roadmap',
  },
];

const MOCK_STATS = {
  totalTickets: 142,
  wikiPages: 87,
  activeSprints: 3,
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const SPACE_ICON_MAP: Record<Space['type'], typeof Ticket> = {
  service_desk: Ticket,
  wiki: FileText,
  project: ListTodo,
};

const SPACE_BADGE_LABEL: Record<Space['type'], string> = {
  service_desk: 'Service Desk',
  wiki: 'Wiki',
  project: 'Project',
};

function linkForSpace(space: Space): string {
  switch (space.type) {
    case 'service_desk':
      return `/tickets`;
    case 'wiki':
      return `/wiki`;
    case 'project':
      return `/backlog`;
  }
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Main dashboard page showing spaces, quick stats, and navigation. */
export function DashboardPage() {
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
        <Button>
          <Plus className="mr-2 h-4 w-4" />
          Create Space
        </Button>
      </div>

      {/* Quick stats */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard
          icon={Ticket}
          label="Total Tickets"
          value={MOCK_STATS.totalTickets}
        />
        <StatCard
          icon={BookOpen}
          label="Wiki Pages"
          value={MOCK_STATS.wikiPages}
        />
        <StatCard
          icon={Zap}
          label="Active Sprints"
          value={MOCK_STATS.activeSprints}
        />
      </div>

      {/* Space cards */}
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
        {MOCK_SPACES.map((space) => {
          const Icon = SPACE_ICON_MAP[space.type];
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
                          {SPACE_BADGE_LABEL[space.type]}
                        </Badge>
                        <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                          {space.memberCount} members
                        </span>
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
