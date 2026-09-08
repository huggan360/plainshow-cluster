// A Plainshow Cluster node that does not exist.
//
// This is deliberately not a second implementation of the interface. It is the
// interface — the same app.js, the same views, the same stylesheet — with the
// node replaced. A demo that reimplemented the pages would start drifting from
// the real ones the day after it was written, and a demo that has quietly
// stopped matching the product is worse than none.
//
// So this stubs exactly two things the browser uses to reach a node: fetch, and
// WebSocket. Everything above them is the real program.

(function demoNode() {
    const now = () => new Date().toISOString();
    const ago = (minutes) => new Date(Date.now() - minutes * 60000).toISOString();

    const state = {
        networks: [
            { id: 'net-lab', name: 'Research lab', role: 'owner', enabled: true,
              active: true, node_count: 3, gpu_count: 4, project_count: 2 },
            { id: 'net-albin', name: "Albin's network", role: 'member', enabled: true,
              active: false, node_count: 2, gpu_count: 1, project_count: 1 },
        ],
        projects: [
            { id: 'p-vision', name: 'vision', description: 'Image model experiments',
              repository: 'huggan360/vision', branch: 'main', has_files: true,
              size_kb: 18432, updated_at: ago(12), path: '~/Plainshow/Projects/vision' },
            { id: 'p-vision-dev', name: 'vision@dev', description: 'Image model experiments',
              repository: 'huggan360/vision', branch: 'dev', has_files: true,
              size_kb: 18010, updated_at: ago(90), path: '~/Plainshow/Projects/vision@dev' },
            { id: 'p-speech', name: 'speech', description: 'Made on the stationary',
              repository: 'huggan360/speech', branch: 'main', has_files: false,
              size_kb: 240128, updated_at: ago(300), path: '' },
        ],
        devices: [
            { id: 'd-stationary', name: 'hugo-stationary', os: 'linux', arch: 'amd64',
              gpu_count: 1, project_count: 2, online: true, version: '0.1.2',
              active_network: 'net-lab', desired_network: '', last_seen: now(),
              networks: [{ id: 'net-lab', name: 'Research lab' }] },
            { id: 'd-laptop', name: 'hugo-laptop', os: 'linux', arch: 'amd64',
              gpu_count: 1, project_count: 1, online: true, version: '0.1.2',
              active_network: 'net-lab', desired_network: '', last_seen: ago(1),
              networks: [{ id: 'net-lab', name: 'Research lab' }] },
            { id: 'd-albin', name: 'albin-desktop', os: 'linux', arch: 'amd64',
              gpu_count: 2, project_count: 1, online: false, version: '0.1.2',
              active_network: 'net-albin', desired_network: '', last_seen: ago(220),
              networks: [{ id: 'net-albin', name: "Albin's network" }] },
        ],
        invitations: [
            { id: 'inv-1', network_id: 'net-albin', network_name: "Albin's network",
              username: 'huggan360', display_name: 'Hugo', invited_by_name: 'Albin',
              role: 'operator', status: 'pending', created_at: ago(4) },
        ],
        jobs: [],
    };

    const system = {
        os: 'linux', arch: 'amd64', cpu_cores: 16, ram_total_mb: 32768,
        ram_used_mb: 11400, disk_total_gb: 930, disk_free_gb: 402, load_avg_1: 2.4,
        gpus: [{ index: 0, name: 'NVIDIA GeForce RTX 5060', vendor: 'nvidia',
                 trainable: true, vram_total_mb: 8151, vram_used_mb: 1024,
                 util_percent: 37, temp_c: 52 }],
    };

    const overview = () => ({
        cluster: { id: 'net-lab', name: 'Research lab' },
        node: { id: 'd-stationary', name: 'hugo-stationary', roles: ['worker'],
                root: '/opt/plainshow-cluster' },
        account: { id: 'a1', username: 'huggan360', display_name: 'Hugo' },
        version: '0.1.2-demo',
        networks: state.networks,
        networks_error: '',
        active_network: 'net-lab',
        machines: [], projects: state.projects,
        active_jobs: state.jobs, recent_jobs: [],
        recent_commits: [
            { project: 'vision', branch: 'main', subject: 'Shorter warmup',
              author: 'Hugo', short: '9f21c4a', when: ago(12) },
            { project: 'speech', branch: 'main', subject: 'First pass at the loader',
              author: 'Albin', short: '3c80f11', when: ago(300) },
        ],
        system, git_available: true,
        github: { connected: true },
        update: { current: '0.1.2-demo', latest: '0.1.2-demo', available: false },
        controller: null,
    });

    // routes are matched in order; the first pattern that matches answers.
    const routes = [
        ['GET', /^\/api\/auth\/status$/, () => ({ enabled: true, authenticated: true, central: true })],
        ['GET', /^\/api\/overview$/, overview],
        ['GET', /^\/api\/sysinfo$/, () => system],
        ['GET', /^\/api\/networks$/, () => ({ active: 'net-lab', networks: state.networks })],
        ['GET', /^\/api\/invitations$/, () => ({ invitations: state.invitations })],
        ['GET', /^\/api\/projects$/, () => state.projects],
        ['GET', /^\/api\/devices$/, () => ({ devices: state.devices, this_device: 'd-stationary' })],
        ['GET', /^\/api\/downloads$/, () => ({ downloads: [] })],
        ['GET', /^\/api\/projects-orphans$/, () => ({
            orphans: [{ name: 'old-experiment', path: '~/Plainshow/Projects/old-experiment', size_kb: 5120 }],
            size_kb: 5120,
        })],
        ['GET', /^\/api\/tailnet$/, () => ({
            installed: true, running: true, state: 'Running',
            self: { hostname: 'hugo-stationary', address: '100.64.0.1' },
            peers: [{ hostname: 'albin-desktop', address: '100.64.0.3', relayed: false }],
        })],
        ['GET', /^\/api\/ray$/, () => ({
            installed: true, running: true, head: '100.64.0.1:6379',
            head_node: 'd-stationary', is_head: true,
            nodes: [{}, {}, {}], total_cpu: 40, total_gpu: 4, eligible: true,
            policy: { allow_gpu: true },
        })],
        ['GET', /^\/api\/ray\/jobs$/, () => ({ jobs: [], running: true })],
        ['GET', /^\/api\/github$/, () => ({
            connected: true, account: 'huggan360', name: 'Hugo', email: '',
            repositories: { total: 24, private: 9, admin: 24 }, git_ready: true,
        })],
        ['GET', /^\/api\/github\/repositories$/, () => ([
            { full_name: 'huggan360/vision', name: 'vision', private: true,
              description: 'Image model experiments', pushed_at: ago(12) },
            { full_name: 'huggan360/speech', name: 'speech', private: false,
              description: 'Speech models', pushed_at: ago(300) },
        ])],
        ['GET', /^\/api\/accounts\/search/, () => ({
            accounts: [{ id: 'a2', username: 'albin', display_name: 'Albin',
                         github_login: 'albin-gh' }],
        })],
        ['GET', /^\/api\/settings$/, () => ({
            node: { id: 'd-stationary', name: 'hugo-stationary', roles: ['worker'] },
            cluster: { id: 'net-lab', name: 'Research lab' },
            network: { bind: '127.0.0.1', port: 41297 },
            worker: { enabled: true, allow_jobs: true, allow_gpu: true,
                      allow_terminal: false, allow_project_sync: true },
            update: { enabled: true, repository: 'huggan360/plainshow-cluster',
                      channel: 'stable', check_every: '6h', automatic: false },
            auth: { remember_this_machine: true },
            root: '/opt/plainshow-cluster', version: '0.1.2-demo',
            paths: { config: '/opt/plainshow-cluster/config.yaml',
                     database: '/opt/plainshow-cluster/data/cluster.db',
                     projects: '/opt/plainshow-cluster/projects' },
        })],
        ['GET', /^\/api\/update$/, () => ({
            current: '0.1.2-demo', latest: '0.1.2-demo', available: false, checking: false,
        })],
        ['GET', /^\/api\/service$/, () => ({
            managed: true, unit: 'plainshow-cluster.service',
            boot_enabled: true, can_change: true, detail: '',
        })],
        ['GET', /^\/api\/service\/uninstall$/, () => ({
            root: '/opt/plainshow-cluster', unit: 'plainshow-cluster.service',
            managed: true, package_manager: 'pacman', can_remove: true,
            options: [
                { id: 'ray', name: 'Ray', present: true,
                  detail: "The runtime that runs your work. Installed inside this node's own directory." },
                { id: 'tailscale', name: 'Tailscale', present: true,
                  detail: 'The private network client.',
                  warn: 'System-wide. Other things on this machine may be using it.' },
            ],
            always: ['/opt/plainshow-cluster', 'the systemd unit, if there is one',
                     '/usr/local/bin/pscluster', 'the desktop entry and its icons'],
        })],
    ];

    const demoOnly = () => {
        throw new Error('This is a demonstration, so nothing here actually changes.');
    };

    window.fetch = async (input, init = {}) => {
        const url = typeof input === 'string' ? input : input.url;
        const path = url.replace(/^https?:\/\/[^/]+/, '');
        const method = (init.method || 'GET').toUpperCase();
        await new Promise((resolve) => setTimeout(resolve, 140 + Math.random() * 220));

        for (const [verb, pattern, answer] of routes) {
            if (verb === method && pattern.test(path.split('?')[0])) {
                return json(200, answer());
            }
        }
        if (method !== 'GET') {
            return json(400, { error: 'This is a demonstration, so nothing here actually changes.' });
        }
        return json(404, { error: 'No such endpoint in the demonstration.' });
    };

    function json(status, body) {
        return new Response(JSON.stringify(body), {
            status, headers: { 'Content-Type': 'application/json' },
        });
    }

    // The interface opens a socket and reconnects forever if it fails. A stub
    // that reports itself open and then says nothing is the honest shape: a
    // quiet cluster, rather than a broken one.
    window.WebSocket = class DemoSocket {
        constructor() {
            this.readyState = 1;
            setTimeout(() => this.onopen && this.onopen(), 30);
        }
        send() {}
        close() { this.readyState = 3; }
    };
    window.WebSocket.OPEN = 1;

    // A banner, so nobody mistakes this for their own machine.
    window.addEventListener('DOMContentLoaded', () => {
        const banner = document.createElement('div');
        banner.className = 'demo-banner';
        banner.textContent = 'Demonstration — sample data, nothing here is a real machine';
        document.body.append(banner);
    });

    void demoOnly;
})();
