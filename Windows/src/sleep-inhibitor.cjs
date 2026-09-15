'use strict';

// The Electron host owns this native request; no shell or system policy changes.
class SleepInhibitor {
  constructor(powerSaveBlocker) { this.blocker = powerSaveBlocker; this.id = null; }
  setEnabled(enabled) {
    if (enabled) {
      if (this.id !== null) return;
      const id = this.blocker.start('prevent-app-suspension');
      if (!this.blocker.isStarted(id)) {
        this.blocker.stop(id);
        throw new Error('无法启用防睡眠，请重试。');
      }
      this.id = id;
    } else if (this.id !== null) {
      this.blocker.stop(this.id);
      this.id = null;
    }
  }
}
module.exports = { SleepInhibitor };
