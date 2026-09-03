const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { publicUserMessageText } = require('../lib/codex-task-control');
const {
  KSFProjectPromotionClient,
  ProjectPromotionError,
  defaultRootCandidates,
  parseProjectContinuationIntent,
  projectContinuationPrompt,
  promotedTaskTitle,
  selectProject,
  validatePanelBridgeRoot,
} = require('../lib/ksf-project-promotion');

function temporaryKSFRoot(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ksf-project-promotion-'));
  const script = path.join(
    root,
    '.agents',
    'skills',
    'ksf-load-route-context',
    'scripts',
    'ksf_panel_bridge.rb',
  );
  fs.mkdirSync(path.dirname(script), { recursive: true });
  fs.writeFileSync(script, '# test bridge\n', { mode: 0o600 });
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  return { root, script };
}

const projects = [
  {
    id: '10项目/飞书桥/项目记忆卡.md',
    name: '飞书桥',
    cardPath: '/KSF/10项目/飞书桥/项目记忆卡.md',
    status: 'active',
  },
  {
    id: '10项目/3D家庭展示项目/项目记忆卡.md',
    name: '3D家庭展示项目',
    cardPath: '/KSF/10项目/3D家庭展示项目/项目记忆卡.md',
    status: 'active',
  },
];

test('project continuation intent only captures explicit continuation requests', () => {
  assert.deepEqual(parseProjectContinuationIntent('我需要继续完成飞书桥项目。'), {
    projectQuery: '飞书桥项目',
    originalText: '我需要继续完成飞书桥项目。',
  });
  assert.deepEqual(parseProjectContinuationIntent('接下来想继续推进 3D家庭展示项目'), {
    projectQuery: '3D家庭展示项目',
    originalText: '接下来想继续推进 3D家庭展示项目',
  });
  assert.equal(parseProjectContinuationIntent('介绍一下飞书桥项目'), null);
  assert.equal(parseProjectContinuationIntent('继续聊刚才的话题'), null);
});

test('project selection accepts an optional trailing 项目 without fuzzy guessing', () => {
  assert.equal(selectProject(projects, '飞书桥项目').project.name, '飞书桥');
  assert.equal(selectProject(projects, '3D家庭展示').project.name, '3D家庭展示项目');
  assert.equal(selectProject(projects, '不存在').status, 'not_found');
  assert.equal(selectProject([
    ...projects,
    { ...projects[0], id: 'duplicate', name: '飞书桥项目' },
  ], '飞书桥').status, 'ambiguous');
});

test('project bridge root must contain a private regular script owned by this user', (t) => {
  const { root, script } = temporaryKSFRoot(t);
  assert.equal(validatePanelBridgeRoot(root).scriptPath, fs.realpathSync(script));
  fs.chmodSync(script, 0o622);
  assert.equal(validatePanelBridgeRoot(root, { platform: 'darwin' }), null);
  assert.equal(validatePanelBridgeRoot(root, { platform: 'win32' }).scriptPath, fs.realpathSync(script));
  assert.deepEqual(defaultRootCandidates({
    KSF_PROJECT_ROOT: '/one',
    KMS_ROOT: '/two',
  }, '/home/test'), ['/one', '/two', path.join('/home/test', 'Documents', 'KSF')]);
});

test('client reads the catalog and verifies the exact thread projection', async (t) => {
  const { root, script } = temporaryKSFRoot(t);
  const calls = [];
  const client = new KSFProjectPromotionClient({
    rootPath: root,
    env: { KSF_PROJECT_RUBY: '/test/ruby' },
    runner: async (input) => {
      calls.push(input);
      if (input.args.includes('--export-catalog')) {
        return JSON.stringify({ protocol: 'ksf-panel-catalog-v1', projects });
      }
      return JSON.stringify({
        protocol: 'ksf-task-project-resolution-v1',
        projections: [{
          threadId: '01a05763-5e97-70f3-8010-755677942a6b',
          projection: { bindings: [{ projectCard: projects[0].id }] },
        }],
      });
    },
  });

  const resolved = await client.resolveIntent('我需要继续完成飞书桥项目');
  assert.equal(resolved.project.id, projects[0].id);
  const binding = await client.verifyBinding(
    '01a05763-5e97-70f3-8010-755677942a6b',
    projects[0].id,
  );
  assert.equal(binding.projectCard, projects[0].id);
  const current = await client.currentProject('01a05763-5e97-70f3-8010-755677942a6b');
  assert.equal(current.project.id, projects[0].id);
  assert.equal(current.binding.projectCard, projects[0].id);
  assert.equal(calls[0].command, '/test/ruby');
  assert.deepEqual(calls[0].args, [
    fs.realpathSync(script),
    '--root',
    fs.realpathSync(root),
    '--export-catalog',
  ]);
  assert.equal(calls[1].input, JSON.stringify({
    threadIds: ['01a05763-5e97-70f3-8010-755677942a6b'],
  }));
  assert.equal(calls[2].input, JSON.stringify({
    threadIds: ['01a05763-5e97-70f3-8010-755677942a6b'],
  }));
  assert.ok(calls[3].args.includes('--export-catalog'));
});

test('client rejects unknown projects and unverified project bindings', async (t) => {
  const { root } = temporaryKSFRoot(t);
  const client = new KSFProjectPromotionClient({
    rootPath: root,
    runner: async ({ args }) => args.includes('--export-catalog')
      ? JSON.stringify({ protocol: 'ksf-panel-catalog-v1', projects })
      : JSON.stringify({
          protocol: 'ksf-task-project-resolution-v1',
          projections: [{ threadId: 'thread-1', projection: { bindings: [] } }],
        }),
  });

  await assert.rejects(
    client.resolveIntent('我需要继续完成不存在项目'),
    (error) => error instanceof ProjectPromotionError && error.code === 'project_not_found',
  );
  await assert.rejects(
    client.verifyBinding('thread-1', projects[0].id),
    (error) => error instanceof ProjectPromotionError && error.code === 'project_binding_missing',
  );
  assert.equal(await client.currentProject('thread-1'), null);
});

test('promotion prompt performs context binding only and keeps the public user message last', () => {
  const prompt = projectContinuationPrompt(projects[0], '我需要继续完成飞书桥项目');
  assert.match(prompt, /项目记忆卡：\/KSF\/10项目\/飞书桥\/项目记忆卡\.md/);
  assert.match(prompt, /不要修改文件，不要生成实施方案/);
  assert.match(prompt, /## My request:\n我需要继续完成飞书桥项目$/);
  assert.equal(publicUserMessageText({
    type: 'message',
    role: 'user',
    content: [{ type: 'input_text', text: prompt }],
  }), '我需要继续完成飞书桥项目');
  assert.equal(promotedTaskTitle('飞书桥'), '飞书桥 · 飞书任务');
  assert.ok(promotedTaskTitle('很长'.repeat(80)).length <= 64);
});
