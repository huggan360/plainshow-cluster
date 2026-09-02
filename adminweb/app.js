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
        if (key === 'onclick') value.addEventListener('click', item);
        else value.setAttribute(key, item);
    }
    value.append(...children.filter((item) => item !== null));
    return value;
}

function header(account) {
    return node('header', { class: 'top' },
        node('span', { class: 'mark' }),
        node('div', {}, node('div', { class: 'brand' }, 'plain', node('b', {}, 'show')),
            node('div', { class: 'sub' }, 'cluster admin')),
        node('span', { class: 'grow' }),
        node('span', { class: 'muted' }, `@${account.username}`),
        node('button', { class: 'btn', onclick: async () => {
            await api('/api/auth/logout', { method: 'POST', body: {} });
            await start();
        } }, 'Sign out'));
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
    submit.addEventListener('click', async () => {
        submit.disabled = true;
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
        }
    });
    const switcher = node('button', { class: 'link', onclick: () => login(status, !registration) },
        registration ? 'I already have an account' : 'Create an account');
    app.replaceChildren(node('section', { class: 'panel login' },
        node('div', { class: 'top' }, node('span', { class: 'mark' }),
            node('div', { class: 'brand' }, 'plain', node('b', {}, 'show'))),
        node('p', { class: 'sub' }, 'Global cluster identity'),
        node('h1', {}, registration ? 'Create account' : 'Welcome back'),
        node('p', { class: 'muted' }, registration
            ? 'One identity works across every Plainshow network and device.'
            : 'Sign in to manage the Plainshow cluster environment.'),
        username.view,
        registration ? displayName.view : null,
        password.view,
        registration ? bootstrap.view : null,
        error, submit, node('p', { class: 'muted' }, switcher)));
    username.input.focus();
}

function stat(value, label) {
    return node('div', { class: 'stat' }, node('strong', {}, String(value)), node('span', {}, label));
}

function accountRow(account, current, reload) {
    const action = account.id === current.id ? node('span', { class: 'chip good' }, 'you') :
        node('button', { class: `btn ${account.disabled ? '' : 'danger'}`, onclick: async () => {
            await api(`/api/accounts/${encodeURIComponent(account.id)}`, {
                method: 'PATCH', body: { disabled: !account.disabled },
            });
            await reload();
        } }, account.disabled ? 'Enable' : 'Disable');
    return node('div', { class: 'row' }, node('div', { class: 'main' },
        node('div', { class: 'title' }, account.display_name, account.admin ? ' · admin' : ''),
        node('div', { class: 'meta' }, `@${account.username} · ${account.disabled ? 'disabled' : 'active'}`)), action);
}

function nodeRow(item) {
    const seen = new Date(item.last_seen).toLocaleString();
    return node('div', { class: 'row' }, node('div', { class: 'main' },
        node('div', { class: 'title' }, item.name),
        node('div', { class: 'meta' }, `${item.os}/${item.arch} · ${item.gpu_count} GPU · seen ${seen}`)),
        node('span', { class: 'chip' }, item.version || 'dev'));
}

async function dashboard(status) {
    const draw = async () => {
        const [stats, accounts, nodes] = await Promise.all([
            api('/api/stats'), api('/api/accounts'), api('/api/nodes'),
        ]);
        app.replaceChildren(node('div', { class: 'shell' }, header(status.account),
            node('p', { class: 'sub' }, 'Global environment'),
            node('h1', {}, 'Cluster at a glance'),
            node('p', { class: 'muted' }, 'Identity and aggregate health only. Project data and traffic stay on devices.'),
            node('section', { class: 'stats' },
                stat(stats.accounts, 'accounts'), stat(`${stats.online_nodes}/${stats.nodes}`, 'nodes online'),
                stat(stats.networks, 'networks'), stat(stats.gpus, 'GPUs'),
                stat(stats.projects, 'projects'), stat(stats.running_jobs, 'running jobs'),
                stat(stats.disabled_accounts, 'disabled accounts')),
            node('div', { class: 'columns' },
                node('section', { class: 'panel' }, node('h2', {}, 'Accounts'),
                    node('div', { class: 'rows' }, ...accounts.map((item) => accountRow(item, status.account, draw)))),
                node('section', { class: 'panel' }, node('h2', {}, 'Devices'),
                    node('div', { class: 'rows' }, ...nodes.map(nodeRow))))));
    };
    await draw();
}

async function start() {
    try {
        const status = await api('/api/auth/status');
        if (!status.authenticated) login(status);
        else if (!status.account.admin) {
            app.replaceChildren(node('section', { class: 'panel login' },
                node('h1', {}, 'Account ready'),
                node('p', { class: 'muted' }, 'This page is reserved for cluster administrators. Your account can be used by Plainshow devices.')));
        } else await dashboard(status);
    } catch (cause) {
        app.textContent = cause.message;
    }
}

start();
