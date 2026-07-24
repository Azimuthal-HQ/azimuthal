import { render, screen, fireEvent, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { SprintTimeline, sprintSpan, computeWindow } from '../SprintTimeline';
import type { ProjectItem, RoadmapSprint, Sprint } from '../../../lib/api';

// W3: sprints as spans on a shared axis, items placed by their sprint.

const NOW = Date.parse('2026-07-15T00:00:00Z');
const DAY = 86_400_000;

function sprint(id: string, name: string, status: Sprint['status'], starts: string | null, ends: string | null): Sprint {
  return {
    id, space_id: 's1', name, goal: '', status,
    starts_at: starts, ends_at: ends,
    created_at: '', updated_at: '',
  };
}

function item(id: string, title: string): ProjectItem {
  return {
    id, space_id: 's1', number: Number(id), item_key: `VEC-${id}`,
    title, description: '', kind: 'task', status: 'open', priority: 'high',
    assignee_id: null, reporter_id: 'u1', sprint_id: 's', rank: '', labels: [],
    created_at: '', updated_at: '',
  };
}

function rs(s: Sprint, items: ProjectItem[] = []): RoadmapSprint {
  return { sprint: s, items };
}

const inWindow = rs(
  sprint('a', 'Current Sprint', 'active', '2026-07-06T00:00:00Z', '2026-07-20T00:00:00Z'),
  [item('1', 'Ship the thing')],
);

describe('sprintSpan', () => {
  it('uses both dates when present', () => {
    const span = sprintSpan('2026-07-01T00:00:00Z', '2026-07-15T00:00:00Z');
    expect(span).toEqual({ start: Date.parse('2026-07-01T00:00:00Z'), end: Date.parse('2026-07-15T00:00:00Z') });
  });

  it('gives a one-sided sprint a nominal two-week span rather than zero width', () => {
    const fromStart = sprintSpan('2026-07-01T00:00:00Z', null)!;
    expect(fromStart.end - fromStart.start).toBe(14 * DAY);

    const fromEnd = sprintSpan(null, '2026-07-15T00:00:00Z')!;
    expect(fromEnd.end - fromEnd.start).toBe(14 * DAY);
    expect(fromEnd.end).toBe(Date.parse('2026-07-15T00:00:00Z'));
  });

  it('normalises an inverted range instead of producing negative width', () => {
    const span = sprintSpan('2026-07-20T00:00:00Z', '2026-07-01T00:00:00Z')!;
    expect(span.start).toBeLessThan(span.end);
  });

  it('returns null when the sprint has no dates at all', () => {
    expect(sprintSpan(null, null)).toBeNull();
  });
});

describe('computeWindow', () => {
  it('spans the requested number of months for a preset', () => {
    const q = computeWindow('quarter', [], NOW);
    const y = computeWindow('year', [], NOW);
    expect(y.end - y.start).toBeGreaterThan(q.end - q.start);
  });

  it('starts a preset window before today so in-flight work is visible', () => {
    const w = computeWindow('half', [], NOW);
    expect(w.start).toBeLessThan(NOW);
    expect(w.end).toBeGreaterThan(NOW);
  });

  it('fits every span under "all"', () => {
    const spans = [
      { start: Date.parse('2025-01-01T00:00:00Z'), end: Date.parse('2025-02-01T00:00:00Z') },
      { start: Date.parse('2027-01-01T00:00:00Z'), end: Date.parse('2027-02-01T00:00:00Z') },
    ];
    const w = computeWindow('all', spans, NOW);
    expect(w.start).toBeLessThanOrEqual(spans[0].start);
    expect(w.end).toBeGreaterThanOrEqual(spans[1].end);
  });

  it('never produces a zero-width window for a single instantaneous span', () => {
    const at = Date.parse('2026-07-01T00:00:00Z');
    const w = computeWindow('all', [{ start: at, end: at }], NOW);
    // A zero-width window divides by zero when positioning bars.
    expect(w.end - w.start).toBeGreaterThan(0);
  });
});

describe('SprintTimeline', () => {
  it('renders a bar per sprint that falls in the window', () => {
    render(<SprintTimeline sprints={[inWindow]} zoom="half" spaceKey="VEC" now={NOW} />);
    expect(screen.getByTestId('sprint-timeline')).toBeInTheDocument();
    expect(screen.getAllByTestId('timeline-bar')).toHaveLength(1);
  });

  it('marks today inside the window', () => {
    render(<SprintTimeline sprints={[inWindow]} zoom="half" spaceKey="VEC" now={NOW} />);
    expect(screen.getAllByTestId('timeline-today').length).toBeGreaterThan(0);
  });

  it('shows the empty state when no sprint has dates', () => {
    const undated = rs(sprint('b', 'No Dates', 'planned', null, null));
    render(<SprintTimeline sprints={[undated]} zoom="half" now={NOW} />);
    expect(screen.getByText(/No sprints with dates/i)).toBeInTheDocument();
    expect(screen.queryByTestId('timeline-bar')).not.toBeInTheDocument();
  });

  it('places a sprint bar proportionally to its dates, not at a fixed offset', () => {
    const early = rs(sprint('e', 'Early', 'completed', '2026-06-05T00:00:00Z', '2026-06-19T00:00:00Z'));
    const late = rs(sprint('l', 'Late', 'planned', '2026-09-05T00:00:00Z', '2026-09-19T00:00:00Z'));
    render(<SprintTimeline sprints={[early, late]} zoom="half" now={NOW} />);

    const bars = screen.getAllByTestId('timeline-bar');
    const lefts = bars.map(b => parseFloat((b as HTMLElement).style.left));
    // The June sprint must sit left of the September one. A layout that
    // ignored dates would give both the same offset.
    expect(lefts[0]).toBeLessThan(lefts[1]);
  });

  it('counts sprints outside the range instead of dropping them silently', () => {
    const farOff = rs(sprint('f', 'Next Year', 'planned', '2028-01-01T00:00:00Z', '2028-01-14T00:00:00Z'));
    render(<SprintTimeline sprints={[inWindow, farOff]} zoom="quarter" now={NOW} />);

    expect(screen.getAllByTestId('timeline-bar')).toHaveLength(1);
    const note = screen.getByTestId('timeline-hidden-note');
    expect(note).toHaveTextContent(/1 sprint outside this range/i);
  });

  it('places a sprint\'s items against its span only once expanded', () => {
    render(<SprintTimeline sprints={[inWindow]} zoom="half" spaceKey="VEC" now={NOW} />);

    expect(screen.queryByText('Ship the thing')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Toggle items for Current Sprint/i }));

    expect(screen.getByText('Ship the thing')).toBeInTheDocument();
    // Items carry their key as a provenance chip.
    expect(screen.getByText('VEC-1')).toBeInTheDocument();
  });

  it('keeps an item label out of its placed bar so a narrow span stays legible', () => {
    // A sprint's span is a few percent of the track at a wide zoom. Text
    // rendered inside the bar collapses to zero width and disappears —
    // Playwright reported exactly that as "hidden". The label belongs in the
    // gutter; the bar carries position only.
    render(<SprintTimeline sprints={[inWindow]} zoom="year" spaceKey="VEC" now={NOW} />);
    fireEvent.click(screen.getByRole('button', { name: /Toggle items for Current Sprint/i }));

    const bar = screen.getByTestId('timeline-item-bar');
    expect(bar).toBeEmptyDOMElement();
    expect(bar).toHaveTextContent('');
    // The label is still on the page, just not inside the bar.
    expect(screen.getByText('Ship the thing')).toBeInTheDocument();
  });

  it('collapses again on a second click', () => {
    render(<SprintTimeline sprints={[inWindow]} zoom="half" spaceKey="VEC" now={NOW} />);
    const toggle = screen.getByRole('button', { name: /Toggle items for Current Sprint/i });
    fireEvent.click(toggle);
    expect(screen.getByText('Ship the thing')).toBeInTheDocument();
    fireEvent.click(toggle);
    expect(screen.queryByText('Ship the thing')).not.toBeInTheDocument();
  });

  it('says so when an expanded sprint has no items', () => {
    const empty = rs(sprint('c', 'Empty Sprint', 'planned', '2026-07-06T00:00:00Z', '2026-07-20T00:00:00Z'));
    render(<SprintTimeline sprints={[empty]} zoom="half" now={NOW} />);
    fireEvent.click(screen.getByRole('button', { name: /Toggle items for Empty Sprint/i }));
    expect(screen.getByText(/No items in this sprint/i)).toBeInTheDocument();
  });

  it('keeps every bar inside the track when a span overruns the window', () => {
    const overrun = rs(sprint('o', 'Long Haul', 'active', '2020-01-01T00:00:00Z', '2030-01-01T00:00:00Z'));
    render(<SprintTimeline sprints={[overrun]} zoom="quarter" now={NOW} />);

    const bar = screen.getByTestId('timeline-bar') as HTMLElement;
    const left = parseFloat(bar.style.left);
    const width = parseFloat(bar.style.width);
    expect(left).toBeGreaterThanOrEqual(0);
    expect(left + width).toBeLessThanOrEqual(100.001);
  });

  it('orders rows by start date regardless of input order', () => {
    const later = rs(sprint('l', 'Later', 'planned', '2026-08-01T00:00:00Z', '2026-08-14T00:00:00Z'));
    const earlier = rs(sprint('e', 'Earlier', 'completed', '2026-06-01T00:00:00Z', '2026-06-14T00:00:00Z'));
    render(<SprintTimeline sprints={[later, earlier]} zoom="half" now={NOW} />);

    // The name appears twice per row (row toggle + bar label), so assert on
    // the bar specifically.
    const rows = screen.getAllByTestId('timeline-row');
    expect(within(rows[0]).getByTestId('timeline-bar')).toHaveTextContent('Earlier');
    expect(within(rows[1]).getByTestId('timeline-bar')).toHaveTextContent('Later');
  });
});
