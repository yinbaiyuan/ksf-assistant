#!/usr/bin/env node

const { REQUIRED_SCOPES } = require('../lib/capability-policy');
const { FIXED_EVENT_KEYS } = require('../lib/event-inbox');
const { runtimeManifest } = require('../lib/runtime-manifest');

function parseArgs(argv) {
  const subject = argv[0] && !argv[0].startsWith('--') ? argv[0] : 'all';
  const rest = subject === 'all' && argv[0]?.startsWith('--') ? argv : argv.slice(1);
  const flags = {};
  for (let index = 0; index < rest.length; index += 1) {
    const token = rest[index];
    if (!token.startsWith('--')) throw new Error(`unexpected argument: ${token}`);
    const key = token.slice(2);
    const value = rest[index + 1];
    if (!value || value.startsWith('--')) throw new Error(`missing value for --${key}`);
    flags[key] = value;
    index += 1;
  }
  return { subject, flags };
}

function formatList(values, format) {
  if (format === 'json') return `${JSON.stringify(values, null, 2)}\n`;
  if (format === 'lines') return `${values.join('\n')}\n`;
  if (format === 'comma') return `${values.join(',')}\n`;
  if (format === 'space') return `${values.join(' ')}\n`;
  throw new Error('format must be json, lines, comma, or space');
}

function requirements(argv = process.argv.slice(2)) {
  const { subject, flags } = parseArgs(argv);
  const format = flags.format || 'json';
  if (subject === 'scopes') {
    const identity = flags.identity || 'all';
    if (identity === 'bot' || identity === 'user') return formatList(REQUIRED_SCOPES[identity], format);
    if (identity !== 'all') throw new Error('identity must be bot, user, or all');
    if (format !== 'json') {
      return formatList([...new Set([...REQUIRED_SCOPES.bot, ...REQUIRED_SCOPES.user])].sort(), format);
    }
    return `${JSON.stringify(REQUIRED_SCOPES, null, 2)}\n`;
  }
  if (subject === 'events') return formatList(FIXED_EVENT_KEYS, format);
  if (subject !== 'all') throw new Error('subject must be all, scopes, or events');
  if (format !== 'json') throw new Error('all supports only --format json');
  return `${JSON.stringify({
    runtime: runtimeManifest(),
    requiredScopes: REQUIRED_SCOPES,
    fixedEventKeys: FIXED_EVENT_KEYS,
  }, null, 2)}\n`;
}

if (require.main === module) {
  try {
    process.stdout.write(requirements());
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}

module.exports = { formatList, parseArgs, requirements };
