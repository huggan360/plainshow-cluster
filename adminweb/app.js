const app = document.querySelector('#app');

async function api(path, options = {}) {
    const init = { headers: {}, ...options };
    if (init.body && typeof init.body !== 'string') {
        init.headers['Content-Type'] = 'application/json';
        init.body = JSON.stringify(init.body);
    }
    const response = await fetch(path, init);
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || `Request failed (${response.status}).`);
    return body;
}

function node(name, attrs = {}, ...children) {
    const value = document.createElement(name);
    for (const [key, item] of Object.entries(attrs)) {
		if (key.startsWith('on') && typeof item === 'function') value.addEventListener(key.slice(2), item);
        else value.setAttribute(key, item);
    }
    value.append(...children.filter((item) => item !== null));
    return value;
}

function brandLogo(subtitle = '') {
    return node('span', { class: 'brand-logo', 'aria-label': `PlainShow ${subtitle}` },
        node('img', { class: 'brand-logo__icon', src: '/brand/plainshow-icon.webp', alt: '', 'aria-hidden': 'true' }),
        node('span', { class: 'brand-logo__copy', 'aria-hidden': 'true' },
            node('span', { class: 'brand-logo__word' },
                node('span', {}, 'plain'), node('span', { class: 'brand-logo__show' }, 'show')),
            node('span', { class: 'brand-logo__sub' }, subtitle)));
}

function initials(value) {
    return String(value || '?').split(/[\s._-]+/).filter(Boolean)
        .map((part) => part[0]).join('').slice(0, 2).toUpperCase();
}

function notify(message, kind = 'ok') {
    const item = node('div', { class: `toast toast--${kind}`, role: kind === 'error' ? 'alert' : 'status' }, message);
    document.body.append(item);
    setTimeout(() => item.remove(), kind === 'error' ? 6500 : 2800);
}

function header(account) {
    return node('header', { class: 'top' },
		brandLogo('cluster admin'),
        node('span', { class: 'grow' }),
		node('div', { class: 'account' }, node('span', { class: 'avatar' }, initials(account.display_name)),
			node('span', {}, `@${account.username}`),
			node('button', { class: 'btn', onclick: async () => {
				await api('/api/auth/logout', { method: 'POST', body: {} });
				await start();
			} }, 'Sign out')));
}

function field(label, type = 'text', placeholder = '') {
    const input = node('input', { type, placeholder });
    return { input, view: node('div', { class: 'field' }, node('label', {}, label), input) };
}

function login(status, registration = false) {
    const username = field('Username', 'text', 'your-name');
    const displayName = field('Display name', 'text', 'Your name');
    const password = field('Password', 'password', 'At least 10 characters');
    const bootstrap = field('First-account bootstrap token', 'password', 'Only needed for the first account');
    const error = node('p', { class: 'error' });
    const submit = node('button', { class: 'btn primary' }, registration ? 'Create account' : 'Sign in');
	const send = async (event) => {
		if (event) event.preventDefault();
        submit.disabled = true;
		submit.textContent = registration ? 'Creating account…' : 'Signing in…';
        error.textContent = '';
        try {
            const body = { username: username.input.value, password: password.input.value };
            if (registration) {
                body.display_name = displayName.input.value;
                body.bootstrap_token = bootstrap.input.value;
            }
            await api(registration ? '/api/auth/register' : '/api/auth/login', { method: 'POST', body });
            await start();
        } catch (cause) {
            error.textContent = cause.message;
            submit.disabled = false;
			submit.textContent = registration ? 'Create account' : 'Sign in';
        }
	};
    const switcher = node('button', { class: 'link', onclick: () => login(status, !registration) },
        registration ? 'I already have an account' : 'Create an account');
	const form = node('form', { onsubmit: send },
		username.view,
		registration ? displayName.view : null,
		password.view,
		registration ? bootstrap.view : null,
		error, submit);
    app.replaceChildren(node('main', { class: 'login-wrap' }, node('section', { class: 'panel login' },
		node('div', { class: 'top top--login' }, brandLogo('cluster')),
		node('p', { class: 'eyebrow' }, 'Global cluster identity'),
        node('h1', {}, registration ? 'Create account' : 'Welcome back'),
        node('p', { class: 'muted' }, registration
            ? 'One identity works across every Plainshow network and device.'
            : 'Sign in to manage the Plainshow cluster environment.'),
		form, node('p', { class: 'muted login-switch' }, switcher))));
    username.input.focus();
}

function stat(value, label) {
    return node('div', { class: 'metric' }, node('div', { class: 'metric__surface' },
		node('strong', {}, String(value)), node('span', {}, label)));
}

function accountRow(account, current, reload) {
    const action = account.id === current.id ? node('span', { class: 'chip good' }, 'you') :
        node('button', { class: `btn ${account.disabled ? '' : 'danger'}`, onclick: async () => {
            await api(`/api/accounts/${encodeURIComponent(account.id)}`, {
                method: 'PATCH', body: { disabled: !account.disabled },
            });
            await reload();
        } }, account.disabled ? 'Enable' : 'Disable');
	return node('div', { class: 'row' }, node('span', { class: 'row__icon' }, initials(account.display_name)), node('div', { class: 'main' },
        node('div', { class: 'title' }, account.display_name, account.admin ? ' · admin' : ''),
        node('div', { class: 'meta' }, `@${account.username} · ${account.disabled ? 'disabled' : 'active'}`)), action);
}

function nodeRow(item) {
    const seen = new Date(item.last_seen).toLocaleString();
	return node('div', { class: 'row' }, node('span', { class: 'row__icon' }, initials(item.name)), node('div', { class: 'main' },
        node('div', { class: 'title' }, item.name),
        node('div', { class: 'meta' }, `${item.os}/${item.arch} · ${item.gpu_count} GPU · seen ${seen}`)),
        node('span', { class: 'chip' }, item.version || 'dev'));
}

function controllerRow(item) {
    return node('div', { class: 'row' },
        node('span', { class: `row__icon ${item.online ? 'row__icon--online' : ''}` }, 'WS'),
        node('div', { class: 'main' },
            node('div', { class: 'title' }, item.name),
            node('div', { class: 'meta' }, `${item.public_url} · @${item.owner_username} · ${item.networks} networks`)),
        node('span', { class: `chip ${item.online ? 'good' : ''}` }, item.online ? 'online' : 'offline'));
}

function networkRow(item, reload) {
    const key = node('code', { class: 'meta' }, item.management_key);
    const copy = node('button', { class: 'btn', onclick: async () => {
        await navigator.clipboard.writeText(item.management_key);
		notify('Recovery key copied.');
    } }, 'Copy key');
    const rotate = node('button', { class: 'btn danger', onclick: async () => {
        if (!confirm(`Rotate the management key for ${item.name}? Existing devices must receive the new key.`)) return;
        const result = await api(`/api/networks/${encodeURIComponent(item.id)}/rotate-key`, { method: 'POST', body: {} });
        await navigator.clipboard.writeText(result.management_key);
		notify('New key copied. Update every device before its next registry sync.');
        await reload();
    } }, 'Rotate');
	return node('div', { class: 'row' }, node('span', { class: 'row__icon' }, 'NW'), node('div', { class: 'main' },
        node('div', { class: 'title' }, item.name),
        node('div', { class: 'meta' }, `${item.members} accounts · ${item.nodes} devices · ${item.id}`),
		node('details', {}, node('summary', {}, 'Show recovery key'), key)),
		node('div', { class: 'row__actions' }, copy, rotate));
}

function panel(title, items, empty, wide = false) {
    return node('section', { class: `panel ${wide ? 'panel--wide' : ''}` },
        node('div', { class: 'panel__head' }, node('h2', {}, title),
            node('span', { class: 'panel__count' }, String(items.length))),
        items.length ? node('div', { class: 'rows' }, ...items) : node('div', { class: 'empty' }, empty));
}

async function dashboard(status) {
    const draw = async () => {
        const [stats, accounts, nodes, networks, controllers] = await Promise.all([
            api('/api/stats'), api('/api/accounts'), api('/api/nodes'), api('/api/networks'), api('/api/controllers'),
        ]);
		app.replaceChildren(node('div', { class: 'shell' }, header(status.account),
			node('section', { class: 'hero' }, node('p', { class: 'eyebrow' }, 'Global environment'),
				node('h1', {}, 'Cluster at a glance'),
				node('p', { class: 'muted' }, 'Accounts, network recovery keys, controller registrations and aggregate health. Project data, compute traffic and Cowork WebSockets stay outside this service.')),
			node('section', { class: 'metrics', 'aria-label': 'Global cluster statistics' },
                stat(stats.accounts, 'accounts'), stat(`${stats.online_nodes}/${stats.nodes}`, 'nodes online'),
                stat(stats.networks, 'networks'), stat(stats.gpus, 'GPUs'),
                stat(stats.projects, 'projects'), stat(stats.running_jobs, 'running jobs'),
                stat(stats.controllers, 'controllers'), stat(stats.disabled_accounts, 'disabled accounts')),
			node('div', { class: 'dashboard-grid' },
				panel('Accounts', accounts.map((item) => accountRow(item, status.account, draw)), 'No accounts registered.'),
				panel('Devices', nodes.map(nodeRow), 'No devices have checked in yet.'),
				panel('Network registry', networks.map((item) => networkRow(item, draw)), 'No networks registered.'),
				panel('Controller registry', controllers.map(controllerRow), 'No controller servers registered.'))));
    };
    await draw();
}

async function start() {
    try {
        const status = await api('/api/auth/status');
        if (!status.authenticated) login(status);
        else if (!status.account.admin) {
			app.replaceChildren(node('main', { class: 'login-wrap' }, node('section', { class: 'panel login' },
				node('div', { class: 'top top--login' }, brandLogo('cluster')),
                node('h1', {}, 'Account ready'),
				node('p', { class: 'muted' }, 'This page is reserved for cluster administrators. Your account can be used by Plainshow devices.'),
				node('button', { class: 'btn btn--full', onclick: async () => {
					await api('/api/auth/logout', { method: 'POST', body: {} });
					await start();
				} }, 'Sign out'))));
        } else await dashboard(status);
    } catch (cause) {
		app.replaceChildren(node('main', { class: 'login-wrap' }, node('section', { class: 'panel login' },
			node('div', { class: 'top top--login' }, brandLogo('cluster')),
			node('h1', {}, 'Cannot load the admin service'),
			node('p', { class: 'muted' }, cause.message),
			node('button', { class: 'btn primary', onclick: start }, 'Try again'))));
    }
}

start();
