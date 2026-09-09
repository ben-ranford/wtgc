#!/usr/bin/env node
// Verify the statically loaded trusted workflow handler with mocked GitHub REST calls.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const { trackSuppressions } = require('./suppression-accountability.js');
const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));

const workflow = fs.readFileSync(path.join(scriptDirectory, '..', '.github', 'workflows', 'suppression-accountability.yml'), 'utf8');
if (!workflow.includes('pull_request_target:')) throw new Error('workflow must retain its trusted trigger');
if (!workflow.includes('branches: [main]')) throw new Error('workflow must restrict trusted execution to main');
const checkout = workflow.match(/- name: Checkout trusted workflow source[\s\S]*?(?=\n      - name:|$)/)?.[0];
if (!checkout) throw new Error('workflow must retain the trusted checkout step');
if (/^\s*(ref|repository):/m.test(checkout)) throw new Error('trusted checkout must use the protected default workflow source');
if (!checkout.includes('persist-credentials: false')) throw new Error('trusted checkout must not retain credentials');
if (/allow-unsafe-pr-checkout:\s*true|github\.event\.pull_request\.(head|merge)/.test(checkout)) throw new Error('trusted checkout must not select PR-controlled source');
if (!workflow.includes("require('${{ github.workspace }}/scripts/suppression-accountability.js')")) throw new Error('workflow must load the static accountability handler');
const release = fs.readFileSync(path.join(scriptDirectory, '..', '.github', 'workflows', 'release.yml'), 'utf8');
const ci = fs.readFileSync(path.join(scriptDirectory, '..', '.github', 'workflows', 'ci.yml'), 'utf8');
const releasePlease = fs.readFileSync(path.join(scriptDirectory, '..', '.github', 'workflows', 'release-please.yml'), 'utf8');
const qualityArtifacts = ci.match(/- name: Upload quality-gate artifacts[\s\S]*?(?=\n      - name:|$)/)?.[0];
if (!qualityArtifacts) throw new Error('CI must retain its quality-gate artifact upload');
if (!/^\s*path:\s*\.artifacts\/\s*$/m.test(qualityArtifacts)) throw new Error('quality-gate upload must stay scoped to .artifacts');
if (!/^\s*include-hidden-files:\s*true\s*$/m.test(qualityArtifacts)) throw new Error('quality-gate upload must include the managed hidden artifact directory');
if (!/^\s*if-no-files-found:\s*error\s*$/m.test(qualityArtifacts)) throw new Error('quality-gate upload must fail when reports are missing');
if (!release.includes('issues: read')) throw new Error('release checks must declare the issue-read permission they use');
const releaseAssets = releasePlease.slice(releasePlease.indexOf('  release-assets:'));
if (!releaseAssets.includes('issues: read')) throw new Error('release caller must grant the callee issue-read permission');
for (const workflowSource of [ci, release]) {
  if (!workflowSource.includes('python3 -m venv "$uv_venv"')) throw new Error('CI must install uv in a runner-temp virtual environment');
  if (!workflowSource.includes('"$uv_venv/bin/uv" --version')) throw new Error('CI must verify the installed uv executable');
  if (!workflowSource.includes('echo "$uv_venv/bin" >> "$GITHUB_PATH"')) throw new Error('CI must expose only the virtual-environment uv path');
}

const patch = '@@ -0,0 +1 @@\n+package fixture //nosec G204';
const manifest = { suppressions: [{ location: 'fixture.go:1', rationale: 'fixture', owner: 'security', removal_condition: 'replace fixture', issue: 'https://github.com/example/repo/issues/7' }] };
const listFiles = () => undefined;
const listComments = () => undefined;
const listForRepo = () => undefined;

function mock(options = {}) {
  const calls = [];
  const github = {
    rest: {
      pulls: { listFiles },
      repos: { getContent: async () => ({ data: options.content || { encoding: 'base64', content: Buffer.from(JSON.stringify(manifest)).toString('base64') } }) },
      issues: {
        listComments, listForRepo,
        get: async (args) => calls.push(['get', args]),
        updateComment: async (args) => calls.push(['updateComment', args]),
        createComment: async (args) => calls.push(['createComment', args]),
        create: async (args) => { calls.push(['create', args]); return { data: { html_url: 'https://github.com/example/repo/issues/88' } }; },
        update: async (args) => calls.push(['update', args]),
      },
    },
    paginate: async (method) => {
      if (method === listFiles) return options.files || [{ filename: 'fixture.go', patch, changes: 1 }];
      if (method === listComments) return options.comments || [{ id: 9, body: '<!-- wtgc-suppression-accountability:fixture.go:1 --> old' }];
      if (method === listForRepo) return options.issues || [];
      throw new Error('unexpected pagination method');
    },
  };
  return { github, calls };
}

const context = { repo: { owner: 'example', repo: 'repo' }, payload: { pull_request: { number: 1, head: { sha: 'head' } } } };
async function expectReject(action, message) {
  try {
    await action();
  } catch (error) {
    if (String(error).includes(message)) {
      return;
    }
    throw error;
  }
  throw new Error(`expected failure containing ${message}`);
}

async function main() {
  const linked = mock();
  await trackSuppressions(linked.github, context);
  if (linked.calls.filter(([name]) => name === 'updateComment').length !== 1 || linked.calls.some(([name]) => name === 'create')) throw new Error('linked issue was not updated idempotently');
  const truncated = mock({ files: [{ filename: 'fixture.go', patch, changes: 2 }] });
  await expectReject(() => trackSuppressions(truncated.github, context), 'patch is missing or truncated');
  const missingLink = mock({ content: { encoding: 'base64', content: Buffer.from(JSON.stringify({ suppressions: [{ ...manifest.suppressions[0], issue: '' }] })).toString('base64') }, issues: [{ number: 12, title: 'Track static-analysis suppression: fixture.go:1', html_url: 'https://github.com/example/repo/issues/12' }] });
  await expectReject(() => trackSuppressions(missingLink.github, context), 'must be added to .ci/static-suppressions.json');
  if (missingLink.calls.filter(([name]) => name === 'update').length !== 1 || missingLink.calls.some(([name]) => name === 'create')) throw new Error('existing unlinked issue was not updated idempotently');
  const invalidIssue = mock({ content: { encoding: 'base64', content: Buffer.from(JSON.stringify({ suppressions: [{ ...manifest.suppressions[0], issue: 'https://github.com/other/repo/issues/7' }] })).toString('base64') } });
  await expectReject(() => trackSuppressions(invalidIssue.github, context), 'must belong to this repository');
  process.stdout.write('suppression accountability workflow tests passed\n');
}

await main();
