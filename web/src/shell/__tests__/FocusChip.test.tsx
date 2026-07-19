import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FocusChip } from '../FocusChip';
import * as teamFocus from '../hooks/useTeamFocus';

/** ADR-0006 point 7: the focus chip renders only while a team focus is active. */
describe('FocusChip', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders nothing when no focus is active (the P1 state)', () => {
    const { container } = render(<FocusChip />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders the team name and a clear control when a focus is active', () => {
    const clearFocus = vi.fn();
    vi.spyOn(teamFocus, 'useTeamFocus').mockReturnValue({
      focus: { teamId: 't1', teamName: 'Platform' },
      setFocus: vi.fn(),
      clearFocus,
    });

    render(<FocusChip />);
    const chip = screen.getByTestId('focus-chip');
    expect(chip).toHaveTextContent('Platform');

    screen.getByRole('button', { name: 'Clear team focus' }).click();
    expect(clearFocus).toHaveBeenCalledTimes(1);
  });
});
