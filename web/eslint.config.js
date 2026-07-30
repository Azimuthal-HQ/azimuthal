import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

// `npm run lint` is a required CI gate (the `Frontend` job in
// .github/workflows/ci.yml). It runs `eslint .` with no --max-warnings slack.
//
// There is no baseline file and there will not be one — see docs/known-issues.md
// #17. Where a rule is not satisfied it is turned OFF here, scoped to the exact
// files it could not be satisfied in, with the reason written next to it. That
// is deliberately more annoying to add to than a generated ledger: a new file
// cannot drift into an exemption without someone editing this list and
// justifying it in review.
//
// Every override below is scoped rather than global, so each rule stays live
// for the rest of the codebase and for every file added after this.

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      // The repository already writes a leading underscore to mean "this
      // binding is deliberately unused" — `friendlyErrorMessage: (_e, fallback)`
      // in RoadmapPage.test.tsx is one of several. Without these patterns the
      // rule ignores that convention in the one position it matters: a lone
      // unused parameter. (`_e` above goes unreported only because the default
      // `args: 'after-used'` stops at the last used parameter.) This is the
      // rule's own standard option, not an exemption.
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
    },
  },

  // ───────────────────────────────────────────────────────────────────
  // react-refresh/only-export-components — OFF for 12 files (21 findings)
  //
  // This is a Vite fast-refresh developer-experience rule. It has no runtime,
  // correctness or security effect: it asks that a module export components and
  // nothing else, so HMR can swap a component without discarding module state.
  // Satisfying it means MOVING the non-component export into a new module and
  // rewriting every import site.
  //
  // Three groups, none fixable without a refactor this pass is scoped out of:
  //
  //  1. The `useX` + `XProvider` context idiom — the hook must live beside the
  //     context object it reads, because that object is module-private on
  //     purpose. Splitting them means exporting the raw context and widening
  //     the API to avoid a fast-refresh warning.
  //       theme/ThemeProvider.tsx (useTheme), ui/toast.tsx (useToast),
  //       shell/ShellUIContext.tsx (useShellUI),
  //       shell/sidebars/SidebarChrome.tsx (useSidebarIsCollapsed),
  //       codex/CodexDocumentContext.tsx (useCodexDocumentContext, usePageTitle)
  //
  //  2. Shared vocabulary modules — a domain type, its constants and its
  //     helpers sitting beside the two small components that render them.
  //     priority.tsx alone carries five findings, and `normalizePriority` has
  //     twelve production importers; splitting it rewrites imports across
  //     beacon, vector, shared and the non-React lib/views/draft.ts.
  //       priority.tsx, ItemKeyChip.tsx, views/ViewChips.tsx,
  //       ui/badge.tsx (badgeVariants), ui/button.tsx (buttonVariants)
  //
  //  3. Helpers exported solely so a unit test can reach them —
  //     resolveDropTarget/laneKeyOf/buildLanes and sprintSpan/computeWindow.
  //     Un-exporting breaks the test files that import them, and editing a test
  //     to accommodate a lint rule is not something this project does.
  //       pages/vector/SprintBoardPage.tsx, pages/vector/SprintTimeline.tsx
  //
  // reactRefresh.configs.vite already sets allowConstantExport, so nothing
  // below is a plain literal constant — those are exempt already.
  // ───────────────────────────────────────────────────────────────────
  {
    files: [
      'src/components/ItemKeyChip.tsx',
      'src/components/priority.tsx',
      'src/components/codex/CodexDocumentContext.tsx',
      'src/components/theme/ThemeProvider.tsx',
      'src/components/ui/badge.tsx',
      'src/components/ui/button.tsx',
      'src/components/ui/toast.tsx',
      'src/components/views/ViewChips.tsx',
      'src/pages/vector/SprintBoardPage.tsx',
      'src/pages/vector/SprintTimeline.tsx',
      'src/shell/ShellUIContext.tsx',
      'src/shell/sidebars/SidebarChrome.tsx',
    ],
    rules: { 'react-refresh/only-export-components': 'off' },
  },

  // ───────────────────────────────────────────────────────────────────
  // react-hooks/set-state-in-effect — OFF for 11 files (13 findings)
  //
  // Unlike the rule above, this one earns its place: a setState in an effect
  // body is a render cascade, and often a sign that state should have been
  // derived instead. It stays ON everywhere not listed here.
  //
  // But all 13 findings are BEHAVIOURAL. There is no edit that silences the
  // rule and leaves the rendered output identical — each fix changes what
  // paints first, what re-fires, or what a refetch does to an in-progress edit.
  // This pass is mechanical and contracted to zero behaviour change, so they
  // are turned off here rather than guessed at:
  //
  //  - Controlled-form seeding (copy fetched data into editable local state):
  //      CustomFieldsSection.tsx, admin/OrgSettingsPage.tsx,
  //      space/BoardConfigSection.tsx — all three guard on `dirty` so a refetch
  //      never stomps unsaved edits. BoardConfigSection always did; the other
  //      two were the defect recorded as known-issues #17 item 1, closed by the
  //      maintenance mini-pass. The guard is still a setState in an effect, so
  //      the rule stays off for all three.
  //  - The `?create=…` deep link (read the param, open the dialog, clear the
  //      param): beacon/TicketListPage.tsx, home/HomeOverviewPage.tsx,
  //      vector/BacklogPage.tsx, shell/sidebars/CodexSidebar.tsx. The clear is
  //      a router navigation — the external-system side effect an effect is for.
  //  - Reset-before-async (clear the previous failure before a new load):
  //      codex/nodeviews/ImageView.tsx, shared/SharedAttachment.tsx
  //  - Post-layout DOM measurement (flip the suggestion panel above the input
  //      when it would overflow the viewport): TicketRefField.tsx
  //  - Selection mirroring and auto-select: codex/WikiPage.tsx (3 findings, one
  //      already carrying a comment that records an earlier useMemo
  //      implementation of the same logic being wrong).
  //
  // Three of these deserve a look on their own merits; they are recorded in
  // docs/known-issues.md #17 rather than fixed in passing.
  // ───────────────────────────────────────────────────────────────────
  {
    files: [
      'src/components/CustomFieldsSection.tsx',
      'src/components/TicketRefField.tsx',
      'src/components/codex/nodeviews/ImageView.tsx',
      'src/pages/admin/OrgSettingsPage.tsx',
      'src/pages/beacon/TicketListPage.tsx',
      'src/pages/codex/WikiPage.tsx',
      'src/pages/home/HomeOverviewPage.tsx',
      'src/pages/shared/SharedAttachment.tsx',
      'src/pages/space/BoardConfigSection.tsx',
      'src/pages/vector/BacklogPage.tsx',
      'src/shell/sidebars/CodexSidebar.tsx',
    ],
    rules: { 'react-hooks/set-state-in-effect': 'off' },
  },

  // ───────────────────────────────────────────────────────────────────
  // react-hooks/purity — OFF for 1 file (1 finding)
  //
  // SprintTimeline takes `now = Date.now()` as a default parameter, so the
  // clock is read during render whenever the prop is omitted — which is what
  // RoadmapPage does, its only production call site.
  //
  // The candidate fixes were tested against eslint rather than reasoned about.
  // Relocating the call into the function body does NOT silence the rule: the
  // rule is about calling an impure function during render, not about where in
  // the render it is called. What does silence it — freezing the value in a
  // useState/useRef initialiser, or making the caller pass it — changes
  // behaviour. Today a re-render re-reads the clock and the "today" marker
  // tracks it; frozen, it would stop at mount.
  //
  // The safety net does not cover this either: all thirteen assertions in
  // __tests__/SprintTimeline.test.tsx pass `now` explicitly, so no test
  // exercises the default. A change here would be invisible to the suite, which
  // is exactly when not to guess.
  // ───────────────────────────────────────────────────────────────────
  {
    files: ['src/pages/vector/SprintTimeline.tsx'],
    rules: { 'react-hooks/purity': 'off' },
  },
])
