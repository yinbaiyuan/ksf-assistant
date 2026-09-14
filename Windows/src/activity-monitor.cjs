'use strict';

// Lives in the main process: hidden/throttled renderers must not own tray state.
function startActivityMonitor({ readRevision, refresh, stopped, schedule = setTimeout }) {
  let revision;
  let cancelled = false;
  async function tick() {
    if (cancelled || stopped()) return;
    try {
      const next = await readRevision();
      if (next !== revision && !cancelled && !stopped()) {
        await refresh();
        revision = next;
      }
    } catch { revision = undefined; }
    if (!cancelled && !stopped()) schedule(tick, 1000);
  }
  schedule(tick, 1000);
  return () => { cancelled = true; };
}

module.exports = { startActivityMonitor };
