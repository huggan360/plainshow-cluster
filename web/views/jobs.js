// Jobs — what Ray is running, and which machines are busy.
//
// These are Ray's jobs, reported as Ray sees them. Plainshow keeps no parallel
// job model for work Ray owns.

import { el, mount, ago } from '../lib/ui.js';
import { api, on, navigate, modal, toast } from '../lib/client.js';

export async function renderJobs(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const draw = async () => {
        const [networkData, listing] = await Promise.all([
            api('/api/networks').catch(() => ({ networks: [] })),
            api('/api/ray/jobs').catch(() => ({ jobs: [] })),
        ]);
        const statuses = await Promise.all((networkData.networks || []).map(async (network) => ({
            network,
            status: await api(`/api/ray?network_id=${encodeURIComponent(network.id)}`).catch((error) =>
                ({ detail: error.message })),
        })));
        const jobs = listing.jobs || [];
        const running = jobs.filter((j) => ['PENDING', 'RUNNING'].includes(j.status));

        mount(page,
            el('div', { class: 'page__head' },
                el('p', { class: 'page__eyebrow' }, 'Jobs'),
                el('h1', { class: 'page__title' }, 'Jobs'),
                el('p', { class: 'page__sub' },
                    'Everything Ray is running across all of your networks.')),

            statuses.length ? el('div', { class: 'ps-card-grid', style: 'margin-bottom:14px' },
                ...statuses.map(({ network, status }) => clusterCard(network, status))) : null,
            el('div', { class: 'grid grid--2', style: 'margin-bottom:14px' },
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' },
                        el('span', { class: 'grow' }, 'Running'),
                        el('span', { class: 'chip' }, String(running.length))),
                    running.length
                        ? el('div', { class: 'rows' }, ...running.map((job) => jobRow(job, draw)))
                        : empty('Nothing is running right now.')),
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' }, 'How jobs choose a network'),
                    el('p', { class: 'muted', style: 'margin:0;font-size:13px;line-height:1.65' },
                        'Every project belongs to exactly one network. Running a project sends it ' +
                        'to that network automatically; there is no workspace-wide network selector.'))),

            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Recent'),
                jobs.length
                    ? el('div', { class: 'rows' }, ...jobs.slice(0, 25).map((job) => jobRow(job, draw)))
                    : empty(listing.detail || 'Ray has not run anything yet.',
                        'Read How to for the three commands that start a run.')));
    };

    await draw();
    const off = on('ray.changed', draw);
    const timer = setInterval(draw, 5000);
    return () => { off(); clearInterval(timer); };
}

function clusterCard(network, status) {
    const ok = status.running;
    return el('div', { class: `frame ${ok ? 'frame--good' : ''}` },
        el('div', { class: 'frame__in' },
            el('div', { class: 'panel__head' },
                el('span', { class: 'grow' }, network.name),
                el('span', { class: `chip ${ok ? 'chip--good' : 'chip--warn'}` },
                    ok ? 'running' : 'not running')),
            ok
                ? el('div', {},
                    el('div', { style: 'font-size:20px;font-weight:700' },
                        `${status.total_gpu || 0} GPU · ${status.total_cpu || 0} CPU`),
                    el('p', { class: 'mono', style: 'margin:4px 0 12px;font-size:11px;color:#64748b' },
                        `head ${status.head}`),
                    el('div', { class: 'rows' }, ...(status.nodes || []).map((node) =>
                        el('div', { class: 'row', style: 'cursor:default' },
                            el('span', { class: `dot ${node.alive ? 'dot--on' : 'dot--off'}` }),
                            el('span', { class: 'row__main' },
                                el('span', { class: 'row__title mono', style: 'font-size:12px' },
                                    node.address),
                                el('span', { class: 'row__meta' },
                                    `${node.gpu} GPU · ${node.cpu} CPU`))))))
                : el('div', {},
                    el('p', { class: 'muted', style: 'margin:0 0 12px;font-size:13px;line-height:1.6' },
                        status.advice || status.detail || 'No Ray cluster for this network yet.'),
                    el('button', {
                        class: 'btn btn--primary btn--sm',
                        onclick: () => navigate(`networks/${encodeURIComponent(network.id)}`),
                    }, 'Open network'))));
}

function jobRow(job, redraw) {
    const tone = { RUNNING: 'chip--cyan', PENDING: 'chip--warn', SUCCEEDED: 'chip--good',
        FAILED: 'chip--bad', STOPPED: '' }[job.status] || '';
    return el('div', { class: 'row', style: 'cursor:default;align-items:flex-start' },
        el('span', { class: 'row__main' },
            el('span', { class: 'row__title mono', style: 'font-size:12.5px' },
                job.entrypoint || job.id),
            el('span', { class: 'row__meta' },
                [job.network_name, job.id, job.started_at ? ago(new Date(job.started_at).toISOString()) : null,
                    job.message].filter(Boolean).join(' · '))),
        el('span', { class: `chip ${tone}` }, (job.status || '').toLowerCase()),
		el('button', { class: 'btn btn--sm', onclick: () => showLogs(job) }, 'Logs'),
		['PENDING', 'RUNNING'].includes(job.status) ? el('button', {
			class: 'btn btn--sm btn--danger',
			onclick: async () => {
				try {
					await api(`/api/ray/jobs/${encodeURIComponent(job.id)}/stop?network_id=${encodeURIComponent(job.network_id || '')}`,
						{ method: 'POST' });
					toast('Ray is stopping the job.');
					await redraw();
				} catch (error) { toast(error.message, 'err'); }
			},
		}, 'Stop') : null);
}

async function showLogs(job) {
	let text = 'Loading logs…';
	try {
		const response = await api(`/api/ray/jobs/${encodeURIComponent(job.id)}/logs?network_id=${encodeURIComponent(job.network_id || '')}`);
		text = response.logs || 'This job has not written any output.';
	} catch (error) { text = error.message; }
	modal({
		title: `Logs · ${job.id}`,
		confirmLabel: 'Close',
		body: () => el('pre', { class: 'console', style: 'max-height:60vh;white-space:pre-wrap' }, text),
		onConfirm: (close) => close(),
	});
}

function empty(text, hint) {
    return el('div', { class: 'empty' },
		el('i', { class: 'bx bx-task empty__ico' }),
        el('span', { class: 'empty__text' }, text),
        hint ? el('span', { class: 'muted', style: 'font-size:12px' }, hint) : null);
}
