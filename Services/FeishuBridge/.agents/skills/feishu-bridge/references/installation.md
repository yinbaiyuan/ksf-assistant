# Installation routing

Use this reference when the bridge is missing, the skill wrapper cannot locate it, or the user asks to install, upgrade, or uninstall the bridge or skill.

1. Resolve the project root:

   ```bash
   node <skill-directory>/scripts/bridge.js --project-root
   ```

2. Read `<project-root>/docs/installation.md` completely before changing the host. It is the canonical macOS and Windows installation guide.
3. Use `<project-root>/scripts/install-requirements.js` for the exact current Feishu scopes and EventKeys. Do not copy an old permission list from memory.
4. Reuse existing Feishu apps by default. Use `npm run bridge -- auth configure-existing --payload-file -` only when the user can provide App ID/App Secret through a local secure stdin or private file path; do not ask the user to paste secrets into chat.
5. Use `npm run bridge -- auth start-user` for QR-code user OAuth after the existing app is configured. Return the QR path or verification URL to the user, then use `npm run bridge -- auth finish-user` after the user confirms OAuth.
6. Do not run `auth start-config --create-new` unless the user explicitly asks to create a new Feishu CLI app. Plain `auth start-config` must fail closed if no existing profile, Agent binding, or local secure import exists.
7. Keep secrets out of prompts, command arguments, versioned files, and logs. Let lark-cli use its secure profile; on Windows use the repository DPAPI helper for the official SDK credential only when the current lark-cli profile cannot be reused by the bridge.
8. Use `npm run skill:install` from the bridge repository for USER scope. The repository copy under `.agents/skills/feishu-bridge` is automatically REPO scoped.
9. After installation, run `npm run bridge -- capabilities`, `npm run bridge -- permissions`, and `npm run bridge -- doctor`. Do not describe the install as ready while required checks fail.

If `--project-root` fails, the skill may have been copied manually without `installation.json`. Find or clone the bridge repository, then rerun `npm run skill:install` from that repository.
