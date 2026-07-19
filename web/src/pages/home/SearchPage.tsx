import { Search } from 'lucide-react';
import { EmptyState } from '../../shell/EmptyState';

/**
 * SearchPage holds the /search route behind the top bar's "Search everything"
 * control. Cross-module search ships in v0.3.5 (spec P6); until then the
 * route renders a branded empty state rather than a blank body.
 */
export function SearchPage() {
  return (
    <EmptyState
      icon={Search}
      title="Search everything"
      description="Cross-module search over every space you can read arrives in a later v0.3 release."
    />
  );
}
