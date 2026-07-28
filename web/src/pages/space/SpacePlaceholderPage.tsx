import { useParams } from 'react-router-dom';
import {
  Clock,
  Columns3,
  Compass,
  ListFilter,
  ListTodo,
  Map,
  PenLine,
  Search,
  Settings,
  Star,
  Tags,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import { EmptyState } from '../../shell/EmptyState';
import { isModuleKey, MODULES } from '../../shell/modules';

export type PlaceholderFeature =
  | 'backlog'
  | 'board'
  | 'sprints'
  | 'roadmap'
  | 'labels'
  | 'queues'
  | 'settings'
  | 'search'
  | 'recent'
  | 'starred'
  | 'drafts'
  | 'unknown';

interface FeatureCopy {
  icon: LucideIcon;
  title: string;
  /** null → generic not-part-of-this-module copy referencing nativeModule. */
  description: string | null;
  nativeModule?: 'beacon' | 'codex' | 'vector';
}

const FEATURES: Record<PlaceholderFeature, FeatureCopy> = {
  backlog: { icon: ListTodo, title: 'Backlog', description: null, nativeModule: 'vector' },
  board: { icon: Columns3, title: 'Board', description: null, nativeModule: 'vector' },
  sprints: { icon: Zap, title: 'Sprints', description: null, nativeModule: 'vector' },
  roadmap: { icon: Map, title: 'Roadmap', description: null, nativeModule: 'vector' },
  labels: { icon: Tags, title: 'Labels', description: null, nativeModule: 'vector' },
  queues: { icon: ListFilter, title: 'Queues', description: null, nativeModule: 'beacon' },
  settings: {
    icon: Settings,
    title: 'Space settings',
    description: 'Per-space settings arrive with grants and permissions in an upcoming release.',
  },
  search: {
    icon: Search,
    title: 'Search this wiki',
    description: 'Search within a wiki arrives with cross-module search in an upcoming release.',
  },
  recent: {
    icon: Clock,
    title: 'Recent',
    description: 'Pages you opened most recently in this wiki — coming soon.',
  },
  starred: {
    icon: Star,
    title: 'Starred',
    description: 'Pages you pinned in this wiki — coming soon.',
  },
  drafts: {
    icon: PenLine,
    title: 'Drafts',
    description: 'Unpublished edits, visible only to you until you publish — coming soon.',
  },
  unknown: {
    icon: Compass,
    title: 'Nothing here',
    description: 'This page does not exist in this space. Use the sidebar to find your way back.',
  },
};

/**
 * SpacePlaceholderPage is the branded empty state for any space sub-route
 * without real content yet (ADR-0005 point 5: a blank screen is a defect).
 * It renders inside SpaceLayout, so the sidebar and space context stay put.
 */
export function SpacePlaceholderPage({ feature }: { feature: PlaceholderFeature }) {
  const { module } = useParams<{ module: string }>();
  const copy = FEATURES[feature];
  const moduleName = isModuleKey(module) ? MODULES[module].name : 'this module';

  const description =
    copy.description ??
    (copy.nativeModule && isModuleKey(module) && module !== copy.nativeModule
      ? `${copy.title} is a ${MODULES[copy.nativeModule].name} view. It isn't part of ${moduleName} spaces.`
      : `${copy.title} is coming to ${moduleName} in an upcoming release.`);

  return <EmptyState icon={copy.icon} title={copy.title} description={description} />;
}
