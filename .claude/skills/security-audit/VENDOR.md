# Vendored: cloudflare/security-audit-skill

This directory is a **pinned, in-repo copy** of the Cloudflare security-audit skill,
vendored so the deep audit runs consistently for every contributor with no per-machine
install (matching this repo's offline-first, in-repo-tooling ethos).

- Upstream: https://github.com/cloudflare/security-audit-skill (MIT, see `LICENSE`)
- Vendored: 2026-06-24
- Files: `SKILL.md`, `RECONNAISSANCE.md`, `HUNTING.md`, `ATTACK-CLASSES.md`,
  `VALIDATION-AND-REPORTING.md`, `report-schema.json`, `validate-findings.cjs`

## Refreshing

This is a static copy and can drift from upstream. To refresh, re-vendor the files
from the upstream `main`, or install the maintained version with the Skills CLI:

```
npx skills add https://github.com/cloudflare/security-audit-skill --skill security-audit
```

## When it runs (see docs/review-automation.md)

The deep multi-phase audit is **not** a per-push gate — it's heavy (parallel sub-agents,
six phases). It runs **pre-release** (RELEASE_CHECKLIST.md) and **on demand** via
`/security-audit`. The fast per-diff checkin gate stays the Security Guidance review
(`/security-review`).
