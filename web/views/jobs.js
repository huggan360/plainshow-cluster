// Jobs — everything that has run, and the live output of anything running now.

import { el, mount, ago, duration, stateChip, stateDot } from '../lib/ui.js';
import { api, on, toast, navigate } from '../lib/client.js';

export async function renderJobs(host, args) {
    if (args.length > 0) return renderJobDetail(host, args[0]);
    return renderJobList(host);
}

async function renderJobList(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const draw = async () => {
        const jobs = await api('/api/jobs?limit=120');
        mount(page,
            el('div', { class: 'page__head' },
                el('p', { class: 'page__eyebrow' }, 'Jobs'),
                el('h1', { class: 'page__title' }, 'Jobs'),
                el('p', { class: 'page__sub' },
                    'Everything this node has run. A job is a script, a command, ' +
                    'and later a notebook kernel or a training run — one lifecycle for all of them.')),
            jobs.length ? table(jobs) : el('div', { class: 'panel' },
                el('div', { class: 'empty' },
                    el('span', { class: 'empty__ico' }, '▤'),
                    el('span', { class: 'empty__text' },
                        'Nothing has run yet. Open a project and press Run.'),
                    el('button', {
                        class: 'btn btn--primary btn--sm',
                        onclick: () => navigate('workspace'),
                    }, 'Open Workspace'))));
    };

    await draw();
    const off = on('job.state', draw);
    const offNew = on('job.created', draw);
    return () => { off(); offNew(); };
}

function table(jobs) {
    return el('div', { class: 'tblwrap' },
        el('table', {},
            el('thead', {}, el('tr', {},
                el('th', {}, 'Job'), el('th', {}, 'Project'),
                el('th', {}, 'Machine'), el('th', {}, 'State'),
                el('th', {}, 'Took'), el('th', {}, 'When'))),
            el('tbody', {}, ...jobs.map((j) =>
                el('tr', {
                    style: 'cursor:pointer',
                    onclick: () => navigate(`jobs/${j.id}`),
                },
                    el('td', {},
                        el('span', { style: 'display:flex;align-items:center;gap:8px' },
                            stateDot(j.state),
                            el('span', {}, j.title || j.command))),
                    el('td', { class: 'muted' }, j.project || '—'),
                    el('td', { class: 'mono muted', style: 'font-size:11.5px' }, j.machine || '—'),
                    el('td', {}, stateChip(j.state)),
                    el('td', { class: 'num muted' },
                        j.started_at ? duration(j.started_at, j.ended_at) : '—'),
                    el('td', { class: 'muted' }, ago(j.created_at)))))));
}

async function renderJobDetail(host, id) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    let job = await api(`/api/jobs/${id}`);
    const lines = await api(`/api/jobs/${id}/logs`);

    const consoleBox = el('div', { class: 'console' });
    const header = el('div', { class: 'page__head' });
    const actions = el('div', { style: 'display:flex;gap:8px;margin-bottom:14px' });

    const paintHeader = () => {
        mount(header,
            el('p', { class: 'page__eyebrow' }, `Job ${job.id.slice(0, 8)}`),
            el('h1', { class: 'page__title' }, job.title || job.command),
            el('p', { class: 'page__sub' },
                `${job.project || 'no project'} · ${job.machine} · ${ago(job.created_at)}`));

        const terminal = ['succeeded', 'failed', 'stopped'].includes(job.state);
        mount(actions,
            stateChip(job.state),
            job.exit_code >= 0 ? el('span', { class: 'chip' }, `exit ${job.exit_code}`) : null,
            job.started_at ? el('span', { class: 'chip' },
                duration(job.started_at, job.ended_at)) : null,
            el('span', { style: 'flex:1' }),
            !terminal ? el('button', {
                class: 'btn btn--danger btn--sm',
                onclick: async () => {
                    try {
                        await api(`/api/jobs/${job.id}/stop`, { method: 'POST' });
                        toast('Stopping the job.');
                    } catch (err) { toast(err.message, 'err'); }
                },
            }, 'Stop') : null,
            el('button', { class: 'btn btn--sm', onclick: () => navigate('jobs') }, 'All jobs'));
    };

    const append = (line) => {
        const atBottom = consoleBox.scrollHeight - consoleBox.scrollTop
            - consoleBox.clientHeight < 40;
        consoleBox.append(el('div', {
            class: `console__line ${line.stream === 'stderr' ? 'console__line--err' : ''}`,
        }, line.text));
        // Only follow the tail when the reader is already at the bottom, so
        // scrolling back through output is not yanked away by new lines.
        if (atBottom) consoleBox.scrollTop = consoleBox.scrollHeight;
    };

    paintHeader();
    if (lines.length === 0) {
        consoleBox.append(el('div', { class: 'console__line console__line--meta' },
            job.state === 'running' ? 'Waiting for output…' : 'This job produced no output.'));
    } else {
        lines.forEach(append);
    }
    if (job.error) {
        consoleBox.append(el('div', { class: 'console__line console__line--err' }, job.error));
    }

    mount(page, header, actions,
        el('div', { class: 'panel', style: 'margin-bottom:14px' },
            el('div', { class: 'panel__head' }, 'Command'),
            el('pre', {
                class: 'mono',
                style: 'margin:0;font-size:12px;color:#cbd5e1;white-space:pre-wrap;overflow-wrap:anywhere',
            }, job.command)),
        el('div', { class: 'panel' },
            el('div', { class: 'panel__head' }, 'Output'), consoleBox));

    consoleBox.scrollTop = consoleBox.scrollHeight;

    const offLog = on('job.log', (line) => { if (line.job_id === id) append(line); });
    const offState = on('job.state', (next) => {
        if (next.id !== id) return;
        job = next;
        paintHeader();
        if (next.error) {
            consoleBox.append(el('div', { class: 'console__line console__line--err' }, next.error));
        }
        consoleBox.append(el('div', { class: 'console__line console__line--meta' },
            `— ${next.state}${next.exit_code >= 0 ? ` (exit ${next.exit_code})` : ''} —`));
        consoleBox.scrollTop = consoleBox.scrollHeight;
    });
    return () => { offLog(); offState(); };
}
