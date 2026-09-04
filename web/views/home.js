// Home — the cluster at a glance: this machine, what is running, what exists.

import { el, mount, megabytes, meter, ago, stateDot, uptime } from '../lib/ui.js';
import { state, refresh, on, navigate, api, toast } from '../lib/client.js';

/** rayCard is where a machine joins its network's Ray cluster. */
function rayCard() {
    const box = el('div', { class: 'panel' });

    const load = async () => {
        let status = {};
        try {
            status = await api('/api/ray');
        } catch (err) {
            mount(box, el('div', { class: 'panel__head' }, 'Ray'),
                el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' }, err.message));
            return;
        }
        const running = Boolean(status.running);

        const act = async (path, label) => {
            try {
                toast(label);
                await api(path, { method: 'POST', body: {} });
                await load();
            } catch (err) {
                toast(err.message, 'err');
                await load();
            }
        };

        mount(box,
            el('div', { class: 'panel__head' },
                el('span', { class: 'grow' }, 'Ray'),
                el('span', { class: `chip ${running ? 'chip--good' : 'chip--warn'}` },
                    running ? 'running' : 'not running')),
            running
                ? el('div', {},
                    el('div', { style: 'font-size:18px;font-weight:700' },
                        `${status.total_gpu || 0} GPU · ${status.total_cpu || 0} CPU`),
                    el('p', { class: 'mono', style: 'margin:4px 0 0;font-size:11px;color:#64748b' },
                        `head ${status.head || 'unknown'}`))
                : el('p', { class: 'muted', style: 'margin:0 0 12px;font-size:12.5px;line-height:1.6' },
                    status.advice || status.detail ||
                    'This machine is not part of a Ray cluster yet.'),
            el('div', { style: 'display:flex;gap:8px;margin-top:14px' },
                status.installed && !running
                    ? el('button', {
                        class: 'btn btn--primary btn--sm',
                        onclick: () => act('/api/ray/start', 'Starting Ray…'),
                    }, status.head ? 'Join the cluster' : 'Start Ray')
                    : null,
                running
                    ? el('button', {
                        class: 'btn btn--sm',
                        onclick: () => act('/api/ray/stop', 'Stopping Ray…'),
                    }, 'Leave the cluster')
                    : null,
                el('button', { class: 'btn btn--sm', onclick: () => navigate('howto') },
                    'How to run something')));
    };

    load();
    return box;
}

export async function renderHome(host) {
    await refresh();
    const page = el('div', { class: 'page' });
    mount(host, page);

    const draw = () => {
        mount(page, ...content());
        // Paint the meters from the snapshot we already have, rather than
        // leaving them blank until the first telemetry tick arrives.
        paintMeters(page);
    };
    draw();

    // Redraw on anything that changes what this page shows. Telemetry updates
    // the meters in place rather than rebuilding, so the page never flickers.
    const offSystem = on('system', (info) => { state.system = info; paintMeters(page); });
    const offJob = on('job.state', async () => { await refresh(); draw(); });
    const offProject = on('project.created', async () => { await refresh(); draw(); });
    return () => { offSystem(); offJob(); offProject(); };
}

function content() {
    const o = state.overview;
    const sys = state.system || o.system;

    return [
        el('div', { class: 'page__head' },
            el('p', { class: 'page__eyebrow' }, 'Cluster'),
            el('h1', { class: 'page__title' }, o.cluster.name),
            el('p', { class: 'page__sub' },
                `${o.machines.length} machine${o.machines.length === 1 ? '' : 's'} · ` +
                `${o.projects.length} project${o.projects.length === 1 ? '' : 's'} · ` +
                `${o.active_jobs.length} running`)),

        el('div', { class: 'grid grid--2', style: 'margin-bottom:14px' },
            rayCard(),
            machineCard(o, sys),
            runningCard(o)),

        el('div', { class: 'grid grid--2' },
            projectsCard(o),
            recentCard(o)),
    ];
}

function machineCard(o, sys) {
    const gpuLine = sys.gpus.length
        ? sys.gpus.map((g) => `${g.name} ${megabytes(g.vram_total_mb)}`).join(' · ')
        : 'No GPU detected';

    return el('div', { class: 'frame' }, el('div', { class: 'frame__in' },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'This machine'),
            el('span', { class: 'chip chip--good' }, 'online')),
        el('div', { style: 'display:flex;align-items:baseline;gap:10px;margin-bottom:4px' },
            el('span', { style: 'font-size:19px;font-weight:700;letter-spacing:-.02em' }, o.node.name),
            el('span', { class: 'mono', style: 'font-size:10.5px;color:#475569' },
                o.node.roles.join(' · '))),
        el('p', { class: 'mono', style: 'font-size:11px;color:#64748b;margin:0 0 14px' },
            `${sys.cpu_model || sys.arch} · ${sys.cpu_cores} cores · up ${uptime(sys.uptime_sec)}`),
        el('div', { class: 'bars', id: 'meters' }),
        el('p', { class: 'mono', style: 'font-size:11px;color:#475569;margin:12px 0 0' }, gpuLine)));
}

/** paintMeters redraws only the resource bars, from the latest telemetry. */
export function paintMeters(root) {
    const host = (root || document).querySelector('#meters');
    if (!host) return;
    const sys = state.system;
    if (!sys) return;

    const bars = [
        meter('CPU load', sys.load_avg_1, sys.cpu_cores || 1,
            `${sys.load_avg_1.toFixed(2)} / ${sys.cpu_cores}`, '#67e8f9'),
        meter('Memory', sys.ram_used_mb, sys.ram_total_mb || 1,
            `${megabytes(sys.ram_used_mb)} / ${megabytes(sys.ram_total_mb)}`, '#b481ff'),
        meter('Disk', sys.disk_total_gb - sys.disk_free_gb, sys.disk_total_gb || 1,
            `${(sys.disk_total_gb - sys.disk_free_gb).toFixed(0)} / ${sys.disk_total_gb.toFixed(0)} GB`,
            '#34d399'),
    ];
    for (const gpu of sys.gpus) {
        bars.push(meter(`GPU ${gpu.index}`, gpu.util_percent, 100,
            `${gpu.util_percent}% · ${megabytes(gpu.vram_used_mb)}`, '#fdba74'));
    }
    mount(host, ...bars);
}

function runningCard(o) {
    const body = o.active_jobs.length
        ? el('div', { class: 'rows' }, ...o.active_jobs.map(jobRow))
        : el('div', { class: 'empty' },
            el('span', { class: 'empty__ico' }, '○'),
            el('span', { class: 'empty__text' }, 'Nothing is running right now.'));

    return el('div', { class: 'panel' },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'Running'),
            el('span', { class: 'chip' }, String(o.active_jobs.length))),
        body);
}

function jobRow(job) {
    return el('a', { class: 'row', href: `#/jobs/${job.id}` },
        stateDot(job.state),
        el('span', { class: 'row__main' },
            el('span', { class: 'row__title' }, job.title || job.command),
            el('span', { class: 'row__meta' },
                `${job.project || 'no project'} · ${job.machine} · ${ago(job.created_at)}`)));
}

function projectsCard(o) {
    const body = o.projects.length
        ? el('div', { class: 'rows' }, ...o.projects.slice(0, 6).map((p) =>
            el('a', { class: 'row', href: `#/workspace/${encodeURIComponent(p.name)}` },
                el('span', { class: 'dot dot--off' }),
                el('span', { class: 'row__main' },
                    el('span', { class: 'row__title' }, p.name),
                    el('span', { class: 'row__meta' },
                        p.description || `updated ${ago(p.updated_at)}`)))))
        : el('div', { class: 'empty' },
            el('span', { class: 'empty__ico' }, '◫'),
            el('span', { class: 'empty__text' },
                'No projects yet. A project is a folder of code you can edit and run.'),
            el('button', {
                class: 'btn btn--primary btn--sm',
                onclick: () => navigate('workspace'),
            }, 'Go to Workspace'));

    return el('div', { class: 'panel' },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'Projects'),
            el('a', { class: 'chip chip--cyan', href: '#/workspace' }, 'open')),
        body);
}

function recentCard(o) {
    const jobs = o.recent_jobs.filter((j) => j.state !== 'running' && j.state !== 'queued');
    const body = jobs.length
        ? el('div', { class: 'rows' }, ...jobs.slice(0, 6).map(jobRow))
        : el('div', { class: 'empty' },
            el('span', { class: 'empty__ico' }, '▤'),
            el('span', { class: 'empty__text' }, 'Nothing has run on this node yet.'));

    return el('div', { class: 'panel' },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'Recent'),
            el('a', { class: 'chip chip--cyan', href: '#/jobs' }, 'all jobs')),
        body);
}
