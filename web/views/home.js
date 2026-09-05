// Home — the Plainshow dashboard adapted to networks, GPUs and Ray work.

import { el, mount, megabytes, ago, initials } from '../lib/ui.js';
import { state, refresh, on, api } from '../lib/client.js';

export async function renderHome(host) {
    await refresh();
    const page = el('div', { class: 'page' });
    mount(host, page);
    let rayJobs = [];

    const draw = () => mount(page, ...content(rayJobs));
    draw();
    api('/api/ray/jobs').then((data) => {
        rayJobs = data.jobs || [];
        draw();
    }).catch(() => {});

    const offSystem = on('system', (info) => { state.system = info; draw(); });
    const redraw = async () => { await refresh(); draw(); };
    const offProject = on('project.created', redraw);
    const offNetwork = on('networks.changed', redraw);
    const offGit = on('git.committed', redraw);
    const offRay = on('ray.changed', async () => {
        const data = await api('/api/ray/jobs').catch(() => ({ jobs: [] }));
        rayJobs = data.jobs || [];
        await redraw();
    });
    return () => { offSystem(); offProject(); offNetwork(); offGit(); offRay(); };
}

function content(rayJobs) {
    const overview = state.overview;
    const system = state.system || overview.system;
    const name = overview.account?.display_name || overview.account?.username || overview.node.name;
    const firstName = String(name).trim().split(/\s+/)[0];
    const gpus = gpuInventory(overview, system);

    return [
        el('h1', { class: 'welcome-title' }, `Good to see you, ${firstName}.`),
        el('section', { class: 'ps-metrics', 'aria-label': 'Cluster summary' },
            projectMetric(overview), networkMetric(overview), gpuMetric(gpus), systemMetric(system)),
        el('div', { class: 'ps-home-lower' },
            activityPanel(overview, rayJobs),
            el('section', {},
                el('div', { class: 'section-heading' },
                    el('strong', {}, 'Your networks'),
                    el('a', { href: '#/networks' }, 'View all ',
                        el('i', { class: 'bx bx-right-arrow-alt', 'aria-hidden': 'true' }))),
                overview.networks.length
                    ? el('div', { class: 'ps-card-grid' },
                        ...overview.networks.slice(0, 9).map(networkPreview))
                    : emptyPanel('bx-network-chart', 'No networks yet',
                        'Create or join a network to connect machines and projects.')),
            devicesSection()),
    ];
}

// devicesSection lists every machine on the account, not only the one being
// looked at. The point of the product is the other computers, so the answer to
// "what do I have" should not stop at this one.
function devicesSection() {
    const box = el('div', {});
    const section = el('section', { style: 'margin-top:26px' },
        el('div', { class: 'section-heading' },
            el('strong', {}, 'Your devices'),
            el('a', { href: '#/settings/devices' }, 'Manage ',
                el('i', { class: 'bx bx-right-arrow-alt', 'aria-hidden': 'true' }))),
        box);

    // Loaded after the page: this needs the account service, and a node that
    // has none is a working node, not a broken home page.
    api('/api/devices').then((data) => {
        const devices = data.devices || [];
        if (!devices.length) { section.remove(); return; }
        mount(box, el('div', { class: 'device-grid' },
            ...devices.map((device) => accountDeviceCard(device, data.this_device))));
    }).catch(() => section.remove());
    return section;
}

function accountDeviceCard(device, thisDevice) {
    const network = (device.networks || []).find((item) => item.id === device.active_network);
    return el('a', {
        class: 'panel device-card', href: '#/settings/devices',
        style: device.online ? '' : 'opacity:.6',
    },
        el('div', { class: 'device-card__head' },
            el('span', { class: 'avatar' }, initials(device.name)),
            el('span', { class: 'row__main' },
                el('span', { class: 'row__title' }, device.name),
                el('span', { class: 'row__meta' },
                    network ? network.name : 'no network')),
            el('span', { class: `dot ${device.online ? 'dot--on' : 'dot--off'}` }),
            device.id === thisDevice
                ? el('span', { class: 'chip chip--cyan' }, 'this machine') : null),
        el('div', { class: 'device-card__stats' },
            deviceStat(device.gpu_count, 'GPUs'),
            deviceStat(device.project_count, 'Projects'),
            deviceStat(device.online ? 'online' : ago(device.last_seen), 'Seen')));
}

function deviceStat(value, label) {
    return el('span', { class: 'device-stat' },
        el('strong', {}, value ?? '—'), el('span', {}, label));
}

function projectMetric(overview) {
    return el('a', { class: 'ps-metric-card ps-metric-card--projects ps-metric-card--lift', href: '#/projects' },
        el('div', { class: 'ps-metric-card__surface' },
            el('div', { class: 'ps-metric-head' },
                el('span', { class: 'ps-metric-card__icon' }, el('i', { class: 'bx bx-layer' })),
                el('i', { class: 'bx bx-right-arrow-alt muted' })),
            el('div', { class: 'ps-metric-values' },
                metricValue(overview.projects.length, 'Projects'),
                metricValue(overview.networks.length, 'Networks'))));
}

function networkMetric(overview) {
    return el('div', { class: 'ps-metric-card ps-metric-card--networks' },
        el('div', { class: 'ps-metric-card__surface' },
            el('div', { class: 'ps-status-list' },
                ...overview.networks.slice(0, 3).map((network) =>
                    el('a', { class: 'ps-status-row', href: `#/networks/${encodeURIComponent(network.id)}`,
                        title: network.name },
                        el('span', { class: `ps-status-dot ${network.enabled ? 'ps-status-dot--online' : 'ps-status-dot--offline'}` }),
                        el('span', { class: 'ps-status-row__main' },
                            el('span', { class: 'ps-status-row__title' }, network.name),
                            el('span', { class: 'ps-status-row__meta' },
                                `${network.node_count} devices · ${network.project_count} projects`)),
                        el('i', { class: 'bx bx-right-arrow-alt muted' }))),
                overview.networks.length === 0
                    ? el('div', { class: 'ps-status-empty' }, el('i', { class: 'bx bx-network-chart' }))
                    : null)));
}

function gpuMetric(gpus) {
    return el('div', { class: 'ps-metric-card ps-metric-card--gpus' },
        el('div', { class: 'ps-metric-card__surface' },
            el('div', { class: 'ps-status-list' },
                ...gpus.slice(0, 3).map((gpu) => el('div', { class: 'ps-status-row' },
                    el('i', { class: 'bx bx-chip ps-gpu-icon', 'aria-hidden': 'true' }),
                    el('span', { class: 'ps-status-row__main' },
                        el('span', { class: 'ps-status-row__title' }, gpu.name),
                        el('span', { class: 'ps-status-row__meta' },
                            `${gpu.machine} · ${megabytes(gpu.vram_total_mb)}`)))),
                gpus.length === 0
                    ? el('div', { class: 'ps-status-empty', title: 'No graphics card detected' },
                        el('i', { class: 'bx bx-chip' })) : null)));
}

function systemMetric(system) {
    const ram = percent(system.ram_used_mb, system.ram_total_mb);
    const disk = percent(system.disk_total_gb - system.disk_free_gb, system.disk_total_gb);
    const cpu = percent(system.load_avg_1, system.cpu_cores || 1);
    const gpu = system.gpus?.length
        ? Math.max(...system.gpus.map((item) => Number(item.util_percent) || 0)) : 0;
    return el('div', { class: 'ps-metric-card ps-metric-card--system' },
        el('div', { class: 'ps-metric-card__surface' },
            el('div', { class: 'ps-health-list' },
                healthBar('RAM used', ram, '#ef4444', '#fecaca', '#450a0a'),
                healthBar('Disk used', disk, '#f97316', '#fed7aa', '#431407'),
                healthBar('CPU load', cpu, '#eab308', '#fef08a', '#422006'),
                healthBar('GPU load', gpu, '#ec4899', '#fbcfe8', '#500724'))));
}

function metricValue(value, label) {
    return el('span', {}, el('span', { class: 'ps-metric-label' }, label),
        el('strong', { class: 'ps-metric-value' }, String(value)));
}

function healthBar(label, value, color, track, text) {
    const level = Math.round(Math.max(0, Math.min(100, value || 0)));
    return el('div', { class: 'ps-health-bar', style: `color:${text};background:${track}`,
        role: 'progressbar', 'aria-label': label, 'aria-valuenow': level,
        'aria-valuemin': '0', 'aria-valuemax': '100' },
        el('span', { class: 'ps-health-bar__fill', style: `width:${level}%;background:${color};color:${color}` }),
        el('span', { class: 'ps-health-bar__content' }, el('span', {}, label), el('strong', {}, `${level}%`)));
}

function activityPanel(overview, rayJobs) {
    const activity = [
        ...(overview.recent_commits || []).map((commit) => ({
            kind: 'commit', project: commit.project, branch: commit.branch,
            text: commit.subject, detail: `${commit.author} committed ${commit.short}`,
            at: Date.parse(commit.when) || 0,
            href: `#/projects/${encodeURIComponent(commit.project)}/git`,
        })),
        ...(overview.recent_jobs || []).map((job) => ({
            kind: 'job', project: job.project || 'Cluster', branch: '',
            text: job.title || job.command, detail: `Job ${job.state} on ${job.machine || 'this machine'}`,
            at: Date.parse(job.created_at) || 0, href: `#/jobs/${encodeURIComponent(job.id)}`,
        })),
        ...rayJobs.map((job) => ({
            kind: 'job', project: 'Ray', branch: '', text: job.entrypoint || job.id,
            detail: `Distributed job ${String(job.status || '').toLowerCase()}`,
            at: timestamp(job.started_at || job.ended_at), href: '#/jobs',
        })),
    ].sort((a, b) => b.at - a.at).slice(0, 12);

    return el('section', { class: 'ps-server-card', 'aria-label': 'Recent activity' },
        el('div', { class: 'ps-server-card__surface' }, activity.length
            ? el('div', { class: 'ps-notice-list' }, ...activity.map(activityRow))
            : el('div', { class: 'empty', style: 'min-height:176px' },
                el('i', { class: 'bx bx-broadcast empty__ico' }),
                el('span', { class: 'empty__text' }, 'Commits and jobs will appear here.'))));
}

function activityRow(item) {
    const tone = item.kind === 'commit' ? '#67e8f9' : '#fdba74';
    return el('a', { class: 'ps-notice', href: item.href },
        el('span', { class: 'ps-notice__kind', style: `color:${tone};background:${tone}12` },
            el('i', { class: `bx ${item.kind === 'commit' ? 'bx-git-commit' : 'bx-task'}` })),
        el('span', { class: 'ps-notice__main' },
            el('span', { class: 'ps-notice__top' },
                el('span', { class: 'ps-notice__project' }, item.project),
                item.branch ? el('span', { class: 'chip', style: 'padding:2px 6px;font-size:8px' }, item.branch) : null,
                el('span', { class: 'ps-notice__time' }, item.at ? ago(new Date(item.at).toISOString()) : 'recent')),
            el('span', { class: 'ps-notice__text' },
                el('strong', { style: `color:${tone}` }, item.text), ` · ${item.detail}`)),
        el('span', { class: 'ps-notice__action' }, el('i', { class: 'bx bx-right-arrow-alt' })));
}

function networkPreview(network) {
    return el('a', { class: 'panel ps-project-card', href: `#/networks/${encodeURIComponent(network.id)}` },
        el('div', { class: 'ps-project-card__top' },
            el('span', { class: 'ps-project-mark ps-network-mark' }, el('i', { class: 'bx bx-network-chart' })),
            el('span', { class: 'ps-project-card__copy' },
                el('strong', {}, network.name), el('span', {}, network.role || 'member')),
            el('span', { class: 'ps-project-card__status' },
                el('span', { class: `ps-status-dot ${network.enabled ? 'ps-status-dot--online' : 'ps-status-dot--offline'}` }),
                network.enabled ? 'Active' : 'Paused')),
        el('div', { class: 'ps-project-card__foot' },
            el('span', {}, el('i', { class: 'bx bx-devices' }), ` ${network.node_count}`),
            el('span', {}, el('i', { class: 'bx bx-chip' }), ` ${network.gpu_count}`),
            el('span', { class: 'push' }, el('i', { class: 'bx bx-layer' }), ` ${network.project_count}`),
            el('i', { class: 'bx bx-right-arrow-alt' })));
}

function gpuInventory(overview, system) {
    const inventory = [];
    for (const machine of overview.machines || []) {
        if (!machineOnline(machine)) continue;
        const items = machine.is_self ? (system.gpus || []) : (machine.capacity?.gpus || []);
        for (const gpu of items) inventory.push({ ...gpu, machine: machine.name });
    }
    if (!overview.machines?.some((machine) => machine.is_self)) {
        for (const gpu of system.gpus || []) inventory.push({ ...gpu, machine: overview.node.name });
    }
    return inventory;
}

function machineOnline(machine) {
    if (machine.is_self) return true;
    const seen = Date.parse(machine.last_seen);
    return Number.isFinite(seen) && Date.now() - seen >= 0 && Date.now() - seen < 120000;
}

function percent(value, total) {
    return total > 0 ? (Number(value || 0) / Number(total)) * 100 : 0;
}

function timestamp(value) {
    const numeric = Number(value || 0);
    return numeric > 0 && numeric < 1e12 ? numeric * 1000 : numeric;
}

function emptyPanel(icon, title, detail) {
    return el('div', { class: 'panel empty' }, el('i', { class: `bx ${icon} empty__ico` }),
        el('strong', {}, title), el('span', { class: 'empty__text' }, detail));
}
