/**
 * useTeamFocus is the seam for the team-focus filter (ADR-0006 point 7).
 * Teams arrive in P2 (migration 007, owner_team_id in 008); until then no
 * focus can ever be active, so the hook always reports null and the
 * FocusChip stays unrendered. The shape is final: P2 swaps the constant
 * for real state keyed on team id without touching any consumer.
 */
export interface TeamFocus {
  teamId: string;
  teamName: string;
}

export function useTeamFocus(): {
  focus: TeamFocus | null;
  clearFocus: () => void;
} {
  return {
    focus: null,
    clearFocus: () => {
      // No-op until teams exist (P2).
    },
  };
}
