// Plainshow Cluster — the shell: navigation, routing, and the page frame.
//
// Everything shared with the views lives in lib/client.js, so the module graph
// runs one way: app -> views -> client.

import { el, mount, initials } from './lib/ui.js';
import { state, refresh, connect, onConnection } from './lib/client.js';
import { renderHome } from './views/home.js';
import { renderWorkspace } from './views/workspace.js';
import { renderJobs } from './views/jobs.js';
import { renderMachines } from './views/machines.js';
import { renderSettings } from './views/settings.js';

const ROUTES = [
    { id: 'home', label: 'Home', icon: '⌂', render: renderHome },
    { id: 'workspace', label: 'Workspace', icon: '◫', render: renderWorkspace },
    { id: 'jobs', label: 'Jobs', icon: '▤', render: renderJobs },
    { id: 'machines', label: 'Machines', icon: '▣', render: renderMachines },
    { id: 'settings', label: 'Settings', icon: '⚙', render: renderSettings },
];

/** parseRoute reads the hash as a route id plus its arguments. */
function parseRoute() {
    const raw = location.hash.replace(/^#\/?/, '');
    const [id, ...args] = raw.split('/').filter(Boolean);
    const route = ROUTES.find((r) => r.id === id) || ROUTES[0];
    return { route, args: args.map(decodeURIComponent) };
}

let disposeView = null;

async function renderRoute() {
    const { route, args } = parseRoute();
    // Views register event listeners; disposing on navigation is what keeps a
    // long-lived page from leaking one handler per visit.
    if (disposeView) { disposeView(); disposeView = null; }

    document.querySelectorAll('.nav__item').forEach((node) => {
        const on = node.dataset.route === route.id;
        node.classList.toggle('nav__item--on', on);
        node.querySelector('.nav__dot').classList.toggle('hide', !on);
    });
    document.getElementById('top-title').textContent = route.label;

    const host = document.getElementById('view');
    mount(host, el('div', { class: 'page' },
        el('div', { class: 'empty' }, el('span', { class: 'spin' }))));

    try {
        disposeView = await route.render(host, args) || null;
    } catch (err) {
        mount(host, el('div', { class: 'page' },
            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Could not load this page'),
                el('p', { class: 'muted' }, err.message),
                el('button', { class: 'btn', onclick: renderRoute }, 'Try again'))));
    }
}

/** shell builds the sidebar and page frame around the routed view. */
function shell(overview) {
    const node = overview.node;

    const rail = el('aside', { class: 'rail', id: 'rail' },
        el('div', { class: 'brand' },
            el('span', { class: 'brand__mark' }),
            el('span', {},
                el('span', { class: 'brand__word' },
                    el('b', {}, 'plain'), el('span', {}, 'show')),
                el('span', { class: 'brand__sub' }, 'cluster'))),
        el('nav', { class: 'nav' },
            el('p', { class: 'nav__label' }, 'Cluster'),
            ...ROUTES.map((r) => el('a', {
                class: 'nav__item', href: `#/${r.id}`, dataset: { route: r.id },
            },
                el('span', { class: 'nav__ico' }, r.icon),
                el('span', {}, r.label),
                el('span', { class: 'nav__dot hide' })))),
        el('div', { class: 'nodecard' },
            el('div', { class: 'nodecard__row' },
                el('span', { class: 'nodecard__avatar' }, initials(node.name)),
                el('span', { style: 'min-width:0;flex:1' },
                    el('span', { class: 'nodecard__name' }, node.name),
                    el('span', { class: 'nodecard__meta' }, node.roles.join(' · ')))),
            el('div', { class: 'nodecard__foot' },
                el('span', {}, `v${overview.version}`),
                el('span', { style: 'display:flex;align-items:center;gap:6px' },
                    el('span', { class: 'dot dot--off', id: 'conn-dot' }),
                    el('span', {
                        class: 'mono', id: 'conn-label', style: 'font-size:10px',
                    }, 'offline')))));

    const main = el('div', { class: 'main' },
        el('header', { class: 'top' },
            el('button', {
                class: 'btn btn--icon btn--sm', id: 'menu', style: 'display:none',
                'aria-label': 'Toggle navigation',
                onclick: () => rail.classList.toggle('rail--open'),
            }, '☰'),
            el('span', { class: 'top__title', id: 'top-title' }, 'Home'),
            el('span', { class: 'top__spacer' }),
            el('span', { class: 'chip' }, overview.cluster.name)),
        el('div', { id: 'view' }));

    return [rail, main];
}

async function boot() {
    const app = document.getElementById('app');
    try {
        await refresh();
    } catch (err) {
        mount(app, el('div', {
            class: 'page', style: 'margin:auto;max-width:520px;padding-top:80px',
        },
            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Cannot reach this node'),
                el('p', { class: 'muted' }, err.message),
                el('button', {
                    class: 'btn btn--primary', onclick: () => location.reload(),
                }, 'Retry'))));
        return;
    }

    mount(app, ...shell(state.overview));
    if (window.matchMedia('(max-width: 1000px)').matches) {
        document.getElementById('menu').style.display = 'inline-flex';
    }

    onConnection((live) => {
        const dot = document.getElementById('conn-dot');
        const label = document.getElementById('conn-label');
        if (!dot || !label) return;
        dot.className = `dot ${live ? 'dot--on' : 'dot--bad'}`;
        label.textContent = live ? 'live' : 'offline';
    });

    connect();
    window.addEventListener('hashchange', renderRoute);
    if (!location.hash) location.hash = '#/home';
    await renderRoute();
}

boot();
