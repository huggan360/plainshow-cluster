// Plainshow Cluster — the shell: navigation, routing, and the page frame.
//
// Everything shared with the views lives in lib/client.js, so the module graph
// runs one way: app -> views -> client.

import { el, mount, initials, plainshowLogo } from './lib/ui.js';
import { api, state, refresh, connect, on, onConnection, onUnauthorized } from './lib/client.js';
import { powerButton } from './lib/statusbar.js';
import { activityStrip } from './lib/activity.js';
import { availabilitySwitch } from './lib/availability.js';
import { renderHome } from './views/home.js';
import { renderJobs } from './views/jobs.js';
import { renderProjects } from './views/projects.js';
import { renderNewProject } from './views/newproject.js';
import { renderProfile } from './views/profile.js';
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
    { id: 'profile', label: 'Account', icon: 'bx-user', render: renderProfile, hidden: true },
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
	const connectionHost = el('div', { class: 'rail-connection-slot' });
	const drawConnection = () => mount(connectionHost,
		connectionCard(state.overview || overview, closeRail));
	drawConnection();
	on('network.active', drawConnection);
	on('networks.changed', drawConnection);

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
        connectionHost,
        el('div', { class: 'nodecard' },
            // The whole row opens the account, because a name and a face is
            // where people look for their own settings.
            el('a', { class: 'nodecard__row', href: '#/profile', title: 'Your account' },
                el('span', { class: 'nodecard__avatar' }, initials(account.display_name || node.name)),
                el('span', { style: 'min-width:0;flex:1' },
                    el('span', { class: 'nodecard__name' }, account.display_name || node.name),
                    el('span', { class: 'nodecard__meta' }, account.username
						? `@${account.username}` : node.name)),
                el('i', { class: 'bx bx-chevron-right nodecard__go' })),
            el('div', { class: 'nodecard__foot' },
                // The version string may already start with a v, and vv0.1.2 looks broken.
                el('span', {}, /^v/i.test(overview.version) ? overview.version : `v${overview.version}`),
                // The dot is the connection to this node; the switch beside it is
                // whether other people's work may run here. Different questions,
                // and only one of them is worth a word.
                el('span', { class: 'dot dot--off', id: 'conn-dot', title: 'Connecting…' }),
                availabilitySwitch(),
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

/** connectionCard summarises this machine's current compute network. */
function connectionCard(overview, closeRail) {
	const networks = overview.networks || [];
	const active = networks.find((network) => network.id === overview.active_network);
	const connected = Boolean(active && active.enabled !== false);
	const self = (overview.machines || []).find((machine) =>
		machine.is_self || machine.node_id === overview.node?.id);
	const address = connectionAddress(self?.address);
	const href = active ? `#/networks/${encodeURIComponent(active.id)}` : '#/networks';

	return el('a', {
		class: `rail-connection ${connected ? 'rail-connection--on' : ''}`,
		href, onclick: closeRail,
	},
		el('span', { class: 'rail-connection__top' },
			el('span', { class: `dot ${connected ? 'dot--on' : 'dot--off'}` }),
			el('span', {}, connected ? 'Connected' : 'Not connected')),
		el('strong', { class: 'rail-connection__name' },
			connected ? active.name : 'No active network'),
		el('span', { class: 'rail-connection__meta' },
			connected ? (address || 'IP address unavailable') : 'Choose a network for project runs'),
		el('span', { class: 'rail-connection__foot' },
			connected ? `${active.node_count || 0} devices · ${active.gpu_count || 0} GPUs` : 'Open networks',
			el('i', { class: 'bx bx-right-arrow-alt', 'aria-hidden': 'true' })));
}

function connectionAddress(value) {
	const raw = String(value || '').trim();
	if (!raw) return '';
	try {
		return new URL(raw.includes('://') ? raw : `https://${raw}`).hostname;
	} catch {
		return raw.replace(/^https?:\/\//, '').split('/')[0].replace(/:\d+$/, '');
	}
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
        if (!dot) return;
        dot.className = `dot ${live ? 'dot--on' : 'dot--bad'}`;
        dot.title = live ? 'Connected to this node' : 'Not reaching this node';
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
