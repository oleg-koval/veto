const test = require('node:test');
const assert = require('node:assert/strict');
const governance = require('./contributor-governance.js');

test('classifies logins case-insensitively and leaves unknown users grey', () => {
  const policy = { whitelist: ['Oleg-Koval'], blacklist: [{ login: 'BadActor', reason: 'configured abuse' }] };
  assert.equal(governance.classifyContributor(policy, 'oleg-koval').classification, 'whitelisted');
  assert.equal(governance.classifyContributor(policy, 'BADACTOR').reason, 'configured abuse');
  assert.equal(governance.classifyContributor(policy, 'new-user').classification, 'grey');
});

test('accepts same-repository issue links and rejects external or malformed links', () => {
  assert.deepEqual(governance.extractIssueReferences('Fixes #42', 'oleg-koval', 'veto'), { numbers: [42], malformed: false });
  assert.deepEqual(governance.extractIssueReferences('Fixes oleg-koval/veto#43', 'oleg-koval', 'veto'), { numbers: [43], malformed: false });
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
