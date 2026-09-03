const assert = require('node:assert/strict');
const test = require('node:test');
const {
  REQUIRED_SCOPES,
  capabilityManifest,
  compareScopes,
  permissionReport,
} = require('../lib/capability-policy');

test('capability manifest is versioned and excludes broad destructive operations', () => {
  const manifest = capabilityManifest();
  assert.equal(manifest.bridgeVersion, '1.0.0');
  assert.equal(manifest.capabilityVersion, '1.0.0');
  assert.equal(manifest.packageVersion, '1.2.0');
  assert.equal(manifest.skillCompatibility, '1.0.x');
  assert.equal(manifest.stabilityBaselineVersion, '0.6.1');
  assert.equal(manifest.queueStateSchemaVersion, 2);
  assert.ok(manifest.events.includes('card.action.trigger'));
  assert.equal(manifest.eventTransport, 'official-sdk');
  assert.equal(manifest.singleInboundConnection, true);
  assert.ok(manifest.inboundMessageTypes.includes('file'));
  assert.ok(manifest.intentionallyExcluded.includes('arbitrary_openapi'));
  assert.equal(manifest.queuedWriteCapabilities.some((action) => action.includes('delete')), false);
});

test('permission comparison reports missing and excess scopes without treating excess as capability', () => {
  assert.deepEqual(compareScopes(['required:a', 'required:b'], ['required:a', 'extra:x']), {
    requiredCount: 2,
    grantedCount: 2,
    missing: ['required:b'],
    excess: ['extra:x'],
    complete: false,
  });
  const report = permissionReport({
    authStatus: {
      verified: true,
      identities: {
        bot: { available: true, verified: true },
        user: {
          available: true,
          verified: true,
          scope: [...REQUIRED_SCOPES.user, 'space:document:delete'].join(' '),
        },
      },
    },
    appScopes: { userScopes: [...REQUIRED_SCOPES.user, 'space:document:delete'] },
  });
  assert.equal(report.identities.user.complete, true);
  assert.deepEqual(report.identities.user.excess, ['space:document:delete']);
  assert.equal(report.identities.user.application.complete, true);
  assert.equal(report.identities.user.oauth.complete, true);
  assert.equal(report.identities.bot.scopeVerification, 'not_exposed_by_lark_cli_auth_scopes');
});

test('permission comparison requires both the application and OAuth layers', () => {
  const required = [...REQUIRED_SCOPES.user];
  const missingOauth = required.at(-1);
  const report = permissionReport({
    authStatus: {
      verified: true,
      identities: {
        bot: { available: true, verified: true },
        user: {
          available: true,
          verified: true,
          scope: required.slice(0, -1).join(' '),
        },
      },
    },
    appScopes: { userScopes: required },
  });
  assert.deepEqual(report.identities.user.missing, [missingOauth]);
  assert.equal(report.identities.user.application.complete, true);
  assert.deepEqual(report.identities.user.oauth.missing, [missingOauth]);
  assert.equal(report.identities.user.complete, false);
});
