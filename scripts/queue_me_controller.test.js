'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');

const runController = require('./queue_me_controller.js');
const { testables } = runController;

function makePull(number, overrides = {}) {
  return {
    number,
    node_id: `PR_${number}`,
    labels: [{ name: 'queue-me' }],
    draft: false,
    maintainer_can_modify: true,
    base: {
      ref: 'main',
      repo: {
        name: 'wtgc',
        owner: { login: 'octo' },
      },
    },
    head: {
      sha: `head-${number}`,
      repo: { full_name: 'octo/wtgc' },
    },
    ...overrides,
  };
}

function makeHarness(options = {}) {
  const pulls = options.pulls || [];
  const eventPull = options.eventPull;
  const branchSHAs = options.branchSHAs || ['base-sha'];
  const allPulls = eventPull && !pulls.some((pull) => pull.number === eventPull.number)
    ? [...pulls, eventPull]
    : pulls;
  const states = new Map(
    allPulls.map((pull) => [
      pull.number,
      {
        id: pull.node_id,
        number: pull.number,
        baseRefName: pull.base.ref,
        baseRefOid: branchSHAs[0],
        headRefOid: pull.head.sha,
        isDraft: pull.draft,
        mergeable: 'MERGEABLE',
        mergeStateStatus: 'BLOCKED',
        autoMergeRequest: null,
        labels: pull.labels,
        ...(options.initialStates?.[pull.number] || {}),
      },
    ]),
  );
  const comments = new Map();
  for (const number of options.managedPullNumbers || []) {
    comments.set(number, [{ id: number, body: `${'<!-- queue-me-controller -->'}\nmanaged`, user: { type: 'Bot' } }]);
  }
  const calls = {
    armed: [],
    armExpectedHeads: [],
    branchReads: [],
    comments: [],
    createdLabels: [],
    disabled: [],
    deletedComments: [],
    merged: [],
    notices: [],
    rebased: [],
  };
  const repository = { default_branch: 'main', full_name: 'octo/wtgc' };

  const github = {
    rest: {
      issues: {
        getLabel: async () => {
          if (options.labelMissing) {
            const error = new Error('label missing');
            error.status = 404;
            throw error;
          }
        },
        createLabel: async (input) => {
          calls.createdLabels.push(input.name);
        },
        listComments: async () => {},
        createComment: async (input) => {
          const comment = { id: calls.comments.length + 1, body: input.body, user: { type: 'Bot' } };
          comments.set(input.issue_number, [comment]);
          calls.comments.push({ number: input.issue_number, body: input.body });
        },
        updateComment: async (input) => {
          for (const [number, issueComments] of comments) {
            const existing = issueComments.find((comment) => comment.id === input.comment_id);
            if (existing) {
              existing.body = input.body;
              calls.comments.push({ number, body: input.body });
              return;
            }
          }
          throw new Error(`unknown comment ${input.comment_id}`);
        },
        deleteComment: async (input) => {
          calls.deletedComments.push(input.comment_id);
          for (const [number, issueComments] of comments) {
            const index = issueComments.findIndex((comment) => comment.id === input.comment_id);
            if (index >= 0) issueComments.splice(index, 1);
          }
        },
      },
      pulls: {
        list: async () => {},
      },
      repos: {
        get: async () => ({ data: repository }),
        getBranch: async () => {
          const sha = branchSHAs[Math.min(calls.branchReads.length, branchSHAs.length - 1)];
          calls.branchReads.push(sha);
          return { data: { commit: { sha } } };
        },
        compareCommitsWithBasehead: async () => ({
          data: { status: options.comparisonStatus || 'ahead' },
        }),
      },
    },
    paginate: async (_method, input) => {
      if (input.issue_number) {
        return comments.get(input.issue_number) || [];
      }
      return pulls;
    },
    graphql: async (query, variables) => {
      if (query.includes('QueuePullState($owner')) {
        const state = states.get(variables.number);
        if (calls.branchReads.length >= 2 && options.stateAfterFinalBranchRead?.[variables.number]) {
          Object.assign(state, options.stateAfterFinalBranchRead[variables.number]);
        }
        return { repository: { pullRequest: { ...state } } };
      }
      if (query.includes('QueuePullStateByID')) {
        const state = [...states.values()].find((value) => value.id === variables.pullRequestId);
        return {
          node: {
            ...state,
            ...(options.removeQueueLabelBeforeArm ? { labels: [] } : {}),
          },
        };
      }
      if (query.includes('DisableQueueAutoMerge')) {
        const state = [...states.values()].find((value) => value.id === variables.pullRequestId);
        state.autoMergeRequest = null;
        calls.disabled.push(state.number);
        return { disablePullRequestAutoMerge: { pullRequest: { number: state.number } } };
      }
      if (query.includes('RebaseQueuedPull')) {
        const rebaseError = options.rebaseErrors?.[variables.pullRequestId] || options.rebaseError;
        if (rebaseError) {
          throw rebaseError;
        }
        const state = [...states.values()].find((value) => value.id === variables.pullRequestId);
        state.headRefOid = options.rebasedHead || `rebased-${state.number}`;
        calls.rebased.push(state.number);
        return { updatePullRequestBranch: { pullRequest: state } };
      }
      if (query.includes('ArmQueueAutoMerge')) {
        const state = [...states.values()].find((value) => value.id === variables.pullRequestId);
        calls.armExpectedHeads.push(variables.expectedHeadOid);
        if (options.armErrorHead) {
          state.headRefOid = options.armErrorHead;
        }
        if (options.armError) {
          throw options.armError;
        }
        state.autoMergeRequest = { enabledAt: 'now', mergeMethod: 'SQUASH' };
        calls.armed.push(state.number);
        return { enablePullRequestAutoMerge: { pullRequest: state } };
      }
      if (query.includes('MergeQueuedPull')) {
        const state = [...states.values()].find((value) => value.id === variables.pullRequestId);
        calls.merged.push(state.number);
        return { mergePullRequest: { pullRequest: { number: state.number, merged: true } } };
      }
      throw new Error(`unexpected GraphQL operation: ${query}`);
    },
  };

  const payload = eventPull
    ? {
        action: options.action || 'labeled',
        label: { name: 'queue-me' },
        pull_request: eventPull,
        sender: options.sender || { login: 'octocat', type: 'User' },
      }
    : {};
  return {
    args: {
      github,
      context: {
        repo: { owner: 'octo', repo: 'wtgc' },
        eventName: eventPull ? 'pull_request_target' : 'workflow_dispatch',
        payload,
      },
      core: {
        notice: (message) => calls.notices.push(message),
      },
      queueAppSlug: options.queueAppSlug,
    },
    calls,
    pulls,
  };
}

function commentsFor(harness, number) {
  return harness.calls.comments
    .filter((comment) => comment.number === number)
    .map((comment) => comment.body)
    .at(-1) || '';
}

test('sortQueuedPulls uses deterministic ascending PR numbers', () => {
  const sorted = testables.sortQueuedPulls([{ number: 42 }, { number: 7 }, { number: 19 }]);
  assert.deepEqual(sorted.map((pull) => pull.number), [7, 19, 42]);
});

test('hasLabel accepts REST label objects and string labels', () => {
  assert.equal(testables.hasLabel({ labels: [{ name: 'queue-me' }] }, 'queue-me'), true);
  assert.equal(testables.hasLabel({ labels: ['queue-me'] }, 'queue-me'), true);
  assert.equal(testables.hasLabel({ labels: [{ name: 'other' }] }, 'queue-me'), false);
  assert.equal(testables.hasLabel({}, 'queue-me'), false);
});

test('isBranchCurrent accepts only ancestor-preserving compare states', () => {
  assert.equal(testables.isBranchCurrent('ahead'), true);
  assert.equal(testables.isBranchCurrent('identical'), true);
  assert.equal(testables.isBranchCurrent('behind'), false);
  assert.equal(testables.isBranchCurrent('diverged'), false);
});

test('isRebaseConflict recognizes GitHub rebase conflicts without hiding unrelated errors', () => {
  assert.equal(testables.isRebaseConflict(new Error('PullRequest::RebaseConflictError')), true);
  assert.equal(testables.isRebaseConflict({ errors: [{ message: 'conflict' }] }), true);
  assert.equal(testables.isRebaseConflict(new Error('GitHub API unavailable')), false);
});

test('status helpers bound untrusted API text', () => {
  assert.equal(testables.shortSHA('1234567890abcdef'), '1234567890');
  assert.equal(testables.shortSHA(undefined), 'unknown');
  const sanitized = testables.safeError(new Error('bad `branch`\r\ntry again'));
  assert.equal(sanitized, "bad 'branch' try again");
  assert.equal(testables.safeError('x'.repeat(1300)).length, 1200);
});

test('controller creates the queue label and exits cleanly for an empty queue', async () => {
  const harness = makeHarness({ labelMissing: true });

  await runController(harness.args);

  assert.deepEqual(harness.calls.createdLabels, ['queue-me']);
  assert.equal(harness.calls.notices.length, 1);
  assert.match(harness.calls.notices[0], /No open main pull requests/);
});

test('controller disables followers and waits for the oldest numbered pull request', async () => {
  const leader = makePull(10);
  const follower = makePull(20);
  const harness = makeHarness({
    pulls: [follower, leader],
    eventPull: follower,
    initialStates: {
      20: { autoMergeRequest: { enabledAt: 'before', mergeMethod: 'SQUASH' } },
    },
  });

  await runController(harness.args);

  assert.deepEqual(harness.calls.disabled, [20]);
  assert.deepEqual(harness.calls.armed, []);
  assert.deepEqual(harness.calls.merged, []);
  assert.match(
    harness.calls.comments.find((comment) => comment.number === 20).body,
    /Queued behind #10/,
  );
  assert.match(
    harness.calls.comments.find((comment) => comment.number === 10).body,
    /Queue waiting/,
  );
});

test('queue refresh updates a stale follower position after the leader advances', async () => {
  const formerLeader = makePull(3);
  const currentLeader = makePull(5);
  const follower = makePull(8);
  const harness = makeHarness({
    pulls: [formerLeader, currentLeader, follower],
    eventPull: follower,
  });

  await runController(harness.args);
  assert.match(commentsFor(harness, 8), /Queued behind #3/);
  assert.equal(commentsFor(harness, 5), '');

  harness.pulls.splice(0, harness.pulls.length, currentLeader, follower);
  harness.args.context.eventName = 'push';
  harness.args.context.payload = {};

  await runController(harness.args);

  assert.match(commentsFor(harness, 8), /Queued behind #5/);
  assert.doesNotMatch(commentsFor(harness, 8), /Queued behind #3/);
});

test('controller rebases a stale leader and merges it when repository rules are satisfied', async () => {
  const leader = makePull(10);
  const harness = makeHarness({
    pulls: [leader],
    comparisonStatus: 'behind',
    initialStates: {
      10: { mergeStateStatus: 'CLEAN' },
    },
  });

  await runController(harness.args);

  assert.deepEqual(harness.calls.rebased, [10]);
  assert.deepEqual(harness.calls.merged, [10]);
  assert.deepEqual(harness.calls.armed, []);
  assert.match(harness.calls.comments[0].body, /Rebased/);
  assert.match(harness.calls.comments[0].body, /GitHub squash-merged it/);
});

test('controller pauses instead of advancing a leader removed from queue-me before arming', async () => {
  const leader = makePull(10);
  const harness = makeHarness({
    pulls: [leader],
    removeQueueLabelBeforeArm: true,
  });

  await assert.rejects(runController(harness.args), /no longer has the queue-me label/);

  assert.deepEqual(harness.calls.armed, []);
  assert.deepEqual(harness.calls.merged, []);
  assert.match(harness.calls.comments[0].body, /Queue paused/);
});

test('removing queue-me disables auto-merge and leaves an empty queue green', async () => {
  const pull = makePull(10, { labels: [] });
  const harness = makeHarness({
    eventPull: pull,
    action: 'unlabeled',
    initialStates: {
      10: { autoMergeRequest: { enabledAt: 'before', mergeMethod: 'SQUASH' } },
    },
  });

  await runController(harness.args);

  assert.deepEqual(harness.calls.disabled, [10]);
  assert.deepEqual(harness.calls.armed, []);
  assert.match(harness.calls.comments[0].body, /automatic merge is disabled/);
  assert.equal(harness.calls.notices.length, 1);
});

test('later queue runs disable auto-merge for a formerly managed pull removed from the queue', async () => {
  const pull = makePull(10, { labels: [] });
  const harness = makeHarness({
    pulls: [pull],
    managedPullNumbers: [10],
    initialStates: { 10: { autoMergeRequest: { enabledAt: 'before', mergeMethod: 'SQUASH' } } },
  });

  await runController(harness.args);

  assert.deepEqual(harness.calls.disabled, [10]);
  assert.deepEqual(harness.calls.armed, []);
  assert.deepEqual(harness.calls.deletedComments, [10]);
});

test('drafts and stale fork branches pause before rebase or auto-merge', async (t) => {
  const cases = [
    { name: 'draft', pull: makePull(10, { draft: true }), message: /still a draft/ },
    {
      name: 'stale fork',
      pull: makePull(10, {
        head: { sha: 'fork-head', repo: { full_name: 'contributor/wtgc' } },
      }),
      options: { comparisonStatus: 'behind' },
      message: /queue App cannot update it/,
    },
  ];

  for (const scenario of cases) {
    await t.test(scenario.name, async () => {
      const harness = makeHarness({ pulls: [scenario.pull], ...scenario.options });
      await runController(harness.args);
      assert.deepEqual(harness.calls.rebased, []);
      assert.deepEqual(harness.calls.armed, []);
      assert.match(harness.calls.comments[0].body, scenario.message);
    });
  }
});

test('a current fork branch waits without a branch update', async () => {
  const fork = makePull(10, {
    head: { sha: 'fork-head', repo: { full_name: 'contributor/wtgc' } },
  });
  const harness = makeHarness({ pulls: [fork], comparisonStatus: 'ahead' });

  await runController(harness.args);

  assert.deepEqual(harness.calls.rebased, []);
  assert.deepEqual(harness.calls.armed, []);
  assert.match(harness.calls.comments[0].body, /Queue waiting/);
});

test('a rebase conflict pauses that pull request and advances the next queued pull request', async () => {
  const leader = makePull(10);
  const next = makePull(20);
  const harness = makeHarness({
    pulls: [leader, next],
    comparisonStatus: 'behind',
    rebaseErrors: { PR_10: new Error('conflict in `workflow`') },
    initialStates: { 20: { mergeStateStatus: 'CLEAN' } },
  });

  await runController(harness.args);

  assert.deepEqual(harness.calls.armed, []);
  assert.deepEqual(harness.calls.rebased, [20]);
  assert.deepEqual(harness.calls.merged, [20]);
  assert.match(commentsFor(harness, 10), /could not rebase/);
  assert.match(commentsFor(harness, 10), /conflict in 'workflow'/);
  assert.match(commentsFor(harness, 20), /GitHub squash-merged it/);
});

test('controller pauses when the default branch moves before auto-merge is armed', async () => {
  const harness = makeHarness({
    pulls: [makePull(10)],
    branchSHAs: ['base-sha', 'new-base-sha'],
  });

  await assert.rejects(runController(harness.args), /Default branch main moved/);

  assert.deepEqual(harness.calls.branchReads, ['base-sha', 'new-base-sha']);
  assert.deepEqual(harness.calls.armed, []);
  assert.deepEqual(harness.calls.merged, []);
  assert.match(harness.calls.comments[0].body, /Default branch main moved/);
});

test('controller never arms auto-merge for a non-clean leader', async () => {
  const harness = makeHarness({
    pulls: [makePull(10)],
    armError: new Error('expected head mismatch'),
    armErrorHead: 'pushed-head',
  });

  await runController(harness.args);
  assert.deepEqual(harness.calls.armed, []);
  assert.deepEqual(harness.calls.merged, []);
  assert.match(harness.calls.comments[0].body, /Queue waiting/);
});

test('controller revalidates baseRefName and baseRefOid immediately before auto-merge or merge', async (t) => {
  const cases = [
    {
      name: 'retargeted base pauses before auto-merge',
      harness: makeHarness({
        pulls: [makePull(10)],
        stateAfterFinalBranchRead: {
          10: { baseRefName: 'release' },
        },
      }),
      message: /Pull request base changed from main to release/,
    },
    {
      name: 'base tip drift pauses before merge',
      harness: makeHarness({
        pulls: [makePull(10)],
        stateAfterFinalBranchRead: {
          10: { baseRefOid: '1234567890abcdef', mergeStateStatus: 'CLEAN' },
        },
      }),
      message: /Pull request base main moved from base-sha to 1234567890/,
    },
  ];

  for (const scenario of cases) {
    await t.test(scenario.name, async () => {
      await assert.rejects(runController(scenario.harness.args), scenario.message);

      assert.deepEqual(scenario.harness.calls.armed, []);
      assert.deepEqual(scenario.harness.calls.merged, []);
      assert.match(scenario.harness.calls.comments[0].body, scenario.message);
    });
  }
});

test('changing a queued pull request away from main disables auto-merge', async () => {
  const pull = makePull(10);
  pull.base.ref = 'release';
  const harness = makeHarness({
    eventPull: pull,
    action: 'edited',
    initialStates: {
      10: { autoMergeRequest: { enabledAt: 'before', mergeMethod: 'SQUASH' } },
    },
  });

  await runController(harness.args);

  assert.deepEqual(harness.calls.disabled, [10]);
  assert.deepEqual(harness.calls.armed, []);
  assert.match(harness.calls.comments[0].body, /base changed to `release`/);
  assert.equal(harness.calls.notices.length, 1);
});

test('non-default-base queue events disable auto-merge', async (t) => {
  for (const action of ['labeled', 'auto_merge_enabled']) {
    await t.test(action, async () => {
      const pull = makePull(10);
      pull.base.ref = 'release';
      const harness = makeHarness({
        eventPull: pull,
        action,
        initialStates: {
          10: { autoMergeRequest: { enabledAt: 'before', mergeMethod: 'SQUASH' } },
        },
      });

      await runController(harness.args);

      assert.deepEqual(harness.calls.disabled, [10]);
      assert.deepEqual(harness.calls.armed, []);
      assert.match(harness.calls.comments[0].body, /base changed to `release`/);
      assert.equal(harness.calls.notices.length, 1);
    });
  }
});

test('a non-default-base pause comment is not replaced by a queue position', async () => {
  const leader = makePull(10);
  const releasePull = makePull(20);
  releasePull.base.ref = 'release';
  const harness = makeHarness({
    pulls: [leader],
    eventPull: releasePull,
    action: 'labeled',
    initialStates: {
      20: { autoMergeRequest: { enabledAt: 'manual', mergeMethod: 'SQUASH' } },
    },
  });

  await runController(harness.args);

  const eventComments = harness.calls.comments.filter(
    (comment) => comment.number === 20 || comment.number === undefined,
  );
  assert.deepEqual(harness.calls.disabled, [20]);
  assert.equal(eventComments.length, 1);
  assert.match(eventComments[0].body, /base changed to `release`/);
  assert.doesNotMatch(eventComments[0].body, /Queued behind/);
  assert.deepEqual(harness.calls.armed, []);
});

test('manually enabling auto-merge on a follower restores queue ordering', async () => {
  const leader = makePull(10);
  const follower = makePull(20);
  const harness = makeHarness({
    pulls: [leader, follower],
    eventPull: follower,
    action: 'auto_merge_enabled',
    initialStates: {
      20: { autoMergeRequest: { enabledAt: 'manual', mergeMethod: 'SQUASH' } },
    },
  });

  await runController(harness.args);

  assert.deepEqual(harness.calls.disabled, [20]);
  assert.deepEqual(harness.calls.armed, []);
});

test("the queue App's leader auto-merge event does not trigger a disable-enable loop", async () => {
  const leader = makePull(10);
  const harness = makeHarness({
    pulls: [leader],
    eventPull: leader,
    action: 'auto_merge_enabled',
    queueAppSlug: 'queue-app',
    sender: { login: 'queue-app[bot]', type: 'Bot' },
    initialStates: {
      10: { autoMergeRequest: { enabledAt: 'controller', mergeMethod: 'SQUASH' } },
    },
  });

  await runController(harness.args);

  assert.deepEqual(harness.calls.disabled, []);
  assert.deepEqual(harness.calls.armed, []);
  assert.match(harness.calls.notices[0], /Ignoring the queue App's auto-merge event/);
});
