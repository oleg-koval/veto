const fs = require('fs');

const MARKER = '<!-- veto-contributor-governance -->';
const MISMATCH_MARKER = '<!-- veto-contributor-author-mismatch -->';

function normalizeLogin(login) {
  return String(login || '').trim().toLowerCase();
}

function policyEntries(entries) {
  if (Array.isArray(entries)) return entries;
  if (entries && typeof entries === 'object') {
    return Object.entries(entries).map(([login, reason]) => ({ login, reason }));
  }
  return [];
}

function findEntry(entries, login) {
  const wanted = normalizeLogin(login);
  return policyEntries(entries).find((entry) => {
    const value = typeof entry === 'string' ? entry : entry && entry.login;
    return normalizeLogin(value) === wanted;
  });
}

function classifyContributor(policy, login) {
  const blacklisted = findEntry(policy.blacklist, login);
  if (blacklisted) {
    return {
      classification: 'blacklisted',
      reason: typeof blacklisted === 'string' ? 'blacklisted by repository policy' : (blacklisted.reason || 'blacklisted by repository policy'),
    };
  }
  if (findEntry(policy.whitelist, login)) return { classification: 'whitelisted', reason: '' };
  return { classification: 'grey', reason: 'not listed in repository policy' };
}

function extractIssueReferences(body, owner, repo) {
  const text = String(body || '');
  const references = new Set();
  let malformed = false;
  const urlPattern = /https?:\/\/github\.com\/([^/]+)\/([^/#]+)\/issues\/(\d+)/gi;
  for (const match of text.matchAll(urlPattern)) {
    if (normalizeLogin(match[1]) !== normalizeLogin(owner) || normalizeLogin(match[2]) !== normalizeLogin(repo)) malformed = true;
    else references.add(Number(match[3]));
  }
  const shortPattern = /(?:^|[\s([{:])#(\d+)\b/g;
  for (const match of text.matchAll(shortPattern)) references.add(Number(match[1]));
  const qualifiedPattern = new RegExp(`${owner.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\/${repo.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}#(\\d+)`, 'gi');
  for (const match of text.matchAll(qualifiedPattern)) references.add(Number(match[1]));
  if (/(?:^|[\s([{:])#[A-Za-z][A-Za-z0-9_-]*/.test(text)) malformed = true;
  return { numbers: [...references], malformed };
}

function extractAcceptanceCriteria(body) {
  const text = String(body || '');
  const header = /^#{1,6}\s+acceptance criteria\s*$/im.exec(text);
  if (!header) return [];
  const rest = text.slice(header.index + header[0].length);
  const nextHeading = /^#{1,6}\s/m.exec(rest);
  const section = nextHeading ? rest.slice(0, nextHeading.index) : rest;
  return section.split('\n')
    .map((line) => line.match(/^\s*[-*]\s+(?:\[[ xX]\]\s*)?(.+?)\s*$/))
    .map((match) => match && match[1].replace(/<!--.*?-->/g, '').trim())
    .filter(Boolean);
}

function validateAcceptanceEvidence(criteria, pullRequestBody) {
  const body = String(pullRequestBody || '');
  if (!/^#{1,6}\s+acceptance criteria evidence\s*$/im.test(body)) return { valid: false, missing: criteria };
  const headings = [...body.matchAll(/^#{3,6}\s+criterion:\s*(.+?)\s*$/gim)];
  const missing = criteria.filter((criterion) => {
    const heading = headings.find((match) => match[1].trim() === criterion.trim());
    if (!heading) return true;
    const start = heading.index + heading[0].length;
    const nextHeading = headings.find((match) => match.index > heading.index);
    const block = body.slice(start, nextHeading ? nextHeading.index : body.length);
    const evidence = /^\s*evidence:\s*(.+?)\s*$/im.exec(block);
    return !evidence || !evidence[1].trim() || /^<!--/.test(evidence[1].trim());
  });
  return { valid: missing.length === 0, missing };
}

async function lookupReputation(github, owner, repo, login) {
  const user = await github.rest.users.getByUsername({ username: login });
  const [events, history] = await Promise.all([
    github.rest.users.listPublicEventsForUser({ username: login, per_page: 30 }),
    github.rest.search.issuesAndPullRequests({ q: `repo:${owner}/${repo} author:${login}`, per_page: 1 }),
  ]);
  const created = new Date(user.data.created_at);
  return {
    account_age_days: Number.isNaN(created.getTime()) ? null : Math.max(0, Math.floor((Date.now() - created.getTime()) / 86400000)),
    followers: user.data.followers,
    public_repositories: user.data.public_repos,
    public_event_sample: events.data.length,
    prior_veto_items: history.data.total_count,
  };
}

async function reportComment(github, owner, repo, number, message, marker = MARKER) {
  const body = `${marker}\n${message}`;
  const comments = await github.rest.issues.listComments({ owner, repo, issue_number: number, per_page: 100 });
  const existing = comments.data.find((comment) => String(comment.body || '').includes(MARKER));
  if (existing) {
    await github.rest.issues.updateComment({ owner, repo, comment_id: existing.id, body });
  } else {
    await github.rest.issues.createComment({ owner, repo, issue_number: number, body });
  }
}

async function addReviewLabel(github, owner, repo, number, label) {
  try {
    await github.rest.issues.addLabels({ owner, repo, issue_number: number, labels: [label] });
  } catch (error) {
    if (error.status !== 404) throw error;
    await github.rest.issues.createLabel({ owner, repo, name: label, color: 'fbca04', description: 'Grey contributor awaiting maintainer review' });
    await github.rest.issues.addLabels({ owner, repo, issue_number: number, labels: [label] });
  }
}

async function commitAuthorMismatches(github, owner, repo, pullNumber, pullAuthor) {
  const commits = await github.rest.pulls.listCommits({ owner, repo, pull_number: pullNumber, per_page: 100 });
  const accountable = normalizeLogin(pullAuthor);
  return [...new Set(commits.data
    .map((commit) => commit.author && commit.author.login)
    .filter((login) => login && normalizeLogin(login) !== accountable))];
}

async function run({ github, context, core, policyPath }) {
  const { owner, repo } = context.repo;
  const pullRequest = context.payload.pull_request;
  if (!pullRequest) return;
  const policy = JSON.parse(fs.readFileSync(policyPath, 'utf8'));
  const login = pullRequest.user && pullRequest.user.login;
  const result = classifyContributor(policy, login);
  let failure = '';

  if (result.classification === 'blacklisted') {
    failure = `This PR is blocked because @${login} is blacklisted by the versioned contributor policy. Reason: ${result.reason}`;
  }

  const refs = extractIssueReferences(pullRequest.body, owner, repo);
  let criteria = [];
  let issueNumber = refs.numbers[0];
  if (!failure && (refs.malformed || refs.numbers.length === 0)) {
    failure = 'Every PR must link a GitHub issue in this repository. The issue link is missing or malformed.';
  }
  if (!failure) {
    try {
      const issue = await github.rest.issues.get({ owner, repo, issue_number: issueNumber });
      criteria = extractAcceptanceCriteria(issue.data.body);
      if (criteria.length === 0) failure = `Linked issue #${issueNumber} has no non-empty acceptance criteria checklist.`;
    } catch (error) {
      failure = `The linked issue could not be read; this check fails closed (${error.status || 'GitHub API error'}).`;
    }
  }
  const evidence = validateAcceptanceEvidence(criteria, pullRequest.body);
  if (!failure && !evidence.valid) {
    const missing = evidence.missing.join('; ');
    failure = `Acceptance-criteria evidence is missing or empty for: ${missing}`;
  }
  if (!failure && result.classification === 'grey') {
    try {
      const reputation = await lookupReputation(github, owner, repo, login);
      core.notice(`Grey contributor @${login}; reputation is advisory only: ${JSON.stringify(reputation)}`);
      const label = (policy.settings && policy.settings.maintainer_review_label) || 'needs-maintainer-review';
      await addReviewLabel(github, owner, repo, pullRequest.number, label);
      await reportComment(github, owner, repo, pullRequest.number, `Grey contributor @${login}: reputation data is advisory only. A maintainer must review this PR before merge. The linked issue and acceptance-criteria evidence were found.`);
    } catch (error) {
      failure = `Reputation lookup failed for grey contributor @${login}; the check fails closed (${error.status || 'GitHub API error'}).`;
    }
  }

  try {
    const mismatches = await commitAuthorMismatches(github, owner, repo, pullRequest.number, login);
    if (mismatches.length) await reportComment(github, owner, repo, pullRequest.number, `Accountable contributor: @${login}. Commit-author mismatch(es) detected: ${mismatches.map((name) => `@${name}`).join(', ')}. Maintainers should verify attribution.`, MISMATCH_MARKER);
  } catch (error) {
    core.warning(`Could not inspect commit authors: ${error.status || error.message}`);
  }

  if (failure) {
    await reportComment(github, owner, repo, pullRequest.number, failure);
    core.setFailed(failure);
    return;
  }
  core.notice(`Contributor governance passed for @${login} (${result.classification}); maintainers remain responsible for semantic acceptance-criteria review.`);
}

module.exports = {
  classifyContributor,
  extractIssueReferences,
  extractAcceptanceCriteria,
  validateAcceptanceEvidence,
  lookupReputation,
  run,
};
