const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const governance = require('./contributor-governance.js');

test('classifies logins case-insensitively and leaves unknown users grey', () => {
  const policy = { whitelist: ['Oleg-Koval'], blacklist: [{ login: 'BadActor', reason: 'configured abuse' }] };
  assert.equal(governance.classifyContributor(policy, 'oleg-koval').classification, 'whitelisted');
  assert.equal(governance.classifyContributor(policy, 'BADACTOR').reason, 'configured abuse');
  assert.equal(governance.classifyContributor(policy, 'new-user').classification, 'grey');
  assert.equal(governance.classifyContributor({
    whitelist: ['overlap'],
    blacklist: [{ login: 'overlap', reason: 'blocked' }],
  }, 'overlap').classification, 'blacklisted');
});

test('matches explicitly configured automation scopes', () => {
  const policy = {
    settings: {
      trusted_automation: [
        { login: 'github-actions[bot]', head_prefix: '*', same_repository: true },
        { login: 'release-bot', head_prefix: 'release/' },
      ],
    },
  };
  const repository = { full_name: 'oleg-koval/veto' };
  const releasePR = {
    user: { login: 'GitHub-Actions[Bot]' },
    head: { ref: 'release-please--branches--main--components--veto', repo: repository },
    base: { repo: repository },
  };
  assert.ok(governance.trustedAutomationEntry(policy, releasePR));
  assert.ok(governance.trustedAutomationEntry(policy, {
    user: { login: 'github-actions[bot]' },
    head: { ref: 'feature/release', repo: repository },
    base: { repo: repository },
  }));
  assert.equal(governance.trustedAutomationEntry(policy, {
    user: { login: 'github-actions[bot]' },
    head: { ref: 'feature/release', repo: { full_name: 'fork-owner/veto' } },
    base: { repo: repository },
  }), undefined);
  assert.ok(governance.trustedAutomationEntry(policy, { user: { login: 'release-bot' }, head: { ref: 'release/v1' } }));
  assert.equal(governance.trustedAutomationEntry(policy, { user: { login: 'release-bot' }, head: { ref: 'feature/release' } }), undefined);
  assert.equal(governance.trustedAutomationEntry(policy, { user: { login: 'dependabot' }, head: { ref: 'release-please--branches--main--components--veto' } }), undefined);
});

test('skips duplicate issue validation for configured GitHub Actions PRs', async () => {
  const notices = [];
  const failures = [];
  const comments = [];
  await governance.run({
    github: {
      rest: {
        pulls: { listCommits: async () => ({ data: [{ author: { login: 'release-maintainer' } }] }) },
        issues: {
          listComments: async () => ({ data: [] }),
          createComment: async ({ body }) => comments.push(body),
        },
      },
    },
    context: {
      repo: { owner: 'oleg-koval', repo: 'veto' },
      payload: {
        pull_request: {
          number: 23,
          user: { login: 'github-actions[bot]' },
          head: { ref: 'automation/update', repo: { full_name: 'oleg-koval/veto' } },
          base: { repo: { full_name: 'oleg-koval/veto' } },
          body: '',
        },
      },
    },
    core: {
      notice: (message) => notices.push(message),
      setFailed: (message) => failures.push(message),
      warning: () => {},
    },
    policyPath: path.join(__dirname, '..', 'contributor-policy.json'),
  });
  assert.equal(failures.length, 0);
  assert.match(notices[0], /Trusted automation/);
  assert.match(comments[0], /Commit-author mismatch/);
});

test('skips all governance validation for whitelisted contributors', async () => {
  const notices = [];
  const failures = [];
  let issueLookupCalled = false;
  await governance.run({
    github: {
      rest: {
        pulls: { listCommits: async () => { throw new Error('commit lookup should be skipped'); } },
        issues: {
          get: async () => { issueLookupCalled = true; throw new Error('issue lookup should be skipped'); },
          listComments: async () => { throw new Error('comment lookup should be skipped'); },
        },
      },
    },
    context: {
      repo: { owner: 'oleg-koval', repo: 'veto' },
      payload: {
        pull_request: {
          number: 66,
          user: { login: 'OLEG-KOVAL' },
          head: { ref: 'fix/agentic-run-routing', repo: { full_name: 'oleg-koval/veto' } },
          base: { repo: { full_name: 'oleg-koval/veto' } },
          body: 'Closes #',
        },
      },
    },
    core: {
      notice: (message) => notices.push(message),
      setFailed: (message) => failures.push(message),
      warning: () => {},
    },
    policyPath: path.join(__dirname, '..', 'contributor-policy.json'),
  });
  assert.equal(failures.length, 0);
  assert.equal(issueLookupCalled, false);
  assert.match(notices[0], /Whitelisted contributor/);
});

test('accepts same-repository issue links and rejects external or malformed links', () => {
  assert.deepEqual(governance.extractIssueReferences('Fixes #42', 'oleg-koval', 'veto'), { numbers: [42], malformed: false });
  assert.deepEqual(governance.extractIssueReferences('Fixes oleg-koval/veto#43', 'oleg-koval', 'veto'), { numbers: [43], malformed: false });
  assert.deepEqual(governance.extractIssueReferences('Release PR #23\nCloses #44', 'oleg-koval', 'veto'), { numbers: [44], malformed: false });
  assert.equal(governance.extractIssueReferences('Fixes https://github.com/other/repo/issues/42', 'oleg-koval', 'veto').malformed, true);
  assert.equal(governance.extractIssueReferences('Fixes #not-a-number', 'oleg-koval', 'veto').malformed, true);
});

test('distinguishes GitHub pull requests from issue resources', () => {
  assert.equal(governance.isPullRequestReference({ pull_request: { html_url: 'https://github.com/oleg-koval/veto/pull/60' } }), true);
  assert.equal(governance.isPullRequestReference({ html_url: 'https://github.com/oleg-koval/veto/issues/61' }), false);
  assert.equal(governance.isPullRequestReference(null), false);
});

test('fails clearly when a linked reference resolves to a pull request', async () => {
  const failures = [];
  const comments = [];
  await governance.run({
    github: {
      rest: {
        pulls: { listCommits: async () => ({ data: [] }) },
        issues: {
          get: async () => ({ data: { pull_request: { html_url: 'https://github.com/oleg-koval/veto/pull/60' } } }),
          listComments: async () => ({ data: [] }),
          createComment: async ({ body }) => comments.push(body),
        },
      },
    },
    context: {
      repo: { owner: 'oleg-koval', repo: 'veto' },
      payload: {
        pull_request: {
          number: 62,
          user: { login: 'new-user' },
          head: { ref: 'feature/example', repo: { full_name: 'oleg-koval/veto' } },
          base: { repo: { full_name: 'oleg-koval/veto' } },
          body: 'Closes #60',
        },
      },
    },
    core: {
      notice: () => {},
      setFailed: (message) => failures.push(message),
      warning: () => {},
    },
    policyPath: path.join(__dirname, '..', 'contributor-policy.json'),
  });
  assert.match(failures[0], /Linked reference #60 is a pull request/);
  assert.match(comments[0], /link a GitHub issue with acceptance criteria/);
});

test('trusts PAT-owned Release Please branches without trusting ordinary maintainer branches', () => {
  const policy = {
    settings: {
      trusted_automation: [{
        login: 'oleg-koval',
        head_prefix: 'release-please--branches--main--components--',
        same_repository: true,
      }],
    },
  };
  const repository = { full_name: 'oleg-koval/veto' };
  const releasePleasePR = {
    user: { login: 'oleg-koval' },
    head: { ref: 'release-please--branches--main--components--veto', repo: repository },
    base: { repo: repository },
  };
  const ordinaryPR = {
    ...releasePleasePR,
    head: { ...releasePleasePR.head, ref: 'feature/release-please-lookalike' },
  };
  assert.ok(governance.trustedAutomationEntry(policy, releasePleasePR));
  assert.equal(governance.trustedAutomationEntry(policy, ordinaryPR), undefined);
});

test('requires acceptance criteria and non-empty mapped evidence', () => {
  const issue = '### Acceptance criteria\n- [ ] Tests pass\n- [ ] Docs explain the change\n\n### Scope\nCLI';
  const criteria = governance.extractAcceptanceCriteria(issue);
  assert.deepEqual(criteria, ['Tests pass', 'Docs explain the change']);
  const good = '## Acceptance criteria evidence\n#### Criterion: Tests pass\nEvidence: go test ./...\n#### Criterion: Docs explain the change\nEvidence: CONTRIBUTING.md updated';
  assert.equal(governance.validateAcceptanceEvidence(criteria, good).valid, true);
  assert.equal(governance.validateAcceptanceEvidence(criteria, good.replace('Evidence: CONTRIBUTING.md updated', 'Evidence: <!-- empty -->')).valid, false);
  const missingFirst = '## Acceptance criteria evidence\n#### Criterion: Tests pass\n#### Criterion: Docs explain the change\nEvidence: docs updated';
  assert.deepEqual(governance.validateAcceptanceEvidence(criteria, missingFirst).missing, ['Tests pass']);
});

test('fails closed when reputation lookup is unavailable', async () => {
  const github = {
    rest: {
      users: {
        getByUsername: async () => ({ data: { created_at: '2020-01-01T00:00:00Z', followers: 1, public_repos: 2 } }),
        listPublicEventsForUser: async () => { throw { status: 503 }; },
      },
      search: { issuesAndPullRequests: async () => ({ data: { total_count: 0 } }) },
    },
  };
  await assert.rejects(governance.lookupReputation(github, 'oleg-koval', 'veto', 'grey-user'), (error) => error.status === 503);
});
