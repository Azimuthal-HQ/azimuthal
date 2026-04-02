# Azimuthal — GitHub Setup Checklist
# Complete these steps after creating github.com/Azimuthal-HQ/azimuthal
# One-time setup — do before Agent 0A opens its first PR.

---

## 1. Create the GitHub Organisation

- [ ] github.com → profile photo → "Your organizations" → "New organization"
- [ ] Name: `Azimuthal-HQ`  (if taken, try `azimuthal-hq` or `getazimuthal`)
- [ ] Plan: Free (upgrade to Team later if needed)
- [ ] Complete setup — skip adding members for now

---

## 2. Create the Two Repos

**Community repo (public):**
- [ ] github.com/Azimuthal-HQ → "New repository"
- [ ] Name: `Azimuthal-HQ`
- [ ] Visibility: **Public**
- [ ] Add README, Apache 2.0 license, Go .gitignore
- [ ] Default branch: `main`

**Enterprise repo (private):**
- [ ] github.com/Azimuthal-HQ → "New repository"
- [ ] Name: `azimuthal-ee`
- [ ] Visibility: **Private**
- [ ] No license file (proprietary)
- [ ] Default branch: `main`

---

## 3. Repository Settings (community repo)

Settings → General:
- [ ] Disable "Allow merge commits"
- [ ] Enable "Allow squash merging"
- [ ] Disable "Allow rebase merging"
- [ ] Enable "Automatically delete head branches"
- [ ] Enable "Always suggest updating pull request branches"

---

## 4. Branch Protection (community repo)

Settings → Branches → Add branch protection rule → `main`:

- [ ] Require a pull request before merging
  - Required approvals: 0 (pipeline is the gatekeeper)
  - Dismiss stale reviews when new commits pushed
- [ ] Require status checks to pass before merging
  - Require branches to be up to date
  - Required checks (add AFTER first CI run):
    - `All Gates Passed`
    - `Build`
    - `Test`
    - `Lint`
    - `SAST (gosec)`
    - `Dependency Scan (govulncheck)`
    - `Secret Scan (gitleaks)`
    - `Container Scan (trivy)`
- [ ] Require conversation resolution before merging
- [ ] **Do not allow bypassing above settings** ← include admins

---

## 5. Secrets (community repo)

Settings → Secrets and variables → Actions → New repository secret:

- [ ] `JWT_SECRET`
  ```bash
  openssl rand -hex 32   # run this, paste the output
  ```

---

## 6. Native GitHub Security Features

Settings → Security & analysis:

- [ ] Dependency graph — Enable
- [ ] Dependabot alerts — Enable
- [ ] Dependabot security updates — Enable  (auto-PRs for vulnerable deps)
- [ ] Secret scanning — Enable
- [ ] Secret scanning push protection — Enable  (blocks push entirely)

---

## 7. Actions Permissions

Settings → Actions → General:

- [ ] Workflow permissions → Read and write permissions
  (allows workflows to push to ghcr.io/azimuthal using GITHUB_TOKEN)

---

## 8. GHCR Package Visibility

After the first release workflow runs:
- [ ] github.com/Azimuthal-HQ → Packages → azimuthal → Package settings
- [ ] Visibility: Private (until you're ready for public launch)

---

## 9. CLA Assistant (before accepting external PRs)

- [ ] Go to cla-assistant.io → Sign in with GitHub
- [ ] Link repo: Azimuthal-HQ/azimuthal
- [ ] Create `CLA.md` in repo root with your CLA text
- [ ] Point CLA assistant to that file
- [ ] Bot will auto-comment on all future PRs from new contributors

---

## 10. Dependabot Config

Create `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
      day: monday
    open-pull-requests-limit: 5
    labels: [dependencies, go]

  - package-ecosystem: docker
    directory: /build
    schedule:
      interval: weekly
    labels: [dependencies, docker]

  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
    labels: [dependencies, github-actions]
```

---

## 11. Enterprise Repo Secrets (azimuthal-ee — set up when ready)

Fine-grained PAT for community repo access:
- [ ] github.com → Settings → Developer settings → Fine-grained tokens
- [ ] New token:
  - Resource owner: azimuthal org
  - Repository access: Only `azimuthal/azimuthal`
  - Permissions: Contents (Read), Metadata (Read)
  - Expiration: 90 days
- [ ] Add as `COMMUNITY_REPO_TOKEN` secret in `azimuthal-ee` repo

---

## 12. First Run Checklist

After Agent 0A opens its first PR:

- [ ] All 8 CI jobs appear in the PR checks
- [ ] Go back to branch protection → add status check names
  (they only appear after the first workflow run)
- [ ] GitHub Security tab shows gosec + trivy SARIF results
- [ ] Merge Agent 0A's PR
- [ ] Kick off Agent 0B

---

## Estimated Setup Time

| Step | Time |
|------|------|
| Create org + repos | 10 min |
| Repository settings | 5 min |
| Branch protection | 10 min |
| Secrets | 5 min |
| Security features | 5 min |
| CLA assistant | 15 min |
| Dependabot config | 5 min |
| **Total** | **~55 min** |
