const packageManifest = require('../package.json');

const RUNTIME_MANIFEST = Object.freeze({
  bridgeVersion: '1.0.0',
  packageVersion: packageManifest.version,
  capabilityVersion: '1.0.0',
  stabilityBaselineVersion: '0.6.1',
  queueStateSchemaVersion: 2,
  skillCompatibility: '1.0.x',
  larkCliVersion: packageManifest.dependencies['@larksuite/cli'],
  officialSdkVersion: packageManifest.dependencies['@larksuiteoapi/node-sdk'],
});

function runtimeManifest() {
  return { ...RUNTIME_MANIFEST };
}

module.exports = {
  RUNTIME_MANIFEST,
  runtimeManifest,
};
