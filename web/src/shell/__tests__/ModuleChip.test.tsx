import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ModuleChip } from '../ModuleChip';
import { MODULE_KEYS, MODULES } from '../modules';

/**
 * P1 DoD (spec §9): the ModuleChip foreground is --module-chip-fg, never the
 * module hue. Hue with neutral text means provenance; hue with matching text
 * would mean state (spec §8).
 */
describe('ModuleChip', () => {
  it.each(MODULE_KEYS)('renders %s with neutral foreground and the module hue only as background', (key) => {
    render(<ModuleChip module={key} />);
    const chip = screen.getByTestId('module-chip');

    expect(chip).toHaveTextContent(MODULES[key].name);
    expect(chip).toHaveAttribute('data-module', key);

    // Foreground: exactly the neutral chip token.
    expect(chip.style.color).toBe('var(--module-chip-fg)');

    // The module hue reaches the chip only through --chip-hue, which the
    // .module-chip rule mixes into the BACKGROUND at --module-chip-alpha.
    expect(chip.style.getPropertyValue('--chip-hue')).toBe(`var(${MODULES[key].hueVar})`);
    expect(chip.classList.contains('module-chip')).toBe(true);

    // Never the module hue as text colour.
    expect(chip.style.color).not.toContain(MODULES[key].hueVar);
  });
});
