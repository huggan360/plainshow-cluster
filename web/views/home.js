// Home — the Plainshow dashboard adapted to networks, GPUs and Ray work.

import { el, mount, megabytes, ago } from '../lib/ui.js';
import { state, refresh, on, api, watchRefresh } from '../lib/client.js';
import { vendorMark } from '../lib/vendors.js';

export async function renderHome(host) {
    await refresh();
    const page = el('div', { class: 'page ps-home-page' });
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
    const offLive = watchRefresh(['peers.changed', 'connection.restored', 'project.deleted', 'project.updated', 'project.moved'], redraw);
    const offRay = on('ray.changed', async () => {
        const data = await api('/api/ray/jobs').catch(() => ({ jobs: [] }));
        rayJobs = data.jobs || [];
        await redraw();
    });
    return () => { offSystem(); offProject(); offNetwork(); offGit(); offRay(); offLive(); };
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
            networksPanel(overview)),
    ];
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
                                `${network.node_count} devices · ${network.gpu_count} GPUs`)),
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
                    vendorMark(gpu.vendor),
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
            href: `#/projects/${encodeURIComponent(commit.project_id || commit.project)}/git`,
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

    return el('section', { class: 'ps-server-card ps-home-panel', 'aria-label': 'Notifications' },
        el('div', { class: 'ps-server-card__surface ps-home-panel__surface' },
            activity.length
                ? notificationList(activity)
                : [
                    el('div', { class: 'ps-home-panel__head' },
                        el('strong', {}, 'Notifications')),
                    el('div', { class: 'empty ps-home-panel__empty' },
                        el('i', { class: 'bx bx-broadcast empty__ico' }),
                        el('span', { class: 'empty__text' }, 'Commits and jobs will appear here.')),
                ]));
}

function notificationList(activity) {
    const rows = activity.map(activityRow);
    const list = el('div', { class: 'ps-notice-list ps-notice-list--snapping' },
        el('div', { class: 'ps-home-panel__head' },
            el('strong', {}, 'Notifications')),
        ...rows);
    let wheelLocked = false;

    const rowTop = (row, index) => Number(row.offsetTop) || 62 + index * 74;
    const firstVisible = () => {
        const target = list.scrollTop + 8;
        let nearest = 0;
        let distance = Infinity;
        rows.forEach((row, index) => {
            const candidate = Math.abs(rowTop(row, index) - target);
            if (candidate < distance) {
                distance = candidate;
                nearest = index;
            }
        });
        return nearest;
    };
    const updateWindow = () => {
        if (!rows.length) return;
        const first = firstVisible();
        const bottom = list.scrollTop + (list.clientHeight || 280) - 8;
        let last = first;
        for (let index = first; index < rows.length; index++) {
            if (rowTop(rows[index], index) >= bottom) break;
            last = index;
        }
        rows.forEach((row, index) => {
            row.classList.toggle('ps-notice--first', index === first);
            row.classList.toggle('ps-notice--last', index === last);
        });
    };

    list.addEventListener('scroll', updateWindow, { passive: true });
    list.addEventListener('wheel', (event) => {
        if (Math.abs(event.deltaY) < 2 || wheelLocked) {
            if (wheelLocked) event.preventDefault();
            return;
        }
        const current = firstVisible();
        const next = Math.max(0, Math.min(rows.length - 1,
            current + (event.deltaY > 0 ? 1 : -1)));
        const target = event.deltaY < 0 && current === 0 ? 0 : rowTop(rows[next], next) - 8;
        if (target === list.scrollTop) return;
        event.preventDefault();
        wheelLocked = true;
        list.scrollTo({ top: target, behavior: 'smooth' });
        setTimeout(() => { wheelLocked = false; updateWindow(); }, 220);
    }, { passive: false });

    queueMicrotask(updateWindow);
    return list;
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

function networksPanel(overview) {
    return el('section', {
        class: 'ps-server-card ps-server-card--networks ps-home-panel',
        'aria-label': 'Your networks',
    }, el('div', { class: 'ps-server-card__surface ps-home-panel__surface' },
        el('div', { class: 'ps-home-panel__head' },
            el('strong', {}, 'Your networks')),
        overview.networks.length
            ? el('div', { class: 'ps-home-network-list' },
                ...overview.networks.slice(0, 7).map(networkPreview))
            : el('div', { class: 'empty ps-home-panel__empty' },
                el('i', { class: 'bx bx-network-chart empty__ico' }),
                el('strong', {}, 'No networks yet'),
                el('span', { class: 'empty__text' },
                    'Create or join a network to connect machines for project runs.')),
        el('a', { class: 'ps-home-view-more', href: '#/networks' },
            el('span', {}, 'View more'),
            el('i', { class: 'bx bx-right-arrow-alt', 'aria-hidden': 'true' }))));
}

function networkPreview(network) {
    return el('a', {
        class: 'ps-home-network', href: `#/networks/${encodeURIComponent(network.id)}`,
    },
        el('span', { class: 'ps-project-mark ps-network-mark ps-home-network__mark' },
            el('i', { class: 'bx bx-network-chart' })),
        el('span', { class: 'ps-home-network__main' },
            el('strong', {}, network.name),
            el('span', {}, network.role || 'member')),
        el('span', { class: 'ps-home-network__stats' },
            el('span', {}, el('i', { class: 'bx bx-devices' }), ` ${network.node_count}`),
            el('span', {}, el('i', { class: 'bx bx-chip' }), ` ${network.gpu_count}`)),
        el('span', { class: 'ps-home-network__state' },
            el('span', { class: `ps-status-dot ${network.enabled ? 'ps-status-dot--online' : 'ps-status-dot--offline'}` }),
            network.active ? 'Active' : (network.enabled ? 'Ready' : 'Paused')),
        el('i', { class: 'bx bx-right-arrow-alt ps-home-network__arrow', 'aria-hidden': 'true' }));
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
    if (typeof machine.online === 'boolean') return machine.online;
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
