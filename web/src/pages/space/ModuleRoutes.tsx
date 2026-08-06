import { Navigate, useParams } from 'react-router-dom';
import { isModuleKey } from '../../shell/modules';
import { KanbanPage } from '../beacon/KanbanPage';
import { QueueBuilderPage } from '../beacon/queues/QueueBuilderPage';
import { QueueDetailPage } from '../beacon/queues/QueueDetailPage';
import { QueuesPage } from '../beacon/queues/QueuesPage';
import { WikiPage } from '../codex/WikiPage';
import { BacklogPage } from '../vector/BacklogPage';
import { TagsPage } from '../tags/TagsPage';
import { RoadmapPage } from '../vector/RoadmapPage';
import { SprintBoardPage } from '../vector/SprintBoardPage';
import { SprintsPage } from '../vector/SprintsPage';
import { SpacePlaceholderPage } from './SpacePlaceholderPage';

/*
 * The shared sub-route names (board, backlog, sprints, roadmap, labels) exist
 * for every module — the sidebar and layout never change shape with the
 * sub-route — but which component renders depends on the module. These
 * dispatchers read :module and pick the real page where one exists, the
 * branded placeholder where it does not.
 */

/** ModuleIndexRoute resolves the bare /:module/:spaceId URL per module. */
export function ModuleIndexRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'beacon') return <Navigate to="tickets" replace />;
  if (module === 'vector') return <Navigate to="backlog" replace />;
  if (module === 'codex') return <WikiPage />;
  return <SpacePlaceholderPage feature="unknown" />;
}

/** ModuleBoardRoute: Beacon's kanban, Vector's sprint board, placeholder elsewhere. */
export function ModuleBoardRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'beacon') return <KanbanPage />;
  if (module === 'vector') return <SprintBoardPage />;
  return <SpacePlaceholderPage feature="board" />;
}

/** ModuleBacklogRoute: Vector's backlog, placeholder elsewhere. */
export function ModuleBacklogRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'vector') return <BacklogPage />;
  return <SpacePlaceholderPage feature="backlog" />;
}

/** ModuleSprintsRoute: Vector's sprints, placeholder elsewhere. */
export function ModuleSprintsRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'vector') return <SprintsPage />;
  return <SpacePlaceholderPage feature="sprints" />;
}

/** ModuleRoadmapRoute: Vector's roadmap, placeholder elsewhere. */
export function ModuleRoadmapRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'vector') return <RoadmapPage />;
  return <SpacePlaceholderPage feature="roadmap" />;
}

/*
 * Queues (P4) are a Beacon concept: they are bound to a Beacon space and their
 * results are that space's tickets. The three dispatchers below follow the same
 * shape as the rest of this file — real page for Beacon, branded placeholder
 * anywhere else — so a `/vector/{id}/queues` URL keeps the space chrome and
 * says what it is instead of rendering a Beacon page over Vector data.
 */

/** ModuleQueuesRoute: Beacon's queue list and management surface. */
export function ModuleQueuesRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'beacon') return <QueuesPage />;
  return <SpacePlaceholderPage feature="queues" />;
}

/** ModuleQueueDetailRoute: one Beacon queue, resolved for the reader. */
export function ModuleQueueDetailRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'beacon') return <QueueDetailPage />;
  return <SpacePlaceholderPage feature="queues" />;
}

/** ModuleQueueBuilderRoute: create or edit a Beacon queue. */
export function ModuleQueueBuilderRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'beacon') return <QueueBuilderPage />;
  return <SpacePlaceholderPage feature="queues" />;
}

/**
 * ModuleLabelsRoute: the tags index, in ANY module's chrome.
 *
 * The path still says labels because Vector's sidebar and people's bookmarks
 * have pointed at /labels since P1 — but the page it opened promised a label
 * manager that was never built, and the entity-tags convergence delivered the
 * feature under its real name. Tags are module-neutral (pages, tickets and
 * items share one vocabulary), so unlike the other dispatchers in this file
 * there is no per-module fork: every module gets the real surface.
 */
export function ModuleLabelsRoute() {
  const { module } = useParams<{ module: string }>();
  if (isModuleKey(module)) return <TagsPage />;
  return <SpacePlaceholderPage feature="unknown" />;
}
