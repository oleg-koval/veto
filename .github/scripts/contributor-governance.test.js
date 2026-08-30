const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const governance = require('./contributor-governance.js');

test('classifies logins case-insensitively and leaves unknown users grey', () => {
  const policy = { whitelist: ['Oleg-Koval'], blacklist: [{ login: 'BadActor', reason: 'configured abuse' }] };
  assert.equal(governance.classifyContributor(policy, 'oleg-koval').classification, 'whitelisted');
  assert.equal(governance.classifyContributor(policy, 'BADACTOR').reason, 'configured abuse');
  assert.equal(governance.classifyContributor(policy, 'new-user').classification, 'grey');
});

test('matches only the explicitly configured Release Please automation', () => {
  const policy = {
    settings: {
      trusted_automation: [{ login: 'github-actions', head_prefix: 'release-please--branches--main--components--' }],
    },
  };
  const releasePR = { user: { login: 'GitHub-Actions' }, head: { ref: 'release-please--branches--main--components--veto' } };
  assert.ok(governance.trustedAutomationEntry(policy, releasePR));
  assert.equal(governance.trustedAutomationEntry(policy, { user: { login: 'github-actions' }, head: { ref: 'feature/release' } }), undefined);
  assert.equal(governance.trustedAutomationEntry(policy, { user: { login: 'dependabot' }, head: { ref: 'release-please--branches--main--components--veto' } }), undefined);
});

test('skips duplicate issue validation for the configured Release Please PR', async () => {
  const notices = [];
  const failures = [];
  await governance.run({
    github: { rest: {} },
    context: {
      repo: { owner: 'oleg-koval', repo: 'veto' },
      payload: {
        pull_request: {
          number: 23,
          user: { login: 'github-actions' },
          head: { ref: 'release-please--branches--main--components--veto' },
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
});

test('accepts same-repository issue links and rejects external or malformed links', () => {
  assert.deepEqual(governance.extractIssueReferences('Fixes #42', 'oleg-koval', 'veto'), { numbers: [42], malformed: false });
  assert.deepEqual(governance.extractIssueReferences('Fixes oleg-koval/veto#43', 'oleg-koval', 'veto'), { numbers: [43], malformed: false });
  assert.deepEqual(governance.extractIssueReferences('Release PR #23\nCloses #44', 'oleg-koval', 'veto'), { numbers: [44], malformed: false });
  assert.equal(governance.extractIssueReferences('Fixes https://github.com/other/repo/issues/42', 'oleg-koval', 'veto').malformed, true);
  assert.equal(governance.extractIssueReferences('Fixes #not-a-number', 'oleg-koval', 'veto').malformed, true);
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
