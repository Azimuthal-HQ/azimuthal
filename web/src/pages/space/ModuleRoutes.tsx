import { Navigate, useParams } from 'react-router-dom';
import { isModuleKey } from '../../shell/modules';
import { KanbanPage } from '../beacon/KanbanPage';
import { WikiPage } from '../codex/WikiPage';
import { BacklogPage } from '../vector/BacklogPage';
import { LabelsPage } from '../vector/LabelsPage';
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

/** ModuleLabelsRoute: Vector's labels page, placeholder elsewhere. */
export function ModuleLabelsRoute() {
  const { module } = useParams<{ module: string }>();
  if (module === 'vector') return <LabelsPage />;
  if (isModuleKey(module)) return <SpacePlaceholderPage feature="labels" />;
  return <SpacePlaceholderPage feature="unknown" />;
}
