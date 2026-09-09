const markers = /(?:#|\/\/)\s*nosec\b|\/\/\s*nolint\b|\/\/\s*NOSONAR\b/i;
const issueURLPattern = /^https:\/\/github\.com\/([^/]+)\/([^/]+)\/issues\/(\d+)$/;
const requiredFields = ['location', 'rationale', 'owner', 'removal_condition'];

function suppressionLocations(files) {
  const locations = new Set();
  for (const file of files) {
    if (!file.filename.endsWith('.go')) continue;
    collectFileLocations(file, locations);
  }
  return locations;
}

function collectFileLocations(file, locations) {
  if (typeof file.patch !== 'string') throw truncatedPatchError(file.filename);
  const rows = file.patch.split('\n');
  if (!Number.isInteger(file.changes) || changedRowCount(rows) !== file.changes) throw truncatedPatchError(file.filename);
  let line = 0;
  for (const row of rows) line = nextPatchLine(row, line, file.filename, locations);
}

function changedRowCount(rows) {
  return rows.filter((row) => (row.startsWith('+') && !row.startsWith('+++')) || (row.startsWith('-') && !row.startsWith('---'))).length;
}

function nextPatchLine(row, line, filename, locations) {
  const hunk = row.match(/^@@ .*\+(\d+)/);
  if (hunk) return Number(hunk[1]);
  if (row.startsWith('+') && !row.startsWith('+++')) {
    if (markers.test(row)) locations.add(`${filename}:${line}`);
    return line + 1;
  }
  return row.startsWith(' ') ? line + 1 : line;
}

function truncatedPatchError(filename) {
  return new Error(`patch is missing or truncated for ${filename}; cannot verify suppressions safely`);
}

function parseManifest(content) {
  if (Array.isArray(content.data) || content.data.encoding !== 'base64') throw new Error('suppression manifest is missing');
  const manifest = JSON.parse(Buffer.from(content.data.content, 'base64').toString('utf8'));
  if (!manifest || typeof manifest !== 'object' || !Array.isArray(manifest.suppressions)) throw new Error('suppression manifest must contain a suppressions array');
  const byLocation = new Map();
  for (const entry of manifest.suppressions) {
    if (!entry || typeof entry !== 'object') throw new Error('suppression manifest contains a non-object entry');
    if (requiredFields.some((field) => typeof entry[field] !== 'string' || !entry[field].trim())) throw new Error(`suppression manifest has incomplete accountability fields for ${entry.location || '<unknown>'}`);
    if (Object.keys(entry).some((field) => ![...requiredFields, 'issue'].includes(field))) throw new Error(`suppression manifest has unexpected fields for ${entry.location}`);
    if (byLocation.has(entry.location)) throw new Error(`duplicate suppression manifest location: ${entry.location}`);
    byLocation.set(entry.location, entry);
  }
  return byLocation;
}

async function trackLocation(github, context, location, entry) {
  const issueMatch = issueURLPattern.exec(String(entry.issue || ''));
  if (entry.issue && !issueMatch) throw new Error(`suppression issue for ${location} must be a GitHub issue URL`);
  const marker = `<!-- wtgc-suppression-accountability:${location} -->`;
  const body = `${marker}\nLocation: ${location}\n\nRationale: ${entry.rationale}\n\nOwner: ${entry.owner}\n\nRemoval condition: ${entry.removal_condition}`;
  if (!issueMatch) return trackUnlinkedLocation(github, context, location, body);
  const [owner, repo, issueNumber] = issueMatch.slice(1);
  if (owner !== context.repo.owner || repo !== context.repo.repo) throw new Error(`suppression issue for ${location} must belong to this repository`);
  const issue_number = Number(issueNumber);
  await github.rest.issues.get({ owner, repo, issue_number });
  const comments = await github.paginate(github.rest.issues.listComments, { owner, repo, issue_number, per_page: 100 });
  const existing = comments.find((comment) => comment.body?.includes(marker));
  if (existing) return github.rest.issues.updateComment({ owner, repo, comment_id: existing.id, body });
  return github.rest.issues.createComment({ owner, repo, issue_number, body });
}

async function trackUnlinkedLocation(github, context, location, body) {
  const title = `Track static-analysis suppression: ${location}`;
  const issues = await github.paginate(github.rest.issues.listForRepo, { owner: context.repo.owner, repo: context.repo.repo, state: 'open', per_page: 100 });
  const existing = issues.find((issue) => issue.title === title);
  const created = existing || (await github.rest.issues.create({ owner: context.repo.owner, repo: context.repo.repo, title, body })).data;
  if (existing) await github.rest.issues.update({ owner: context.repo.owner, repo: context.repo.repo, issue_number: existing.number, body });
  throw new Error(`tracking issue ${created.html_url} must be added to .ci/static-suppressions.json and rerun`);
}

async function trackSuppressions(github, context) {
  const pr = context.payload.pull_request;
  const files = await github.paginate(github.rest.pulls.listFiles, { owner: context.repo.owner, repo: context.repo.repo, pull_number: pr.number, per_page: 100 });
  const content = await github.rest.repos.getContent({ owner: context.repo.owner, repo: context.repo.repo, path: '.ci/static-suppressions.json', ref: pr.head.sha });
  const manifest = parseManifest(content);
  for (const location of suppressionLocations(files)) {
    const entry = manifest.get(location);
    if (!entry) throw new Error(`missing accountability manifest entry for ${location}`);
    await trackLocation(github, context, location, entry);
  }
}

module.exports = { trackSuppressions };
