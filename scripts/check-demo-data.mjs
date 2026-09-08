const DEMO = process.env.DEMO_DIR || 'dist/demo';

// Load the stub in a fake browser and confirm it answers the endpoints the
// interface actually calls, with the shapes the views read.
const listeners = {};
globalThis.window = {
    addEventListener: (name, fn) => { listeners[name] = fn; },
    location: { protocol: 'http:', host: 'demo' },
};
globalThis.document = { createElement: () => ({ classList: {}, append() {} }), body: { append() {} } };
globalThis.Response = class {
    constructor(body, init) { this.body = body; this.status = init.status; }
    async text() { return this.body; }
    async json() { return JSON.parse(this.body); }
    get ok() { return this.status < 400; }
};

const source = await import('node:fs').then((fs) => fs.readFileSync(`${DEMO}/demo-api.js`, 'utf8'));
new Function('window', 'document', 'Response', source)(window, document, Response);

const endpoints = [
    ['/api/auth/status', (d) => d.authenticated === true],
    ['/api/overview', (d) => d.projects.length === 3 && d.networks.length === 2 && d.system.gpus.length === 1],
    ['/api/projects', (d) => Array.isArray(d) && d.some((p) => p.has_files === false)],
    ['/api/networks', (d) => d.networks.length === 2],
    ['/api/devices', (d) => d.devices.length === 3 && d.this_device],
    ['/api/invitations', (d) => d.invitations.length === 1],
    ['/api/tailnet', (d) => d.self.address === '100.64.0.1'],
    ['/api/ray', (d) => d.running === true],
    ['/api/github', (d) => d.connected === true],
    ['/api/settings', (d) => d.worker && d.update && d.auth],
    ['/api/service', (d) => d.managed === true],
    ['/api/service/uninstall', (d) => d.options.length === 2],
    ['/api/projects-orphans', (d) => d.orphans.length === 1],
    ['/api/downloads', (d) => Array.isArray(d.downloads)],
    ['/api/accounts/search?q=alb', (d) => d.accounts.length === 1],
];

let bad = 0;
for (const [path, check] of endpoints) {
    const response = await window.fetch(path);
    const data = await response.json();
    const ok = response.status === 200 && check(data);
    if (!ok) { console.log('FAIL', path, response.status, JSON.stringify(data).slice(0, 120)); bad++; }
}
// A write must be refused, and refused in words.
const write = await window.fetch('/api/projects', { method: 'POST' });
if (write.status !== 400 || !(await write.json()).error.includes('demonstration')) {
    console.log('FAIL: a write was not refused clearly'); bad++;
}
console.log(bad ? `${bad} failed` : `demo stub: ${endpoints.length + 1} checks passed`);
process.exit(bad ? 1 : 0);
