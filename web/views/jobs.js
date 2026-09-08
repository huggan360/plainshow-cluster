// Jobs — what the cluster is doing, what it will do next, and what it did.
//
// Three questions in that order, because that is the order somebody has them.
// The old page led with a panel explaining how a job picks a network, which is
// documentation rather than status, and put everything else behind it.
//
// These are Ray's jobs, reported as Ray sees them. Plainshow keeps no parallel
// job model for work Ray owns.

import { el, mount, ago } from '../lib/ui.js';
import { api, on, modal, toast } from '../lib/client.js';

// Running work is polled harder than finished work. Two seconds is fast enough
// that a job appearing feels immediate and slow enough not to hammer a head
// node that is busy doing the actual work.
const LIVE_POLL = 2000;
const IDLE_POLL = 10000;

export async function renderJobs(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const expanded = new Set();
    let timer = null;
    let ticker = null;

    const draw = async () => {
        const [listing, ray] = await Promise.all([
            api('/api/ray/jobs').catch((error) => ({ jobs: [], detail: error.message })),
            api('/api/ray').catch(() => ({})),
        ]);
        const jobs = listing.jobs || [];
        const queued = jobs.filter((job) => job.status === 'PENDING');
        const running = jobs.filter((job) => job.status === 'RUNNING');
        const done = jobs.filter((job) => !['PENDING', 'RUNNING'].includes(job.status));

        mount(page,
            el('div', { class: 'jobs-head' },
                el('div', {},
                    el('h1', { class: 'page__title' }, 'Jobs'),
                    el('p', { class: 'jobs-head__sub' },
                        running.length
                            ? `${running.length} running${queued.length ? `, ${queued.length} waiting` : ''}`
                            : queued.length ? `${queued.length} waiting to start`
                                : 'Nothing is running')),
                capacity(ray)),

            section('Waiting', queued, 'bx-time-five',
                'Nothing is queued.', expanded, draw),
            section('Running', running, 'bx-loader-alt',
                listing.detail || 'Nothing is running right now.', expanded, draw),
            section('Recent', done.slice(0, 30), 'bx-history',
                'Ray has not finished anything yet.', expanded, draw));

        // Elapsed times are counted here rather than fetched: a clock does not
        // need the network, and asking the cluster what time it is once a
        // second would be absurd.
        clearInterval(ticker);
        if (running.length) {
            ticker = setInterval(() => {
                for (const node of page.querySelectorAll('[data-since]')) {
                    node.textContent = elapsed(Number(node.dataset.since));
                }
            }, 1000);
        }

        clearInterval(timer);
        timer = setInterval(draw, running.length || queued.length ? LIVE_POLL : IDLE_POLL);
    };

    await draw();
    const off = on('ray.changed', draw);
    return () => { off(); clearInterval(timer); clearInterval(ticker); };
}

// capacity is the one thing worth saying about the cluster on this page: how
// much of it there is. Everything else about a cluster belongs on Networks.
function capacity(ray) {
    if (!ray || !ray.running) {
        return el('div', { class: 'jobs-capacity jobs-capacity--off' },
            el('i', { class: 'bx bx-broadcast' }),
            el('span', {}, ray && ray.advice ? 'Ray is not running' : 'No cluster'));
    }
    const machines = Array.isArray(ray.nodes) ? ray.nodes.length : 0;
    return el('div', { class: 'jobs-capacity' },
        stat(machines || 1, machines === 1 ? 'machine' : 'machines'),
        stat(ray.total_cpu || 0, 'CPU'),
        stat(ray.total_gpu || 0, 'GPU'));
}

function stat(value, label) {
    return el('span', { class: 'jobs-capacity__stat' },
        el('strong', {}, String(value)), el('span', {}, label));
}

function section(title, jobs, icon, emptyText, expanded, redraw) {
    return el('section', { class: 'jobs-section' },
        el('div', { class: 'jobs-section__head' },
            el('i', { class: `bx ${icon}` }),
            el('span', { class: 'jobs-section__title' }, title),
            el('span', { class: 'jobs-section__count' }, String(jobs.length))),
        jobs.length
            ? el('div', { class: 'jobs-list' },
                ...jobs.map((job) => jobRow(job, expanded, redraw)))
            : el('p', { class: 'jobs-empty' }, emptyText));
}

function jobRow(job, expanded, redraw) {
    const status = (job.status || '').toUpperCase();
    const live = status === 'RUNNING';
    const waiting = status === 'PENDING';
    const open = expanded.has(job.id);

    const toggle = () => {
        if (open) expanded.delete(job.id); else expanded.add(job.id);
        redraw();
    };

    const row = el('div', { class: `job ${open ? 'job--open' : ''} job--${status.toLowerCase()}` },
        el('button', { class: 'job__line', onclick: toggle },
            live
                ? el('i', { class: 'bx bx-loader-alt job__spin' })
                : el('span', { class: `job__dot job__dot--${status.toLowerCase()}` }),
            el('span', { class: 'job__what' },
                el('span', { class: 'job__cmd' }, job.entrypoint || job.id),
                el('span', { class: 'job__meta' },
                    [job.network_name, job.id].filter(Boolean).join(' · '))),
            el('span', {
                class: 'job__time', dataset: job.started_at ? { since: String(job.started_at) } : {},
            }, timeFor(job)),
            el('span', { class: `job__state job__state--${status.toLowerCase()}` },
                waiting ? 'waiting' : status.toLowerCase()),
            // Written out rather than built from a fragment: a class assembled
            // at runtime is invisible to the check that verifies every icon
            // this interface asks for actually exists.
            el('i', { class: `bx ${open ? 'bx-chevron-down' : 'bx-chevron-right'} job__chev` })),
        open ? logPane(job, live) : null,
        job.message && !open
            ? el('p', { class: 'job__message' }, job.message)
            : null);

    if (open) {
        row.append(el('div', { class: 'job__actions' },
            el('button', { class: 'btn btn--sm', onclick: () => showLogs(job) },
                el('i', { class: 'bx bx-file' }), 'Full output'),
            el('span', { class: 'push' }),
            live || waiting
                ? el('button', {
                    class: 'btn btn--sm btn--danger',
                    onclick: async () => {
                        try {
                            await api(`/api/ray/jobs/${encodeURIComponent(job.id)}/stop` +
                                `?network_id=${encodeURIComponent(job.network_id || '')}`,
                                { method: 'POST' });
                            toast('Ray is stopping the job.');
                            await redraw();
                        } catch (error) { toast(error.message, 'err'); }
                    },
                }, el('i', { class: 'bx bx-stop-circle' }), 'Stop')
                : null));
    }
    return row;
}

// logPane tails the output in place. A running job's last few lines are what
// somebody actually wants — opening a dialog to read them, and reopening it to
// see whether anything changed, is the thing this page was worst at.
function logPane(job, live) {
    const box = el('pre', { class: 'job__log' }, 'Loading output…');
    let stopped = false;

    const load = async () => {
        if (stopped) return;
        try {
            const response = await api(`/api/ray/jobs/${encodeURIComponent(job.id)}/logs` +
                `?network_id=${encodeURIComponent(job.network_id || '')}`);
            const text = (response.logs || '').trimEnd();
            const lines = text ? text.split('\n') : [];
            mount(box, document.createTextNode(
                lines.length ? lines.slice(-14).join('\n') : 'No output yet.'));
            box.scrollTop = box.scrollHeight;
        } catch (error) {
            mount(box, document.createTextNode(error.message));
        }
        if (live && !stopped) setTimeout(load, 2000);
    };
    load();
    box.addEventListener('plainshow:gone', () => { stopped = true; });
    return box;
}

async function showLogs(job) {
    let text = 'Loading output…';
    try {
        const response = await api(`/api/ray/jobs/${encodeURIComponent(job.id)}/logs` +
            `?network_id=${encodeURIComponent(job.network_id || '')}`);
        text = response.logs || 'This job has not written any output.';
    } catch (error) { text = error.message; }
    modal({
        title: job.entrypoint || job.id,
        confirmLabel: 'Close',
        body: () => el('pre', { class: 'console', style: 'max-height:60vh;white-space:pre-wrap' }, text),
        onConfirm: (close) => close(),
    });
}

// timeFor says the one number that matters for each state: how long it has been
// going, or how long it took.
function timeFor(job) {
    const status = (job.status || '').toUpperCase();
    if (status === 'PENDING') return 'queued';
    if (status === 'RUNNING') return job.started_at ? elapsed(job.started_at) : 'starting';
    if (job.started_at && job.ended_at) return duration(job.ended_at - job.started_at);
    return job.ended_at ? ago(new Date(job.ended_at).toISOString()) : '';
}

function elapsed(startedAt) {
    return duration(Date.now() - startedAt);
}

function duration(milliseconds) {
    const seconds = Math.max(0, Math.round(milliseconds / 1000));
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes}m ${String(seconds % 60).padStart(2, '0')}s`;
    return `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, '0')}m`;
}
