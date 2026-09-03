const OFFICIAL_EVENT_TRANSPORT = 'official-sdk';
const SUPPORTED_FRAME_TYPES = new Set(['event', 'card']);
const SUCCESS_STATUS_CODE = 200;
const FAILURE_STATUS_CODE = 500;

function headerMap(headers = []) {
  return headers.reduce((result, item) => {
    if (item?.key) result[item.key] = item.value;
    return result;
  }, {});
}

function responseFrame(data, payload, elapsedMs) {
  return {
    ...data,
    headers: [
      ...(Array.isArray(data.headers) ? data.headers : []),
      { key: 'biz_rt', value: String(Math.max(0, elapsedMs)) },
    ],
    payload: new TextEncoder().encode(JSON.stringify(payload)),
  };
}

async function handleOfficialDataFrame(client, data) {
  const headers = headerMap(data?.headers);
  const frameType = String(headers.type || '');
  if (!SUPPORTED_FRAME_TYPES.has(frameType)) return false;

  const merged = client.dataCache.mergeData({
    message_id: headers.message_id,
    sum: Number(headers.sum),
    seq: Number(headers.seq),
    trace_id: headers.trace_id,
    data: data.payload,
  });
  if (!merged) return false;

  const startedAt = Date.now();
  const payload = { code: SUCCESS_STATUS_CODE };
  try {
    const result = await client.eventDispatcher?.invoke(merged, { needCheck: false });
    if (result !== undefined && result !== null) {
      payload.data = Buffer.from(JSON.stringify(result)).toString('base64');
    }
  } catch (error) {
    payload.code = FAILURE_STATUS_CODE;
    client.logger?.error?.('[feishu-bridge] official event dispatcher failed');
  }
  client.sendMessage(responseFrame(data, payload, Date.now() - startedAt));
  return true;
}

function cardAwareWsClient(BaseClient) {
  if (typeof BaseClient?.prototype?.handleEventData !== 'function') {
    throw new Error('official SDK WSClient internals changed; card compatibility layer needs review');
  }
  return class CardAwareWsClient extends BaseClient {
    handleEventData(data) {
      return handleOfficialDataFrame(this, data);
    }
  };
}

function silentSdkLogger() {
  return {
    trace() {},
    debug() {},
    info() {},
    warn() {},
    error() {},
  };
}

function immediateCardResponse() {
  return {
    toast: {
      type: 'info',
      content: '请求已接收，结果会更新到卡片',
    },
  };
}

function createOfficialEventAdapter({
  credentials,
  eventKeys,
  handlers,
  onState = () => {},
  onEventState = () => {},
  onHandlerError = () => {},
  scheduler = setImmediate,
  sdk = require('@larksuiteoapi/node-sdk'),
  connectTimeoutMs = 20000,
} = {}) {
  if (!credentials?.appId || !credentials?.appSecret) {
    throw new Error('official SDK credentials are required');
  }
  const keys = [...new Set(eventKeys || [])];
  if (!keys.length) throw new Error('at least one inbound event key is required');
  const logger = silentSdkLogger();
  const dispatcher = new sdk.EventDispatcher({
    logger,
    loggerLevel: sdk.LoggerLevel.error,
  });

  const registered = {};
  for (const eventKey of keys) {
    if (typeof handlers?.[eventKey] !== 'function') {
      throw new Error(`missing inbound handler for ${eventKey}`);
    }
    registered[eventKey] = (data) => {
      onEventState(eventKey, {
        status: 'running',
        errorCode: '',
        lastReceivedAt: new Date().toISOString(),
      });
      scheduler(() => {
        Promise.resolve()
          .then(() => handlers[eventKey](data))
          .catch((error) => onHandlerError(eventKey, error));
      });
      return eventKey === 'card.action.trigger' ? immediateCardResponse() : undefined;
    };
  }
  dispatcher.register(registered);

  const Client = cardAwareWsClient(sdk.WSClient);
  let client = null;
  let connectPromise = null;

  function connectionState() {
    return client?.getConnectionStatus?.() || { state: client ? 'starting' : 'idle' };
  }

  async function start() {
    if (connectPromise) return connectPromise;
    onState({ status: 'starting', errorCode: '', connection: { state: 'connecting' } });
    connectPromise = new Promise((resolve, reject) => {
      let settled = false;
      const timer = setTimeout(() => {
        if (settled) return;
        settled = true;
        reject(new Error(`official SDK WebSocket handshake timed out after ${connectTimeoutMs}ms`));
      }, connectTimeoutMs);
      const settle = (callback, value) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        callback(value);
      };
      const domain = credentials.brand === 'lark' ? sdk.Domain.Lark : sdk.Domain.Feishu;
      client = new Client({
        appId: credentials.appId,
        appSecret: credentials.appSecret,
        domain,
        logger,
        loggerLevel: sdk.LoggerLevel.error,
        autoReconnect: true,
        handshakeTimeoutMs: Math.min(connectTimeoutMs, 15000),
        source: 'feishu-bot-bridge',
        onReady: () => {
          const connection = connectionState();
          onState({ status: 'connected', errorCode: '', connection });
          for (const eventKey of keys) {
            onEventState(eventKey, { status: 'running', errorCode: '', restartScheduled: false });
          }
          settle(resolve);
        },
        onError: (error) => {
          onState({ status: 'failed', errorCode: 'sdk_connection_error', connection: connectionState() });
          settle(reject, error);
        },
        onReconnecting: () => {
          onState({ status: 'reconnecting', errorCode: '', connection: connectionState() });
          for (const eventKey of keys) onEventState(eventKey, { status: 'reconnecting' });
        },
        onReconnected: () => {
          onState({ status: 'connected', errorCode: '', connection: connectionState() });
          for (const eventKey of keys) onEventState(eventKey, { status: 'running', errorCode: '' });
        },
      });
      client.start({ eventDispatcher: dispatcher });
    }).catch((error) => {
      connectPromise = null;
      throw error;
    });
    return connectPromise;
  }

  async function stop() {
    if (!client) return;
    try {
      client.close({ force: true });
    } finally {
      onState({ status: 'stopped', errorCode: '', connection: { state: 'idle' } });
      client = null;
      connectPromise = null;
    }
  }

  return {
    start,
    stop,
    status: connectionState,
    transport: OFFICIAL_EVENT_TRANSPORT,
  };
}

module.exports = {
  OFFICIAL_EVENT_TRANSPORT,
  cardAwareWsClient,
  createOfficialEventAdapter,
  handleOfficialDataFrame,
  immediateCardResponse,
};
