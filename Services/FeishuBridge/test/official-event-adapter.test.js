const assert = require('node:assert/strict');
const test = require('node:test');
const {
  cardAwareWsClient,
  createOfficialEventAdapter,
  handleOfficialDataFrame,
  immediateCardResponse,
} = require('../lib/official-event-adapter');

function dataFrame(type = 'card') {
  return {
    headers: [
      { key: 'type', value: type },
      { key: 'message_id', value: 'frame-one' },
      { key: 'sum', value: '1' },
      { key: 'seq', value: '0' },
      { key: 'trace_id', value: 'trace-one' },
    ],
    payload: Buffer.from('{}'),
  };
}

test('immediate card acknowledgement does not claim the background action succeeded', () => {
  assert.deepEqual(immediateCardResponse(), {
    toast: {
      type: 'info',
      content: '请求已接收，结果会更新到卡片',
    },
  });
});

test('official transport compatibility layer dispatches card frames and sends an immediate response frame', async () => {
  const sent = [];
  const client = {
    dataCache: { mergeData: (item) => item },
    eventDispatcher: { invoke: async () => immediateCardResponse() },
    logger: { error() {} },
    sendMessage: (frame) => sent.push(frame),
  };
  assert.equal(await handleOfficialDataFrame(client, dataFrame('card')), true);
  assert.equal(sent.length, 1);
  const payload = JSON.parse(Buffer.from(sent[0].payload).toString('utf8'));
  assert.equal(payload.code, 200);
  assert.deepEqual(
    JSON.parse(Buffer.from(payload.data, 'base64').toString('utf8')),
    immediateCardResponse(),
  );
  assert.match(sent[0].headers.at(-1).value, /^\d+$/);
});

test('compatibility layer preserves ordinary events and ignores unrelated frame types', async () => {
  let invoked = 0;
  const sent = [];
  const client = {
    dataCache: { mergeData: (item) => item },
    eventDispatcher: { invoke: async () => { invoked += 1; } },
    logger: { error() {} },
    sendMessage: (frame) => sent.push(frame),
  };
  assert.equal(await handleOfficialDataFrame(client, dataFrame('event')), true);
  assert.equal(await handleOfficialDataFrame(client, dataFrame('ping')), false);
  assert.equal(invoked, 1);
  assert.equal(sent.length, 1);
});

test('dispatcher failures produce one failure response without leaking the thrown value', async () => {
  const sent = [];
  let errorLogs = 0;
  const client = {
    dataCache: { mergeData: (item) => item },
    eventDispatcher: { invoke: async () => { throw new Error('private-callback-value'); } },
    logger: { error: () => { errorLogs += 1; } },
    sendMessage: (frame) => sent.push(frame),
  };
  await handleOfficialDataFrame(client, dataFrame('card'));
  const payloadText = Buffer.from(sent[0].payload).toString('utf8');
  assert.equal(JSON.parse(payloadText).code, 500);
  assert.equal(payloadText.includes('private-callback-value'), false);
  assert.equal(errorLogs, 1);
});

test('adapter uses one WS client for messages and card callbacks and detaches business handlers', async () => {
  class FakeDispatcher {
    constructor() { this.handlers = {}; }
    register(handlers) { Object.assign(this.handlers, handlers); return this; }
  }
  class FakeWsClient {
    static instance;
    constructor(options) {
      this.options = options;
      FakeWsClient.instance = this;
    }
    handleEventData() {}
    start({ eventDispatcher }) {
      this.dispatcher = eventDispatcher;
      this.options.onReady();
    }
    close() { this.closed = true; }
    getConnectionStatus() { return { state: 'connected' }; }
  }
  const scheduled = [];
  const handled = [];
  const sdk = {
    EventDispatcher: FakeDispatcher,
    WSClient: FakeWsClient,
    LoggerLevel: { error: 'error' },
    Domain: { Feishu: 'feishu', Lark: 'lark' },
  };
  const adapter = createOfficialEventAdapter({
    credentials: { appId: 'app-id', appSecret: 'app-secret', brand: 'feishu' },
    eventKeys: ['im.message.receive_v1', 'card.action.trigger'],
    handlers: {
      'im.message.receive_v1': async () => handled.push('message'),
      'card.action.trigger': async () => handled.push('card'),
    },
    scheduler: (task) => scheduled.push(task),
    sdk,
  });
  await adapter.start();
  assert.ok(FakeWsClient.instance instanceof FakeWsClient);
  assert.notEqual(FakeWsClient.instance.handleEventData, FakeWsClient.prototype.handleEventData);
  assert.equal(Object.keys(FakeWsClient.instance.dispatcher.handlers).length, 2);
  const response = FakeWsClient.instance.dispatcher.handlers['card.action.trigger']({});
  assert.deepEqual(response, immediateCardResponse());
  assert.deepEqual(handled, []);
  scheduled.shift()();
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(handled, ['card']);
  await adapter.stop();
  assert.equal(FakeWsClient.instance.closed, true);
});

test('official adapter refuses to run when SDK internals no longer expose the reviewed hook', () => {
  assert.throws(() => cardAwareWsClient(class {}), /needs review/);
});

test('production bridge does not spawn lark-cli event consumers in parallel', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const source = fs.readFileSync(path.join(__dirname, '..', 'bot-bridge.js'), 'utf8');
  assert.doesNotMatch(source, /['"]event['"],\s*['"]consume['"]/);
  assert.doesNotMatch(source, /Starting lark-cli event consumer/);
});
