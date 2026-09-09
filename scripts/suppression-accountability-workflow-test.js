#!/usr/bin/env node
// Execute the trusted github-script body with mocked REST calls.
const fs = require('node:fs');
const path = require('node:path');

const workflow = fs.readFileSync(path.join(__dirname, '..', '.github', 'workflows', 'suppression-accountability.yml'), 'utf8');
const lines = workflow.split('\n');
const start = lines.findIndex((line) => line === '          script: |');
if (start < 0) throw new Error('github-script body was not found');
const script = lines.slice(start + 1).filter((line) => line.startsWith('            ')).map((line) => line.slice(12)).join('\n');
const run = new Function('github', 'context', `return (async () => {${script}\n})()`);

const patch = '@@ -0,0 +1 @@\n+package fixture //nosec G204';
const manifest = {
  suppressions: [{
    location: 'fixture.go:1', rationale: 'fixture', owner: 'security',
    removal_condition: 'replace fixture', issue: 'https://github.com/example/repo/issues/7',
  }],
};

function mock(options = {}) {
  const calls = [];
  function listFiles() {}
  function listComments() {}
  function listForRepo() {}
  const github = {
    rest: {
      pulls: { listFiles },
      repos: { getContent: async () => ({ data: options.content || { encoding: 'base64', content: Buffer.from(JSON.stringify(manifest)).toString('base64') } }) },
      issues: {
        listComments,
        listForRepo,
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
  try { await action(); } catch (error) { if (String(error).includes(message)) return; throw error; }
  throw new Error(`expected failure containing ${message}`);
}

(async () => {
  const linked = mock();
  await run(linked.github, context);
  if (linked.calls.filter(([name]) => name === 'updateComment').length !== 1 || linked.calls.some(([name]) => name === 'create')) throw new Error('linked issue was not updated idempotently');

  const truncated = mock({ files: [{ filename: 'fixture.go', patch, changes: 2 }] });
  await expectReject(() => run(truncated.github, context), 'patch is missing or truncated');

  const missingLink = mock({ content: { encoding: 'base64', content: Buffer.from(JSON.stringify({ suppressions: [{ ...manifest.suppressions[0], issue: '' }] })).toString('base64') }, issues: [{ number: 12, title: 'Track static-analysis suppression: fixture.go:1', html_url: 'https://github.com/example/repo/issues/12' }] });
  await expectReject(() => run(missingLink.github, context), 'must be added to .ci/static-suppressions.json');
  if (missingLink.calls.filter(([name]) => name === 'update').length !== 1 || missingLink.calls.some(([name]) => name === 'create')) throw new Error('existing unlinked issue was not updated idempotently');

  const invalidIssue = mock({ content: { encoding: 'base64', content: Buffer.from(JSON.stringify({ suppressions: [{ ...manifest.suppressions[0], issue: 'https://github.com/other/repo/issues/7' }] })).toString('base64') } });
  await expectReject(() => run(invalidIssue.github, context), 'must belong to this repository');
  process.stdout.write('suppression accountability workflow tests passed\n');
})().catch((error) => { console.error(error.stack || error); process.exit(1); });
