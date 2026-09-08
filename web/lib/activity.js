// What the cluster is doing right now, in the top bar.
//
// Work happens on machines you are not looking at, started by people who are
// not you. Without somewhere to say so, the only evidence is a page that
// changes when you happen to reload it. This is that somewhere: one line,
// living where it can be seen from every page, showing transfers and running
// jobs and then getting out of the way.

import { el, mount } from './ui.js';
import { api, on } from './client.js';

// noticeLife is how long a passing message stays. Long enough to read, short
// enough that it is gone before it becomes furniture.
const noticeLife = 6000;

/** activityStrip builds the live status line for the top bar. */
export function activityStrip() {
    const strip = el('div', { class: 'activity hide' });
    const downloads = new Map();
    const notices = [];
    let jobs = 0;

    const draw = () => {
        const items = [];
        for (const item of downloads.values()) {
            items.push(el('span', { class: 'activity__item' },
                el('i', { class: 'bx bx-download' }),
                el('span', { class: 'activity__name' }, item.project),
                el('span', { class: 'activity__bar' },
                    el('span', {
                        class: 'activity__fill',
                        style: `width:${Math.max(0, Math.min(100, item.percent || 0))}%`,
                    })),
                el('span', { class: 'activity__pct' },
                    item.percent > 0 ? `${item.percent}%` : '')));
        }
        if (jobs > 0) {
            items.push(el('span', { class: 'activity__item' },
                el('i', { class: 'bx bx-play' }),
                el('span', { class: 'activity__name' },
                    `${jobs} job${jobs === 1 ? '' : 's'} running`)));
        }
        for (const notice of notices) {
            items.push(el('span', { class: `activity__item activity__item--${notice.tone}` },
                el('i', { class: `bx ${notice.icon}` }),
                el('span', { class: 'activity__name' }, notice.text)));
        }
        strip.classList.toggle('hide', items.length === 0);
        mount(strip, ...items);
    };

    const notice = (text, icon = 'bx-info-circle', tone = 'plain') => {
        const item = { text, icon, tone };
        notices.push(item);
        draw();
        setTimeout(() => {
            const at = notices.indexOf(item);
            if (at >= 0) notices.splice(at, 1);
            draw();
        }, noticeLife);
    };

    on('project.download', (item) => {
        if (!item || !item.project) return;
        if (item.done || item.error) {
            downloads.delete(item.project);
            notice(item.error
                ? `${item.project}: ${item.error}`
                : `${item.project} is ready`,
            item.error ? 'bx-error-circle' : 'bx-check-circle',
            item.error ? 'bad' : 'good');
        } else {
            downloads.set(item.project, item);
        }
        draw();
    });

    // Job counts come from the overview rather than being tallied here: a
    // browser that was closed while three jobs started would otherwise show
    // none of them.
    const countJobs = async () => {
        try {
            const data = await api('/api/overview');
            jobs = (data.active_jobs || []).length;
        } catch { /* an unreachable node is already reported elsewhere */ }
        draw();
    };
    on('job.started', countJobs);
    on('job.finished', countJobs);
    on('jobs.changed', countJobs);
    countJobs();

    on('invitations.changed', () => notice('You have a new invitation', 'bx-envelope'));
    on('devices.changed', () => notice('Your devices changed', 'bx-devices'));
    on('service.stopping', () => notice('Stopping Plainshow on this machine', 'bx-power-off', 'bad'));

    // A transfer running before this page opened still belongs on it.
    api('/api/downloads').then((data) => {
        for (const item of data.downloads || []) {
            if (!item.done && !item.error) downloads.set(item.project, item);
        }
        draw();
    }).catch(() => {});

    return strip;
}
