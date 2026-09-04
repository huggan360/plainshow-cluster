// Plainshow Cluster — the shell: navigation, routing, and the page frame.
//
// Everything shared with the views lives in lib/client.js, so the module graph
// runs one way: app -> views -> client.

import { el, mount, initials, plainshowLogo } from './lib/ui.js';
import { api, state, refresh, connect, onConnection, onUnauthorized, toast } from './lib/client.js';
import { renderHome } from './views/home.js';
import { renderJobs } from './views/jobs.js';
import { renderProjects } from './views/projects.js';
import { renderHowTo } from './views/howto.js';
import { renderSettings } from './views/settings.js';
import { renderGitHub } from './views/github.js';
import { renderGate } from './views/signin.js';
import { renderNetworks } from './views/networks.js';

const ROUTES = [
    { id: 'home', label: 'Home', icon: '⌂', render: renderHome },
    { id: 'networks', label: 'Networks', icon: '◇', render: renderNetworks },
    { id: 'projects', label: 'Projects', icon: '◫', render: renderProjects },
    { id: 'jobs', label: 'Jobs', icon: '▤', render: renderJobs },
    { id: 'github', label: 'GitHub', icon: '⑂', render: renderGitHub },
    { id: 'howto', label: 'How to', icon: '?', render: renderHowTo },
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
            el('p', { class: 'nav__label' }, 'Cluster'),
            ...ROUTES.map((r) => el('a', {
                class: 'nav__item', href: `#/${r.id}`, dataset: { route: r.id },
				onclick: closeRail,
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
	railScrim = el('button', {
		class: 'rail-scrim', 'aria-label': 'Close navigation', onclick: closeRail,
	});

    const networkPicker = el('select', {
        class: 'input input--mono',
        style: 'width:auto;min-width:150px;padding:7px 30px 7px 10px;font-size:10px',
        'aria-label': 'Active network',
        onchange: async (event) => {
            event.target.disabled = true;
			try {
				await api(`/api/networks/${encodeURIComponent(event.target.value)}/active`, { method: 'PUT' });
				location.reload();
			} catch (err) {
				event.target.disabled = false;
				toast(err.message, 'err');
			}
        },
    }, ...(overview.networks || []).map((network) => el('option', {
        value: network.id, selected: network.id === overview.active_network,
    }, network.name)));

    const main = el('div', { class: 'main' },
        el('header', { class: 'top' },
            el('button', {
				class: 'btn btn--icon btn--sm mobile-menu', id: 'menu',
                'aria-label': 'Toggle navigation',
				onclick: toggleRail,
            }, '☰'),
            el('span', { class: 'top__title', id: 'top-title' }, 'Home'),
            el('span', { class: 'top__spacer' }),
            networkPicker),
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
