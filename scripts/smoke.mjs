// End-to-end check against a running node.
//
// Usage:  make smoke
//     or  PSCLUSTER_URL=http://127.0.0.1:9971 node scripts/smoke.mjs
//
// The Go tests cover the packages; this covers the wiring between them and the
// exact HTTP shapes the interface depends on, which is where the bugs that
// actually reach a browser live.

const B = process.env.PSCLUSTER_URL || 'http://127.0.0.1:9971';
let pass = 0, fail = 0;
const ok = (name, cond, extra = '') => {
  if (cond) { pass++; console.log(`  ✓ ${name}`); }
  else { fail++; console.log(`  ✗ ${name} ${extra}`); }
};
/** waitFor polls until check() is truthy, so no assertion depends on a sleep
 *  being long enough on a loaded machine. */
const waitFor = async (check, timeoutMs = 15000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = await check();
    if (value) return value;
    await new Promise((r) => setTimeout(r, 50));
  }
  return null;
};
const j = async (p, o) => {
  const r = await fetch(B + p, o && o.body ? { ...o, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(o.body) } : o);
  return { status: r.status, body: await r.json().catch(() => null) };
};

console.log('\nSTATIC');
for (const [path, type] of [['/', 'text/html'], ['/app.js', 'javascript'], ['/app.css', 'css'],
                            ['/fonts.css', 'css'], ['/boxicons.css', 'css'], ['/lib/client.js', 'javascript'],
                            ['/views/home.js', 'javascript'], ['/fonts/ibm-plex-mono-400.woff2', 'font'],
                            ['/fonts/boxicons.woff2', 'font'],
                            ['/brand/plainshow-icon.webp', 'image/webp']]) {
  const r = await fetch(B + path);
  ok(`serves ${path}`, r.ok && r.headers.get('content-type').includes(type),
     `${r.status} ${r.headers.get('content-type')}`);
}
ok('SPA fallback for unknown route', (await fetch(B + '/anything')).ok);
ok('unknown API endpoint 404s', (await j('/api/nope')).status === 404);

console.log('\nOVERVIEW');
const ov = (await j('/api/overview')).body;
const defaultNetwork = ov.active_network;
ok('cluster named', typeof ov.cluster.name === 'string' && ov.cluster.name.length > 0);
// Every device is equal now: one role, and no machine is special.
ok('node is an ordinary device', JSON.stringify(ov.node.roles) === '["worker"]',
   JSON.stringify(ov.node.roles));
ok('self machine registered', ov.machines.length === 1 && ov.machines[0].is_self);
ok('system probed', ov.system.cpu_cores > 0 && ov.system.ram_total_mb > 0);
ok('git detected', ov.git_available === true);
ok('GitHub starts disconnected', ov.github.connected === false);
ok('updater is configured', ov.update.repository === 'huggan360/plainshow-cluster');

console.log('\nPROJECTS');
const created = await j('/api/projects', {
  method: 'POST', body: { name: 'demo', description: 'smoke', network_id: defaultNetwork },
});
ok('create returns 201 with timestamps', created.status === 201 && !!created.body.created_at, JSON.stringify(created.body));
ok('duplicate name refused', (await j('/api/projects', {
  method: 'POST', body: { name: 'demo', network_id: defaultNetwork },
})).status === 409);
ok('bad name refused', (await j('/api/projects', {
  method: 'POST', body: { name: '../evil', network_id: defaultNetwork },
})).status === 400);
ok('starter files present', (await j('/api/projects/demo/tree')).body.length === 2);
ok('missing project 404s', (await j('/api/projects/ghost/tree')).status === 404);
const updatedProject = await j('/api/projects/demo', {
  method: 'PUT', body: { description: 'Updated from project settings' },
});
ok('project details update', updatedProject.status === 200 &&
  updatedProject.body.description === 'Updated from project settings');

console.log('\nTEAM / GITHUB');
const members = (await j('/api/projects/demo/members')).body;
ok('new project has one owner', members.members.length === 1 && members.members[0].owner);
ok('owner has every capability', members.capabilities.every((name) =>
  members.members[0].capabilities[name] === true));
ok('GitHub status is usable without a token', (await j('/api/github')).body.connected === false);
ok('repository link requires GitHub',
  (await j('/api/projects/demo/repository', {
    method: 'POST', body: { repository: 'plainshow/demo' },
  })).status === 428);

console.log('\nFILES');
ok('write', (await j('/api/projects/demo/file', { method: 'PUT', body: { path: 'a/b/deep.py', content: 'x=1\n' } })).status === 200);
ok('read back', (await j('/api/projects/demo/file?path=a/b/deep.py')).body.content === 'x=1\n');
ok('mkdir', (await j('/api/projects/demo/dir', { method: 'POST', body: { path: 'notebooks' } })).status === 201);
ok('rename', (await j('/api/projects/demo/rename', { method: 'POST', body: { from: 'a/b/deep.py', to: 'a/b/renamed.py' } })).status === 200);
ok('delete', (await j('/api/projects/demo/entry?path=a/b/renamed.py', { method: 'DELETE' })).status === 200);
const uploadForm = new FormData();
uploadForm.append('file', new Blob(['uploaded through the browser\n']), 'notes.txt');
const uploaded = await fetch(B + '/api/projects/demo/upload?path=assets/notes.txt', {
  method: 'POST', body: uploadForm,
});
ok('multipart upload', uploaded.status === 201 && (await uploaded.json()).size === 29);
const downloaded = await fetch(B + '/api/projects/demo/raw?path=assets/notes.txt');
ok('uploaded file downloads', downloaded.ok && (await downloaded.text()) === 'uploaded through the browser\n');
ok('download names the attachment', downloaded.headers.get('content-disposition').includes('notes.txt'));
const esc = await j('/api/projects/demo/file?path=../../../etc/passwd');
ok('traversal read refused', esc.status === 404 || esc.status === 400, JSON.stringify(esc.body));

console.log('\nCOLLABORATION');
await j('/api/projects/demo/file', {
  method: 'PUT', body: { path: 'main.py', content: 'print("replacement")\n' },
});

console.log('\nGIT');
// Make a change to observe: the file operations above net out to nothing.
await j('/api/projects/demo/file', { method: 'PUT', body: { path: 'changed.py', content: 'print(1)\n' } });
const git = (await j('/api/projects/demo/git')).body;
ok('branch main', git.branch === 'main');
ok('initial commit exists', git.log.length === 1);
ok('sees uncommitted changes', git.changes.length > 0);
const c = await j('/api/projects/demo/commit', { method: 'POST', body: { message: 'smoke commit' } });
ok('commit succeeds', c.body.committed === true);
ok('history grew', (await j('/api/projects/demo/git')).body.log.length === 2);
ok('nothing left to commit', (await j('/api/projects/demo/commit', { method: 'POST', body: { message: 'again' } })).body.committed === false);

console.log('\nJOBS');
const job = (await j('/api/jobs', { method: 'POST', body: { project: 'demo', command: 'echo hello; python3 -c "print(6*7)"' } })).body;
const done = await waitFor(async () => {
  const r = (await j(`/api/jobs/${job.id}`)).body;
  return ['succeeded', 'failed', 'stopped'].includes(r.state) ? r : null;
});
ok('job succeeded', done && done.state === 'succeeded' && done.exit_code === 0, done && done.state);
const logs = (await j(`/api/jobs/${job.id}/logs`)).body;
ok('log captured', logs.map((l) => l.text).join('|') === 'hello|42', JSON.stringify(logs.map(l => l.text)));
ok('empty command refused', (await j('/api/jobs', { method: 'POST', body: { project: 'demo', command: '  ' } })).status === 400);
const failing = (await j('/api/jobs', { method: 'POST', body: { command: 'exit 3' } })).body;
const failed = await waitFor(async () => {
  const r = (await j(`/api/jobs/${failing.id}`)).body;
  return r.state === 'failed' ? r : null;
});
ok('failure keeps exit code', failed && failed.exit_code === 3, failed && failed.exit_code);

console.log('\nSTOP');
const long = (await j('/api/jobs', { method: 'POST', body: { command: 'sleep 30' } })).body;
// Wait until it is actually running before stopping it: stopping a job that has
// not spawned yet is a different code path and not what this checks.
const started = await waitFor(async () =>
  (await j(`/api/jobs/${long.id}`)).body.state === 'running' || null);
ok('job reaches running', started === true);
ok('stop accepted', (await j(`/api/jobs/${long.id}/stop`, { method: 'POST' })).status === 200);
const stopped = await waitFor(async () =>
  (await j(`/api/jobs/${long.id}`)).body.state === 'stopped' || null);
ok('job stopped', stopped === true);

console.log('\nSETTINGS / POLICY');
const settings = (await j('/api/settings')).body;
ok('terminal off by default', settings.worker.allow_terminal === false);
ok('update settings exposed', settings.update.enabled === true && settings.update.channel === 'stable');
await j('/api/settings', { method: 'PUT', body: { worker: { enabled: false, allow_jobs: true, allow_gpu: true, allow_terminal: false } } });
ok('policy refuses work when disabled', (await j('/api/jobs', { method: 'POST', body: { command: 'echo nope' } })).status === 403);
await j('/api/settings', { method: 'PUT', body: { worker: { enabled: true, allow_jobs: true, allow_gpu: true, allow_terminal: false } } });
ok('policy restored', (await j('/api/jobs', { method: 'POST', body: { command: 'true' } })).status === 201);
const updateStatus = (await j('/api/update')).body;
ok('update endpoint reports current version', !!updateStatus.current && updateStatus.enabled);
ok('invalid update repository refused', (await j('/api/settings', {
  method: 'PUT', body: { update: { ...settings.update, repository: 'not-a-repository' } },
})).status === 400);

console.log('\nRAY');
const rayStatus = (await j('/api/ray')).body;
ok('ray surface answers', typeof rayStatus.installed === 'boolean', JSON.stringify(rayStatus));
ok('ray says what to do when absent',
   rayStatus.installed || (rayStatus.advice || '').includes('Install Ray'), rayStatus.advice);
const rayJobs = (await j('/api/ray/jobs')).body;
ok('ray jobs answers without a cluster', Array.isArray(rayJobs.jobs), JSON.stringify(rayJobs));

console.log('\nDELETE PROJECT');
ok('delete project', (await j('/api/projects/demo', { method: 'DELETE' })).status === 200);
ok('gone', (await j('/api/projects/demo/tree')).status === 404);

console.log('\nMULTIPLE NETWORKS');
const beforeNetworks = (await j('/api/networks')).body;
ok('initial installation has one network', beforeNetworks.networks.length === 1);
const secondNetwork = await j('/api/networks', {
  method: 'POST', body: { name: 'Friends lab' },
});
ok('create a second network without moving device compute', secondNetwork.status === 201 &&
  (await j('/api/networks')).body.active === defaultNetwork);
ok('project list covers all account networks', (await j('/api/projects')).body.length === 0);
let secondProject;
ok('same project name is valid in another network',
  (secondProject = await j('/api/projects', {
    method: 'POST', body: { name: 'demo', network_id: secondNetwork.body.id },
  })).status === 201);
const originalTwin = await j('/api/projects', {
  method: 'POST', body: { name: 'demo', network_id: defaultNetwork },
});
ok('same name can exist in both networks', originalTwin.status === 201 &&
  (await j('/api/projects')).body.length === 2);
ok('stable project id opens the correct project',
  (await j(`/api/projects/${secondProject.body.id}/tree`)).status === 200);
ok('network has this device',
  (await j(`/api/networks/${secondNetwork.body.id}/nodes`)).body.length === 1);
ok('network has its owner account',
  (await j(`/api/networks/${secondNetwork.body.id}/members`)).body[0].role === 'owner');
const networkDetail = await j(`/api/networks/${secondNetwork.body.id}`);
ok('network workspace has projects, devices and policy', networkDetail.status === 200 &&
  networkDetail.body.projects.length === 1 && networkDetail.body.nodes.length === 1 &&
  networkDetail.body.membership.id === secondNetwork.body.id);
ok('device compute assignment can switch independently',
  (await j(`/api/networks/${defaultNetwork}/active`, { method: 'PUT' })).status === 200);
ok('switching device compute does not hide projects', (await j('/api/projects')).body.length === 2);

console.log(`\n  ${pass} passed, ${fail} failed\n`);
process.exit(fail ? 1 : 0);
