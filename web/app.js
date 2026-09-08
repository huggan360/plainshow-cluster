// Plainshow Cluster — the shell: navigation, routing, and the page frame.
//
// Everything shared with the views lives in lib/client.js, so the module graph
// runs one way: app -> views -> client.

import { el, mount, initials, plainshowLogo } from './lib/ui.js';
import { api, state, refresh, connect, onConnection, onUnauthorized } from './lib/client.js';
import { powerButton } from './lib/statusbar.js';
import { renderHome } from './views/home.js';
import { renderJobs } from './views/jobs.js';
import { renderProjects } from './views/projects.js';
import { renderNewProject } from './views/newproject.js';
import { renderHowTo } from './views/howto.js';
import { renderSettings, renderDevicesPage } from './views/settings.js';
import { renderGitHub } from './views/github.js';
import { renderGate } from './views/signin.js';
import { renderNetworks } from './views/networks.js';

const ROUTES = [
    { id: 'home', label: 'Home', icon: 'bx-home-alt-2', render: renderHome },
    { id: 'networks', label: 'Networks', icon: 'bx-network-chart', render: renderNetworks },
    { id: 'projects', label: 'Projects', icon: 'bx-layer', render: renderProjects },
    { id: 'jobs', label: 'Jobs', icon: 'bx-task', render: renderJobs },
    { id: 'github', label: 'GitHub', icon: 'bxl-github', render: renderGitHub },
    { id: 'devices', label: 'Devices', icon: 'bx-devices', render: renderDevicesPage },
    { id: 'howto', label: 'How to', icon: 'bx-help-circle', render: renderHowTo },
    { id: 'settings', label: 'Settings', icon: 'bx-cog', render: renderSettings },
    // Reachable, but not a place in the sidebar: it is a step in making a
    // project, not somewhere you go.
    { id: 'new', label: 'New project', icon: 'bx-layer-plus', render: renderNewProject, hidden: true },
];

/** parseRoute reads the hash as a route id plus its arguments. */
function parseRoute() {
    const raw = location.hash.replace(/^#\/?/, '');
    const [id, ...args] = raw.split('/').filter(Boolean);
    const route = ROUTES.find((r) => r.id === id) || ROUTES[0];
    return { route, args: args.map(decodeURIComponent) };
}

let disposeView = null;
let routeGeneration = 0;

async function renderRoute() {
    const generation = ++routeGeneration;
    const { route, args } = parseRoute();
    // Views register event listeners; disposing on navigation is what keeps a
    // long-lived page from leaking one handler per visit.
    if (disposeView) { disposeView(); disposeView = null; }

    document.querySelectorAll('.nav__item').forEach((node) => {
        const on = node.dataset.route === route.id;
        node.classList.toggle('nav__item--on', on);
        node.querySelector('.nav__dot').classList.toggle('hide', !on);
    });
    const title = document.getElementById('top-title');
    if (title) title.textContent = route.label;

    const host = el('div');
    mount(document.getElementById('view'), host);
    mount(host, el('div', { class: 'page' },
        el('div', { class: 'empty' }, el('span', { class: 'spin' }))));

    try {
        const dispose = await route.render(host, args) || null;
        if (generation !== routeGeneration) { dispose?.(); return; }
        disposeView = dispose;
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
	const account = overview.account || {};
	let railScrim;
	const closeRail = () => {
		rail.classList.remove('rail--open');
		if (railScrim) railScrim.classList.remove('rail-scrim--open');
	};
	const toggleRail = () => {
		const open = rail.classList.toggle('rail--open');
		railScrim.classList.toggle('rail-scrim--open', open);
	};

    const rail = el('aside', { class: 'rail', id: 'rail' },
        el('div', { class: 'brand' },
			plainshowLogo('cluster')),
        el('nav', { class: 'nav' },
            el('p', { class: 'nav__label' }, 'Workspace'),
            ...ROUTES.filter((r) => !r.hidden).map((r) => el('a', {
                class: 'nav__item', href: `#/${r.id}`, dataset: { route: r.id },
				onclick: closeRail,
            },
                el('i', { class: `bx ${r.icon} nav__ico`, 'aria-hidden': 'true' }),
                el('span', {}, r.label),
                el('span', { class: 'nav__dot hide' })))),
        el('div', { class: 'nodecard' },
            el('div', { class: 'nodecard__row' },
                el('span', { class: 'nodecard__avatar' }, initials(node.name)),
                el('span', { style: 'min-width:0;flex:1' },
                    el('span', { class: 'nodecard__name' }, account.display_name || node.name),
                    el('span', { class: 'nodecard__meta' }, account.username
						? `@${account.username}` : node.name))),
            el('div', { class: 'nodecard__foot' },
                // The version string may already start with a v, and vv0.1.2 looks broken.
                el('span', {}, /^v/i.test(overview.version) ? overview.version : `v${overview.version}`),
                el('span', { style: 'display:flex;align-items:center;gap:6px' },
                    el('span', { class: 'dot dot--off', id: 'conn-dot' }),
                    el('span', {
                        class: 'mono', id: 'conn-label', style: 'font-size:10px',
                    }, 'offline')),
				el('button', {
					class: 'tree__action', title: 'Sign out', 'aria-label': 'Sign out',
					onclick: async () => {
						await api('/api/auth/logout', { method: 'POST', body: {} });
						location.reload();
					},
				}, el('i', { class: 'bx bx-log-out' })))));
	railScrim = el('button', {
		class: 'rail-scrim', 'aria-label': 'Close navigation', onclick: closeRail,
	});

    const main = el('div', { class: 'main' },
        el('header', { class: 'top' },
            el('button', {
				class: 'btn btn--icon btn--sm mobile-menu', id: 'menu',
                'aria-label': 'Toggle navigation',
				onclick: toggleRail,
            }, el('i', { class: 'bx bx-menu' })),
			el('span', { class: 'top__title', id: 'top-title' }, 'Home'),
            activityStrip(),
            el('span', { class: 'top__spacer' }),
            powerButton(),
			el('a', { class: 'btn btn--sm', href: '#/new', title: 'New project' },
				el('i', { class: 'bx bx-plus' }), el('span', {}, 'New project'))),
        el('div', { id: 'view' }));

    return [railScrim, rail, main];
}

async function boot() {
    const app = document.getElementById('app');

    // A node with no owner account is unclaimed: offer to claim it rather than
    // dropping straight into a workspace anyone on the network could use.
    const auth = await api('/api/auth/status').catch(() => null);
    if (auth && (!auth.enabled || !auth.authenticated)) {
        await renderGate(app, auth);
    }

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
    onConnection((live) => {
        const dot = document.getElementById('conn-dot');
        const label = document.getElementById('conn-label');
        if (!dot || !label) return;
        dot.className = `dot ${live ? 'dot--on' : 'dot--bad'}`;
        label.textContent = live ? 'live' : 'offline';
    });

    // A session can expire while the page is open. Show the gate again rather
    // than leaving a workspace whose every request quietly fails.
    onUnauthorized(() => location.reload());

    connect();
    window.addEventListener('hashchange', renderRoute);
    if (!location.hash) location.hash = '#/home';
    await renderRoute();
}

boot();
