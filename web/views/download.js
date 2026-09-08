// A project that is here as a row, but not yet as files.
//
// Until the working tree arrives there is nothing to edit, run or commit, so
// showing the usual tabs would be showing seven doors onto an empty room. One
// thing is offered instead, with the size said up front — agreeing to a
// download without knowing whether it is four megabytes or forty is not really
// agreeing.

import { el, mount } from '../lib/ui.js';
import { api, on, toast } from '../lib/client.js';

/** downloadPane is the whole page for a project whose files are absent. */
export function downloadPane(project) {
    const box = el('div', {});
    let started = false;

    const size = project.size_kb
        ? project.size_kb >= 1024
            ? `${(project.size_kb / 1024).toFixed(1)} MB`
            : `${project.size_kb} KB`
        : 'unknown size';

    const bar = el('span', { class: 'dl-bar__fill', style: 'width:0%' });
    const phase = el('span', { class: 'dl-phase' }, 'Waiting to start');
    const meter = el('div', { class: 'dl-bar hide' }, bar);

    const button = el('button', { class: 'dl-action', onclick: () => start() },
        el('span', { class: 'dl-action__in' },
            el('i', { class: 'bx bx-download' }),
            el('span', {}, 'Download project')));

    const start = async () => {
        if (started) return;
        started = true;
        button.disabled = true;
        meter.classList.remove('hide');
        phase.textContent = 'Starting';
        try {
            await api(`/api/projects/${encodeURIComponent(project.id || project.name)}/download`,
                { method: 'POST', body: {} });
        } catch (err) {
            toast(err.message, 'err');
            phase.textContent = err.message;
            button.disabled = false;
            started = false;
        }
    };

    const draw = (item) => {
        if (!item || item.project !== project.name) return;
        meter.classList.remove('hide');
        if (item.error) {
            phase.textContent = item.error;
            bar.style.width = '0%';
            button.disabled = false;
            started = false;
            return;
        }
        bar.style.width = `${Math.max(0, Math.min(100, item.percent || 0))}%`;
        phase.textContent = item.done
            ? 'Ready — opening the project…'
            : `${item.phase || 'Working'} ${item.percent > 0 ? `${item.percent}%` : ''}`.trim();
        // The page it is about to become is a different page entirely, so it is
        // rebuilt rather than patched.
        if (item.done) setTimeout(() => location.reload(), 600);
    };

    mount(box, el('div', { class: 'dl' },
        el('span', { class: 'dl__mark' }, el('i', { class: 'bx bx-layer' })),
        el('h1', { class: 'dl__title' }, project.name),
        el('p', { class: 'dl__sub' },
            project.repository
                ? el('span', {}, el('i', { class: 'bx bxl-github' }), ` ${project.repository}`)
                : 'This project has no repository yet.'),
        el('div', { class: 'dl__facts' },
            fact('Size', size),
            fact('Branch', project.branch || 'main'),
            fact('Files', 'not on this machine')),
        project.repository
            ? el('div', {}, button, meter, phase)
            : el('p', { class: 'dl__none' },
                'Its files can only come from a machine that already has them. ' +
                'Open the project there and send them to this machine.')));

    // A transfer somebody started elsewhere in the app, or before this page was
    // opened, still belongs on it.
    api('/api/downloads').then((data) => {
        const mine = (data.downloads || []).find((item) => item.project === project.name);
        if (mine) { started = true; button.disabled = true; draw(mine); }
    }).catch(() => {});

    const off = on('project.download', draw);
    return { node: box, dispose: off };
}

function fact(label, value) {
    return el('span', { class: 'dl-fact' },
        el('span', { class: 'dl-fact__label' }, label),
        el('strong', {}, value));
}
