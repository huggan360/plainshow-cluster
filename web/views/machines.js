// Machines — what this cluster is made of, and what each machine is doing.

import { el, mount, megabytes, meter, ago, uptime } from '../lib/ui.js';
import { api, on, state } from '../lib/client.js';

export async function renderMachines(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const machines = await api('/api/machines');
    const metersBox = el('div', { class: 'bars' });
    const gpuBox = el('div', {});

    const paint = () => {
        const sys = state.system;
        if (!sys) return;
        mount(metersBox,
            meter('CPU load', sys.load_avg_1, sys.cpu_cores || 1,
                `${sys.load_avg_1.toFixed(2)} / ${sys.cpu_cores}`, '#67e8f9'),
            meter('Memory', sys.ram_used_mb, sys.ram_total_mb || 1,
                `${megabytes(sys.ram_used_mb)} / ${megabytes(sys.ram_total_mb)}`, '#b481ff'),
            meter('Disk', sys.disk_total_gb - sys.disk_free_gb, sys.disk_total_gb || 1,
                `${(sys.disk_total_gb - sys.disk_free_gb).toFixed(0)} / ${sys.disk_total_gb.toFixed(0)} GB`,
                '#34d399'));

        mount(gpuBox, sys.gpus.length
            ? el('div', { class: 'grid grid--2' }, ...sys.gpus.map(gpuCard))
            : el('div', { class: 'empty' },
                el('span', { class: 'empty__ico' }, '▣'),
                el('span', { class: 'empty__text' },
                    'No NVIDIA GPU was detected on this machine. It can still run ' +
                    'CPU work, and it will report a GPU as soon as one is available.')));
    };

    mount(page,
        el('div', { class: 'page__head' },
            el('p', { class: 'page__eyebrow' }, 'Machines'),
            el('h1', { class: 'page__title' }, 'Machines'),
            el('p', { class: 'page__sub' },
                'Every computer in the cluster. Each one decides for itself what it ' +
                'will run — the limits live on the machine, not in the cluster.')),

        el('div', { class: 'grid grid--2', style: 'margin-bottom:14px' },
            el('div', { class: 'frame' }, el('div', { class: 'frame__in' },
                el('div', { class: 'panel__head' },
                    el('span', { class: 'grow' }, 'This machine'),
                    el('span', { class: 'chip chip--good' }, 'online')),
                hostSummary(),
                el('div', { style: 'margin-top:14px' }, metersBox))),
            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Details'), hostDetails())),

        el('div', { class: 'panel', style: 'margin-bottom:14px' },
            el('div', { class: 'panel__head' }, 'Accelerators'), gpuBox),

        el('div', { class: 'panel' },
            el('div', { class: 'panel__head' },
                el('span', { class: 'grow' }, 'Cluster members'),
                el('span', { class: 'chip' }, String(machines.length))),
            el('div', { class: 'rows' }, ...machines.map(memberRow)),
            machines.length === 1
                ? el('p', { class: 'muted', style: 'margin:14px 0 0;font-size:12.5px' },
                    'This is the only machine so far. Joining more machines arrives ' +
                    'with the networking layer — this node is complete on its own until then.')
                : null));

    paint();
    return on('system', (info) => { state.system = info; paint(); });
}

function hostSummary() {
    const sys = state.system || {};
    const o = state.overview;
    return el('div', {},
        el('div', { style: 'display:flex;align-items:baseline;gap:10px;margin-bottom:4px' },
            el('span', { style: 'font-size:19px;font-weight:700;letter-spacing:-.02em' },
                o.node.name),
            el('span', { class: 'mono', style: 'font-size:10.5px;color:#475569' },
                o.node.roles.join(' · '))),
        el('p', { class: 'mono', style: 'font-size:11px;color:#64748b;margin:0' },
            `${sys.cpu_model || ''} · ${sys.cpu_cores || 0} cores · up ${uptime(sys.uptime_sec)}`));
}

function hostDetails() {
    const sys = state.system || {};
    const o = state.overview;
    const rows = [
        ['Hostname', sys.hostname],
        ['Model', sys.model || '—'],
        ['Platform', `${sys.os}/${sys.arch}`],
        ['Kernel', sys.kernel],
        ['Node id', o.node.id],
        ['Install root', o.node.root],
        ['Version', o.version],
        ['git', o.git_available ? 'available' : 'not installed'],
    ];
    return el('dl', { class: 'kv' },
        ...rows.flatMap(([k, v]) => [el('dt', {}, k), el('dd', {}, v || '—')]));
}

function gpuCard(gpu) {
    return el('div', { class: 'panel' },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, `GPU ${gpu.index}`),
            gpu.temp_c ? el('span', { class: 'chip' }, `${gpu.temp_c}°C`) : null),
        el('div', { style: 'font-size:15px;font-weight:600;margin-bottom:12px' }, gpu.name),
        el('div', { class: 'bars' },
            meter('Utilisation', gpu.util_percent, 100, `${gpu.util_percent}%`, '#fdba74'),
            meter('VRAM', gpu.vram_used_mb, gpu.vram_total_mb || 1,
                `${megabytes(gpu.vram_used_mb)} / ${megabytes(gpu.vram_total_mb)}`, '#67e8f9')));
}

function memberRow(m) {
    return el('div', { class: 'row', style: 'cursor:default' },
        el('span', { class: `dot ${m.is_self ? 'dot--on' : 'dot--off'}` }),
        el('span', { class: 'row__main' },
            el('span', { class: 'row__title' }, m.name),
            el('span', { class: 'row__meta' },
                `${m.roles.join(' · ')} · ${m.os}/${m.arch} · seen ${ago(m.last_seen)}`)),
        m.is_self ? el('span', { class: 'chip chip--cyan' }, 'this machine') : null);
}
