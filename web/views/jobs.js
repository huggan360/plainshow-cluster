// Jobs — what the cluster is doing, what it will do next, and what it did.
//
// Three questions in that order, because that is the order somebody has them.
// The old page led with a panel explaining how a job picks a network, which is
// documentation rather than status, and put everything else behind it.
//
// Ray owns execution. The account server retains summaries after a head stops.

import { el, mount, ago } from '../lib/ui.js';
import { api, on, modal, toast, state } from '../lib/client.js';

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
    let disposed = false;
    let drawing = false;
    let pending = false;
    let offset = 0;
    let network = state.overview?.active_network || '';
    let generation = 0;
    const requests = new AbortController();
    const logRequests = new Set();

    const draw = async () => {
        if (disposed) return;
        if (drawing) { pending = true; return; }
        drawing = true;
        clearTimeout(timer);
        const current = generation;
        const query = `network_id=${encodeURIComponent(network)}`;
        const [listing, ray] = await Promise.all([
            api(`/api/ray/jobs?${query}&history_offset=${offset}`, { signal: requests.signal })
                .catch((error) => ({ jobs: [], detail: error.message })),
            api(`/api/ray?${query}`, { signal: requests.signal }).catch(() => ({})),
        ]);
        drawing = false;
        if (disposed) return;
        if (current !== generation) { pending = false; return draw(); }
        const jobs = listing.jobs || [];
        const queued = jobs.filter((job) => !job.archived && job.status === 'PENDING');
        const running = jobs.filter((job) => !job.archived && job.status === 'RUNNING');
        const done = jobs.filter((job) => job.archived || !['PENDING', 'RUNNING'].includes(job.status));
        for (const controller of logRequests) controller.abort();
        logRequests.clear();
        const loadLog = (job) => logPane(job, logRequests);

        mount(page,
            el('div', { class: 'jobs-head' },
                el('div', {},
                    el('h1', { class: 'page__title' }, 'Jobs'),
                    el('p', { class: 'jobs-head__sub' }, listing.network_name || 'Choose a network on Networks'),
                    el('p', { class: 'jobs-head__sub' },
                        running.length
                            ? `${running.length} running${queued.length ? `, ${queued.length} waiting` : ''}`
                            : queued.length ? `${queued.length} waiting to start`
                                : 'Nothing is running')),
                capacity(ray)),

            listing.detail ? el('p', { class: 'jobs-empty', role: 'status' }, listing.detail) : null,
            listing.history_error ? el('p', { class: 'jobs-warning', role: 'status' }, listing.history_error) : null,

            section('Waiting', queued, 'bx-time-five',
                'Nothing is queued.', expanded, draw, loadLog),
            section('Running', running, 'bx-loader-alt',
                'Nothing is running right now.', expanded, draw, loadLog),
            section('Recent', done, 'bx-history',
                'No saved jobs for this network yet.', expanded, draw, loadLog),
            offset || listing.history_more ? el('div', { class: 'job__actions' },
                el('button', { class: 'btn btn--sm', disabled: offset === 0,
                    onclick: () => { offset = Math.max(0, offset - 50); generation++; return draw(); } }, 'Newer history'),
                el('span', { class: 'muted' }, `History page ${Math.floor(offset / 50) + 1}`),
                el('button', { class: 'btn btn--sm', disabled: !listing.history_more,
                    onclick: () => { offset += 50; generation++; return draw(); } }, 'Older history')) : null);

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

        timer = setTimeout(draw, pending ? 0 : running.length || queued.length ? LIVE_POLL : IDLE_POLL);
        pending = false;
    };

    const resetNetwork = (data) => {
        network = data?.id ?? state.overview?.active_network ?? '';
        generation++;
        offset = 0;
        expanded.clear();
        for (const controller of logRequests) controller.abort();
        logRequests.clear();
        mount(page, el('p', { class: 'jobs-empty' }, 'Loading network jobs…'));
        draw();
    };
    const off = ['ray.changed', 'jobs.changed'].map((topic) => on(topic, draw));
    off.push(on('connection.restored', () => resetNetwork()));
    off.push(on('network.active', resetNetwork));
    off.push(on('networks.changed', () => resetNetwork()));
    await draw();
    return () => {
        disposed = true;
        for (const unsubscribe of off) unsubscribe();
        requests.abort();
        for (const controller of logRequests) controller.abort();
        clearTimeout(timer); clearInterval(ticker);
    };
}

// capacity is the one thing worth saying about the cluster on this page: how
// much of it there is. Everything else about a cluster belongs on Networks.
function capacity(ray) {
    if (!ray || !ray.running) {
        return el('div', { class: 'jobs-capacity jobs-capacity--off' },
            el('i', { class: 'bx bx-broadcast' }),
            el('span', {}, ray && ray.advice ? 'Ray is not running' : 'No cluster'));
    }
    const machines = Array.isArray(ray.nodes) ? ray.nodes.filter((node) => node.alive !== false).length : 0;
    return el('div', { class: 'jobs-capacity' },
        stat(machines, machines === 1 ? 'machine' : 'machines'),
        stat(ray.total_cpu || 0, 'CPU'),
        stat(ray.total_gpu || 0, 'GPU'));
}

function stat(value, label) {
    return el('span', { class: 'jobs-capacity__stat' },
        el('strong', {}, String(value)), el('span', {}, label));
}

function section(title, jobs, icon, emptyText, expanded, redraw, loadLog) {
    return el('section', { class: 'jobs-section' },
        el('div', { class: 'jobs-section__head' },
            el('i', { class: `bx ${icon}` }),
            el('span', { class: 'jobs-section__title' }, title),
            el('span', { class: 'jobs-section__count' }, String(jobs.length))),
        jobs.length
            ? el('div', { class: 'jobs-list' },
                ...jobs.map((job) => jobRow(job, expanded, redraw, loadLog)))
            : el('p', { class: 'jobs-empty' }, emptyText));
}

function jobRow(job, expanded, redraw, loadLog) {
    const status = (job.status || '').toUpperCase();
    const live = !job.archived && status === 'RUNNING';
    const waiting = !job.archived && status === 'PENDING';
    const key = `${job.network_id}/${job.id}/${job.started_at || 0}`;
    const open = expanded.has(key);

    const toggle = () => {
        if (open) expanded.delete(key); else expanded.add(key);
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
                class: 'job__time', dataset: live && job.started_at ? { since: String(job.started_at) } : {},
            }, timeFor(job)),
            el('span', { class: `job__state job__state--${status.toLowerCase()}` },
                job.archived && ['PENDING', 'RUNNING'].includes(status) ? `last known: ${status.toLowerCase()}`
                    : waiting ? 'waiting' : status.toLowerCase()),
            // Written out rather than built from a fragment: a class assembled
            // at runtime is invisible to the check that verifies every icon
            // this interface asks for actually exists.
            el('i', { class: `bx ${open ? 'bx-chevron-down' : 'bx-chevron-right'} job__chev` })),
        open ? loadLog(job) : null,
        job.message && !open
            ? el('p', { class: 'job__message' }, job.message)
            : null);

    if (open) {
        row.append(el('div', { class: 'job__actions' },
            el('button', { class: 'btn btn--sm', disabled: job.archived, onclick: () => showLogs(job) },
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
function logPane(job, requests) {
    const box = el('pre', { class: 'job__log' }, 'Loading output…');
    if (job.archived) {
        box.textContent = 'Saved summary. Output stays on the Ray head and is not stored on clusteradmin.';
        return box;
    }
    const controller = new AbortController();
    requests.add(controller);

    const load = async () => {
        try {
            const response = await api(`/api/ray/jobs/${encodeURIComponent(job.id)}/logs` +
                `?network_id=${encodeURIComponent(job.network_id || '')}`, { signal: controller.signal });
            if (controller.signal.aborted) return;
            const text = (response.logs || '').trimEnd();
            const lines = text ? text.split('\n') : [];
            mount(box, document.createTextNode(
                lines.length ? lines.slice(-14).join('\n') : 'No output yet.'));
            box.scrollTop = box.scrollHeight;
        } catch (error) {
            if (!controller.signal.aborted) mount(box, document.createTextNode(error.message));
        } finally {
            requests.delete(controller);
        }
    };
    load();
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
    if (job.archived && ['PENDING', 'RUNNING'].includes(status)) return 'unconfirmed';
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
