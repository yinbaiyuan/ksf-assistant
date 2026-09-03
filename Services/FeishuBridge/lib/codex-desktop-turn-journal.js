const fs = require('node:fs');
const path = require('node:path');
const { publicPlanTextFromItem, publicUserMessageText } = require('./codex-task-control');

const DEFAULT_INITIAL_TAIL_BYTES = 16 * 1024 * 1024;

function validThreadId(threadId) {
  return /^[A-Za-z0-9-]+$/.test(String(threadId || ''));
}

function findRolloutPath(codexHome, threadId) {
  if (!codexHome || !validThreadId(threadId)) return '';
  const suffix = `-${threadId}.jsonl`;
  const roots = ['sessions', 'archived_sessions'].map((name) => path.join(codexHome, name));
  for (const root of roots) {
    if (!fs.existsSync(root)) continue;
    const pending = [root];
    while (pending.length) {
      const directory = pending.pop();
      let entries;
      try {
        entries = fs.readdirSync(directory, { withFileTypes: true });
      } catch {
        continue;
      }
      for (const entry of entries) {
        const entryPath = path.join(directory, entry.name);
        if (entry.isDirectory()) {
          pending.push(entryPath);
        } else if (entry.isFile() && entry.name.endsWith(suffix)) {
          return entryPath;
        }
      }
    }
  }
  return '';
}

function eventTurnId(payload) {
  return String(
    payload?.turn_id
      || payload?.turnId
      || payload?.internal_chat_message_metadata_passthrough?.turn_id
      || '',
  );
}

function assistantContentText(payload) {
  if (payload?.type !== 'message' || payload?.role !== 'assistant') return '';
  return (Array.isArray(payload.content) ? payload.content : [])
    .filter((item) => item?.type === 'output_text' && item.text)
    .map((item) => item.text)
    .join('')
    .trim();
}

function boundedString(value, maximum) {
  return String(value || '').trim().slice(0, maximum);
}

function desktopUserInputRequest(payload) {
  if (payload?.type !== 'function_call' || payload?.name !== 'request_user_input') return null;
  const requestId = boundedString(payload.call_id, 240);
  if (!requestId) return null;
  let input;
  try {
    input = typeof payload.arguments === 'string'
      ? JSON.parse(payload.arguments)
      : payload.arguments;
  } catch {
    return null;
  }
  const questions = (Array.isArray(input?.questions) ? input.questions : [])
    .slice(0, 3)
    .map((question) => {
      const id = boundedString(question?.id, 120);
      const isSecret = Boolean(question?.isSecret);
      if (!id) return null;
      if (isSecret) return { id, header: '', question: '', options: [], isSecret: true };
      return {
        id,
        header: boundedString(question?.header, 120),
        question: boundedString(question?.question, 2000),
        options: (Array.isArray(question?.options) ? question.options : [])
          .slice(0, 3)
          .map((option) => ({
            label: boundedString(option?.label, 160),
            description: boundedString(option?.description, 500),
          }))
          .filter((option) => option.label),
        isOther: Boolean(question?.isOther),
        isSecret: false,
      };
    })
    .filter(Boolean);
  if (!questions.length) return null;
  return { requestId, questions };
}

function applyJournalRecord(latest, record) {
  const payload = record?.payload || {};
  const turnId = eventTurnId(payload);
  if (!turnId) return latest;
  const timestamp = String(record.timestamp || '');

  if (record.type === 'event_msg' && payload.type === 'task_started') {
    return {
      turnId,
      state: 'running',
      finalText: '',
      lastMessage: '',
      lastMessagePhase: '',
      lastUserMessage: '',
      planText: '',
      model: '',
      effort: null,
      collaborationMode: null,
      pendingInput: null,
      reason: '',
      startedAt: timestamp,
      completedAt: '',
      updatedAt: timestamp,
    };
  }

  if (!latest || latest.turnId !== turnId) return latest;

  if (record.type === 'turn_context') {
    return {
      ...latest,
      model: String(payload.model || payload.collaboration_mode?.settings?.model || ''),
      effort: payload.effort ?? payload.collaboration_mode?.settings?.reasoning_effort ?? null,
      collaborationMode: payload.collaboration_mode || null,
      updatedAt: timestamp,
    };
  }

  if (record.type === 'response_item') {
    const pendingInput = desktopUserInputRequest(payload);
    if (pendingInput) {
      return {
        ...latest,
        state: 'waiting_input',
        pendingInput: { ...pendingInput, requestedAt: timestamp },
        updatedAt: timestamp,
      };
    }
    if (payload.type === 'function_call_output'
      && latest.pendingInput?.requestId
      && String(payload.call_id || '') === latest.pendingInput.requestId) {
      return {
        ...latest,
        state: 'running',
        pendingInput: null,
        updatedAt: timestamp,
      };
    }
  }

  if (record.type === 'response_item' && payload.type === 'message' && payload.role === 'user') {
    const message = publicUserMessageText(payload);
    if (!message) return latest;
    return {
      ...latest,
      lastUserMessage: message,
      updatedAt: timestamp,
    };
  }

  if (record.type === 'event_msg' && payload.type === 'agent_message') {
    const message = String(payload.message || '').trim();
    if (!message) return latest;
    return {
      ...latest,
      finalText: String(payload.phase || '').toLowerCase() === 'final_answer'
        ? message : latest.finalText,
      lastMessage: message,
      lastMessagePhase: String(payload.phase || '').toLowerCase(),
      updatedAt: timestamp,
    };
  }

  if (record.type === 'event_msg' && payload.type === 'item_completed') {
    const planText = publicPlanTextFromItem(payload.item);
    if (!planText) return latest;
    return {
      ...latest,
      planText,
      updatedAt: timestamp,
    };
  }

  if (record.type === 'response_item') {
    const message = assistantContentText(payload);
    if (!message) return latest;
    return {
      ...latest,
      finalText: String(payload.phase || '').toLowerCase() === 'final_answer'
        ? message : latest.finalText,
      lastMessage: message,
      lastMessagePhase: String(payload.phase || '').toLowerCase(),
      updatedAt: timestamp,
    };
  }

  if (record.type === 'event_msg' && payload.type === 'task_complete') {
    const finalText = String(payload.last_agent_message || latest.finalText || '').trim();
    return {
      ...latest,
      state: 'completed',
      finalText,
      lastMessage: finalText || latest.lastMessage,
      lastMessagePhase: finalText ? 'final_answer' : latest.lastMessagePhase,
      reason: '',
      pendingInput: null,
      completedAt: timestamp,
      updatedAt: timestamp,
    };
  }

  if (record.type === 'event_msg' && payload.type === 'turn_aborted') {
    const reason = String(payload.reason || 'interrupted');
    const interrupted = /interrupt|cancel|user.stop/i.test(reason);
    return {
      ...latest,
      state: interrupted ? 'interrupted' : 'failed',
      reason,
      pendingInput: null,
      completedAt: timestamp,
      updatedAt: timestamp,
    };
  }

  return latest;
}

class CodexDesktopTurnJournal {
  constructor({ codexHome, initialTailBytes = DEFAULT_INITIAL_TAIL_BYTES } = {}) {
    this.codexHome = codexHome;
    this.initialTailBytes = initialTailBytes;
    this.entries = new Map();
  }

  createEntry(threadId) {
    const filePath = findRolloutPath(this.codexHome, threadId);
    if (!filePath) return null;
    const size = fs.statSync(filePath).size;
    const offset = Math.max(0, size - this.initialTailBytes);
    return {
      filePath,
      offset,
      remainder: Buffer.alloc(0),
      discardFirstLine: offset > 0,
      latest: null,
    };
  }

  entry(threadId) {
    if (!validThreadId(threadId)) return null;
    if (!this.entries.has(threadId)) this.entries.set(threadId, this.createEntry(threadId));
    let entry = this.entries.get(threadId);
    if (!entry) {
      entry = this.createEntry(threadId);
      this.entries.set(threadId, entry);
    }
    return entry;
  }

  snapshot(threadId) {
    const entry = this.entry(threadId);
    if (!entry) return null;
    let stat;
    try {
      stat = fs.statSync(entry.filePath);
    } catch {
      this.entries.delete(threadId);
      return null;
    }
    if (stat.size < entry.offset) {
      entry.offset = 0;
      entry.remainder = Buffer.alloc(0);
      entry.discardFirstLine = false;
      entry.latest = null;
    }
    if (stat.size > entry.offset) {
      const length = stat.size - entry.offset;
      const handle = fs.openSync(entry.filePath, 'r');
      const appended = Buffer.allocUnsafe(length);
      try {
        let bytesRead = 0;
        while (bytesRead < length) {
          const count = fs.readSync(
            handle,
            appended,
            bytesRead,
            length - bytesRead,
            entry.offset + bytesRead,
          );
          if (!count) break;
          bytesRead += count;
        }
        if (bytesRead < length) throw new Error('Codex Desktop rollout ended during read');
      } finally {
        fs.closeSync(handle);
      }
      entry.offset = stat.size;
      const data = Buffer.concat([entry.remainder, appended]);
      const lines = [];
      let start = 0;
      for (let index = 0; index < data.length; index += 1) {
        if (data[index] !== 0x0a) continue;
        lines.push(data.subarray(start, index));
        start = index + 1;
      }
      entry.remainder = data.subarray(start);
      if (entry.discardFirstLine && lines.length) {
        lines.shift();
        entry.discardFirstLine = false;
      }
      for (const line of lines) {
        if (!line.length) continue;
        try {
          entry.latest = applyJournalRecord(entry.latest, JSON.parse(line.toString('utf8')));
        } catch {
          // A malformed historical row must not break task control.
        }
      }
    }
    return entry.latest ? { ...entry.latest } : null;
  }

  watch(threadId, onChange, { debounceMs = 120 } = {}) {
    if (typeof onChange !== 'function') throw new TypeError('journal watcher requires a callback');
    const entry = this.entry(threadId);
    if (!entry) return null;
    let closed = false;
    let timer = null;
    let watcher = null;
    const deliver = () => {
      timer = null;
      if (closed) return;
      try {
        onChange(this.snapshot(threadId));
      } catch {
        // Periodic reconciliation remains the recovery path for transient journal errors.
      }
    };
    try {
      watcher = fs.watch(entry.filePath, { persistent: false }, () => {
        if (timer) clearTimeout(timer);
        timer = setTimeout(deliver, debounceMs);
      });
    } catch {
      return null;
    }
    return () => {
      if (closed) return;
      closed = true;
      if (timer) clearTimeout(timer);
      watcher?.close();
    };
  }
}

module.exports = {
  CodexDesktopTurnJournal,
  applyJournalRecord,
  desktopUserInputRequest,
  findRolloutPath,
};
