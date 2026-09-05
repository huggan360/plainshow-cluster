// GitHub — the Plainshow console's GitHub page, on a node.
//
// This is deliberately the same page as plainshow.se/console: one centred card,
// the connection pill, the three counts, and the token form folded into the
// card rather than thrown into a dialog. Someone who has connected GitHub on
// the web should recognise this immediately and not have to learn it twice.

import { el, mount, ago } from '../lib/ui.js';
import { api, toast, modal, refresh, navigate, state } from '../lib/client.js';

const TOKEN_URL =
    'https://github.com/settings/tokens/new?description=Plainshow%20Cluster&scopes=repo,workflow';

export async function renderGitHub(host) {
    const page = el('div', { class: 'page gh-page' });
    mount(host, page);

    let editing = false;
    const draw = async () => {
        const status = await api('/api/github');
        // A node that has never had a token opens straight into the form; there
        // is nothing else on the page to do.
        if (!status.connected && !status.error) editing = true;
        mount(page, card(status, {
            editing,
            setEditing: (value) => { editing = value; draw(); },
            reload: draw,
        }));
    };

    await draw();
    return null;
}

function card(status, control) {
    const connected = Boolean(status.connected);
    return el('section', { class: 'panel gh-card' },
        connected ? resyncButton(control.reload) : null,
        el('div', { class: 'gh-card__head' },
            el('span', { class: 'gh-mark' }, el('i', { class: 'bx bxl-github' })),
            connected ? connectedHead(status, control) : disconnectedHead(status, control)),
        control.editing ? tokenForm(status, control) : null);
}

function connectedHead(status, control) {
    const repos = status.repositories || {};
    const ready = Boolean(status.git_ready);
    return el('div', { class: 'gh-card__body' },
        el('span', { class: 'gh-pill gh-pill--on' },
            el('span', { class: 'gh-pill__dot' }), 'Connected'),
        el('h1', { class: 'gh-title' }, `@${status.account}`),
        el('p', { class: 'gh-sub' },
            [status.name, status.email].filter(Boolean).join(' · ') || 'No public name set'),
        el('div', { class: 'gh-stats' },
            stat(repos.total, 'Repositories'),
            stat(repos.private, 'Private'),
            stat(ready ? 'Ready' : 'Missing', 'Git access', ready ? 'on' : 'off')),
        !ready && status.error
            ? el('div', { class: 'gh-warn' },
                el('div', { class: 'gh-warn__head' },
                    el('i', { class: 'bx bx-error-circle' }), 'Access needed'),
                el('p', { class: 'gh-warn__text' }, status.error))
            : null,
        control.editing ? null : el('button', {
            class: 'btn gh-action', onclick: () => control.setEditing(true),
        }, el('i', { class: 'bx bx-key' }), 'New token'));
}

function disconnectedHead(status, control) {
    return el('div', { class: 'gh-card__body' },
        el('span', { class: 'gh-pill' }, el('span', { class: 'gh-pill__dot' }), 'Not connected'),
        el('h1', { class: 'gh-title' }, 'Connect GitHub'),
        el('p', { class: 'gh-sub' }, 'Use your repositories directly from Plainshow.'),
        status.error ? el('p', { class: 'gh-error' }, status.error) : null,
        control.editing ? null : el('button', {
            class: 'btn btn--primary gh-action', onclick: () => control.setEditing(true),
        }, el('i', { class: 'bx bxl-github' }), 'Connect GitHub'));
}

function stat(value, label, tone) {
    return el('div', { class: 'gh-stat' },
        el('span', {
            class: `gh-stat__value ${tone ? `gh-stat__value--${tone}` : ''}`,
        }, String(value ?? 0)),
        el('span', { class: 'gh-stat__label' }, label));
}

function resyncButton(reload) {
    const icon = el('i', { class: 'bx bx-refresh' });
    const button = el('button', {
        class: 'gh-resync', title: 'Resync GitHub', 'aria-label': 'Resync GitHub',
        onclick: async () => {
            button.disabled = true;
            icon.classList.add('spin-icon');
            try { await reload(); } catch (err) { toast(err.message, 'err'); }
            icon.classList.remove('spin-icon');
            button.disabled = false;
        },
    }, icon);
    return button;
}

// tokenForm lives inside the card, as it does on the console. A dialog hides
// the account you are about to replace at the moment you decide whether to.
function tokenForm(status, control) {
    const connected = Boolean(status.connected);
    const token = el('input', {
        class: 'input input--mono', type: 'password', autocomplete: 'off',
        placeholder: 'github_pat_… or ghp_…',
    });
    const reveal = el('button', {
        class: 'gh-reveal', type: 'button', 'aria-label': 'Show token',
        onclick: () => {
            const hidden = token.getAttribute('type') === 'password';
            token.setAttribute('type', hidden ? 'text' : 'password');
            reveal.setAttribute('aria-label', hidden ? 'Hide token' : 'Show token');
            mount(reveal, el('i', { class: `bx ${hidden ? 'bx-hide' : 'bx-show'}` }));
        },
    }, el('i', { class: 'bx bx-show' }));

    const submit = el('button', {
        class: 'btn btn--primary',
        onclick: async () => {
            const value = token.value.trim();
            if (value.length < 20) {
                toast('That does not look like a GitHub token.', 'err');
                return;
            }
            submit.disabled = true;
            try {
                const result = await api('/api/github', { method: 'POST', body: { token: value } });
                toast(`Connected as @${result.account}.`);
                await refresh();
                control.setEditing(false);
            } catch (err) {
                toast(err.message, 'err');
                submit.disabled = false;
            }
        },
    }, el('i', { class: 'bx bx-check' }), connected ? 'Replace' : 'Connect');

    return el('div', { class: 'gh-form' },
        el('div', { class: 'gh-form__head' },
            el('div', {},
                el('h2', { class: 'gh-form__title' },
                    connected ? 'Replace token' : 'Classic token'),
                el('p', { class: 'gh-form__note' },
                    'Repository and workflow scope · create, sync, push and CI')),
            el('a', {
                class: 'gh-link', href: TOKEN_URL, target: '_blank', rel: 'noreferrer',
            }, 'Create token ', el('i', { class: 'bx bx-link-external' }))),
        el('div', { class: 'gh-input' }, token, reveal),
        el('p', { class: 'gh-form__note', style: 'margin-top:10px' },
            'The token is stored inside this node’s own directory, readable only by ' +
            'the account running it, and is never written into a project.'),
        el('div', { class: 'gh-form__foot' },
            connected
                ? el('button', { class: 'btn btn--danger', onclick: () => disconnect(control) },
                    el('i', { class: 'bx bx-unlink' }), 'Disconnect')
                : null,
            el('span', { style: 'flex:1' }),
            connected
                ? el('button', { class: 'btn', onclick: () => control.setEditing(false) }, 'Cancel')
                : null,
            submit));
}

function disconnect(control) {
    modal({
        title: 'Disconnect GitHub?',
        confirmLabel: 'Disconnect',
        danger: true,
        body: () => el('p', { style: 'margin:0;font-size:13px;color:#cbd5e1;line-height:1.6' },
            'The token is removed from this node. Projects keep their files and their ' +
            'history, but pushing, pulling and collaborator sync stop working until a ' +
            'token is connected again. Nothing changes on GitHub.'),
        onConfirm: async (close) => {
            await api('/api/github', { method: 'DELETE' });
            close();
            toast('GitHub disconnected.');
            await refresh();
            control.setEditing(true);
        },
    });
}

/** cloneForm creates a project from an existing repository. */
export function cloneForm(after) {
    const networks = state.overview.networks || [];
    if (!networks.length) {
        toast('Create or join a network before cloning a project.', 'err');
        navigate('networks');
        return;
    }
    const repo = el('input', { class: 'input input--mono', placeholder: 'owner/repository' });
    const name = el('input', { class: 'input', placeholder: 'Leave empty to use the repo name' });
    const network = el('select', { class: 'input' }, ...networks.map((item) =>
        el('option', { value: item.id }, item.name)));
    const list = el('div', {
        class: 'rows',
        style: 'max-height:190px;overflow-y:auto;margin-top:10px',
    });

    api('/api/github/repositories').then((repos) => {
        mount(list, ...repos.slice(0, 40).map((r) => el('button', {
            class: 'row', style: 'text-align:left',
            onclick: () => { repo.value = r.full_name; },
        },
            el('span', { class: 'row__main' },
                el('span', { class: 'row__title mono', style: 'font-size:12px' }, r.full_name),
                el('span', { class: 'row__meta' },
                    `${r.private ? 'private' : 'public'} · updated ${ago(r.pushed_at)}`)))));
    }).catch(() => {
        mount(list, el('p', { class: 'muted', style: 'font-size:12px;margin:0' },
            'Could not list your repositories. Type the name instead.'));
    });

    modal({
        title: 'Clone a repository',
        confirmLabel: 'Clone',
        body: () => el('div', {},
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Repository'), repo),
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Project name'), name),
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Network'), network),
            el('p', { class: 'field__label', style: 'margin:6px 0 0' }, 'Your repositories'),
            list),
        onConfirm: async (close) => {
            const project = await api('/api/github/clone', {
                method: 'POST',
                body: { repository: repo.value.trim(), name: name.value.trim(), network_id: network.value },
            });
            close();
            toast(`Cloned into ${project.name}.`);
            await refresh();
            if (after) await after();
            navigate(`projects/${encodeURIComponent(project.id)}`);
        },
    });
}
