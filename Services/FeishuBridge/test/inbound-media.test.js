const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { assertPrivateMode } = require('./helpers/platform-private');
const {
  cleanupInboundAssets,
  inboundAssetsPrompt,
  inboundMessageDetails,
  stageInboundMessage,
} = require('../lib/inbound-media');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-inbound-media-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('inbound message parsing extracts bounded resources without exposing keys in prompts', () => {
  const post = inboundMessageDetails({
    message_type: 'post',
    content: JSON.stringify({
      title: '周报',
      content: [[
        { tag: 'text', text: '请总结' },
        { tag: 'img', image_key: 'img_private' },
        { tag: 'img', image_key: 'img_private' },
      ]],
    }),
  });
  assert.equal(post.text, '周报\n请总结');
  assert.equal(post.resources.length, 1);
  assert.equal(post.resources[0].resourceType, 'image');
  const prompt = inboundAssetsPrompt(post.text, {
    assets: [{ messageType: 'post', resourceType: 'image', displayName: 'image', localPath: '/tmp/image', sizeBytes: 3 }],
  });
  assert.match(prompt, /\/tmp\/image/);
  assert.doesNotMatch(prompt, /img_private/);
});

test('inbound resources are downloaded to a private scoped directory and cleaned after use', async (t) => {
  const cwd = temporaryDirectory(t);
  const calls = [];
  const lark = {
    async larkImDownloadMessageResource(input) {
      calls.push(input);
      const output = path.join(cwd, input.output);
      fs.writeFileSync(output, 'private attachment');
      return {};
    },
  };
  const staged = await stageInboundMessage({
    lark,
    cwd,
    message: {
      message_id: 'om_private_message',
      message_type: 'file',
      content: JSON.stringify({ file_key: 'file_private_key', file_name: '../../report.txt' }),
    },
  });
  assert.equal(staged.assets.length, 1);
  assert.equal(staged.assets[0].displayName, 'report.txt');
  assert.equal(staged.assets[0].localPath.startsWith(path.join(cwd, 'node_modules', '.cache')), true);
  assertPrivateMode(staged.cleanupDir, 0o700);
  assertPrivateMode(staged.assets[0].localPath, 0o600);
  assert.equal(calls[0].fileKey, 'file_private_key');
  assert.equal(calls[0].output.includes('..'), false);
  cleanupInboundAssets(cwd, staged.cleanupDir);
  assert.equal(fs.existsSync(staged.cleanupDir), false);
});

test('inbound size limits fail closed and remove partial downloads', async (t) => {
  const cwd = temporaryDirectory(t);
  const lark = {
    async larkImDownloadMessageResource(input) {
      fs.writeFileSync(path.join(cwd, input.output), 'too large');
      return {};
    },
  };
  await assert.rejects(() => stageInboundMessage({
    lark,
    cwd,
    maxBytes: 4,
    message: {
      message_id: 'om_large',
      message_type: 'image',
      content: JSON.stringify({ image_key: 'img_large' }),
    },
  }), /size limit/);
  const root = path.join(cwd, 'node_modules', '.cache', 'feishu-bridge', 'inbound-assets');
  assert.deepEqual(fs.existsSync(root) ? fs.readdirSync(root) : [], []);
});
