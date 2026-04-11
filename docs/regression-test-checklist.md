# Azimuthal Release Verification Checklist

Run this checklist after every release before tagging.
All items must pass before pushing a version tag.
Estimated time: 15 minutes on a fresh deployment.

---

## Setup

- [ ] Run `docker compose down -v` to clear all data
- [ ] Run `docker compose up -d` for a fresh deployment
- [ ] Wait 15 seconds for services to initialize
- [ ] Run `docker compose exec app azimuthal admin create-user --email admin@test.com --name "Admin" --password testpass123`
- [ ] Confirm output shows: User created, Organization created, User added as owner
- [ ] Open `http://localhost:8080` in browser
- [ ] Confirm browser redirects to `/login`
- [ ] Login with `admin@test.com` / `testpass123`
- [ ] Confirm redirect to dashboard — no error, no blank screen

---

## Dashboard

- [ ] Dashboard loads with "Welcome back" heading
- [ ] Stat cards show 0 for all counts
- [ ] "+ Create Space" button is visible
- [ ] Dark mode is the default appearance

---

## Service Desk Space

- [ ] Click "+ Create Space" → modal opens
- [ ] Select Service Desk type → fill in name "Test Desk" → click Create Space
- [ ] Redirect to ticket list — no blank screen, no error message
- [ ] Ticket list shows empty state — no "Failed" or "invalid" text
- [ ] Click "+ New Ticket" → modal opens
- [ ] Fill in title "Test Ticket" → priority defaults to Medium → click Create Ticket
- [ ] Modal closes — ticket appears in list
- [ ] Ticket shows correct title "Test Ticket"
- [ ] Ticket shows "Medium" priority — NOT "Unknown"
- [ ] Ticket shows "Open" status — NOT blank
- [ ] Click the ticket row → detail view opens
- [ ] Detail view shows ticket title and fields — no blank screen

---

## Wiki Space

- [ ] Go back to dashboard
- [ ] Create a Wiki space named "Test Wiki"
- [ ] Redirect to wiki — no blank screen, no error message
- [ ] Wiki shows empty state — no "Failed" or "invalid" text
- [ ] Click "+ New Page" → modal opens
- [ ] Fill in title "Test Page" → click Create Page
- [ ] Page appears in sidebar tree
- [ ] Click page in tree → page content area loads

---

## Project Space

- [ ] Go back to dashboard
- [ ] Create a Project space named "Test Project"
- [ ] Redirect to backlog — no blank screen, no error message
- [ ] Backlog shows empty state — no "Failed" or "invalid" text
- [ ] Click "+ Create Item" → modal opens
- [ ] Fill in title "Test Item" → type defaults to Task → click Create Item
- [ ] Item appears in backlog
- [ ] Item shows correct priority — NOT "Unknown"
- [ ] Click the item row → detail view opens

---

## Navigation

- [ ] Navigate between all three spaces — no blank screens
- [ ] "Back to Dashboard" link works from each module
- [ ] Direct URL navigation works (type `/settings` in browser → settings loads)
- [ ] Dark/light mode toggle in top right works
- [ ] Mode persists after page refresh

---

## Auth

- [ ] Click user avatar → dropdown shows Profile, Settings, Logout
- [ ] Click Logout → redirected to `/login`
- [ ] Login again with same credentials → dashboard loads

---

## API Spot Checks

Open browser dev tools → Network tab, then:

- [ ] `GET /health` returns 200 with `application/json`
- [ ] `GET /api/v1/me` returns 200 with user and org fields
- [ ] All `/api/v1/...` requests return `application/json` — never `text/html`
- [ ] Frontend routes (`/settings`, `/spaces/...`) return `text/html`
- [ ] No console errors on dashboard, ticket list, wiki, or backlog

---

## Release Gate

- [ ] Every item above is checked ✓
- [ ] Zero console errors observed during testing
- [ ] Zero "Failed", "invalid", "Unknown", or blank screens observed

**Only tag the release if every item above is checked.**
If any item fails: fix it, re-run from Setup, then tag.
