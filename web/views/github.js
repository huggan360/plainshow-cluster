// GitHub — connect an account once, then use your repositories from here.

import { el, mount, ago } from '../lib/ui.js';
import { api, toast, modal, navigate, refresh } from '../lib/client.js';

const TOKEN_URL =
    'https://github.com/settings/tokens/new?description=Plainshow%20Cluster&scopes=repo,workflow';

export async function renderGitHub(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const draw = async () => {
        const status = await api('/api/github');
        mount(page,
            el('div', { class: 'detail-head', style: 'margin-bottom:24px' },
                el('span', { class: 'ps-project-mark' }, el('i', { class: 'bx bxl-github' })),
                el('div', { class: 'detail-head__copy' },
                    el('p', { class: 'page__eyebrow' }, 'Source control'),
                    el('h1', { class: 'page__title' }, 'GitHub'),
                    el('p', { class: 'page__sub' },
                        'Connect once to clone repositories, push changes and keep project access in sync.'))),
            status.connected ? connected(status, draw) : disconnected(status, draw));
    };

    await draw();
    return null;
}

function connected(status, draw) {
    const repos = status.repositories || {};
    return el('div', { class: 'grid grid--2' },
        el('div', { class: 'frame' }, el('div', { class: 'frame__in' },
            el('div', { class: 'panel__head' },
                el('span', { class: 'grow' }, 'Connected'),
                el('span', { class: 'chip chip--good' }, 'active')),
            el('div', { style: 'font-size:20px;font-weight:700;letter-spacing:-.02em' },
                `@${status.account}`),
            el('p', { class: 'muted', style: 'margin:4px 0 18px;font-size:12.5px' },
                [status.name, status.email].filter(Boolean).join(' · ') || 'No public name set'),
            el('div', { class: 'grid grid--3', style: 'gap:10px' },
                stat(repos.total, 'Repositories'),
                stat(repos.private, 'Private'),
                stat(status.git_ready ? 'Ready' : 'Limited', 'Access',
                    status.git_ready ? 'var(--good)' : 'var(--warn)')),
            status.error
                ? el('p', {
                    style: 'margin:16px 0 0;font-size:12.5px;color:#fda4af;line-height:1.5',
                }, status.error)
                : null,
            el('div', { style: 'display:flex;gap:8px;margin-top:20px' },
                el('button', { class: 'btn btn--sm', onclick: () => connectForm(draw, true) },
                    el('i', { class: 'bx bx-refresh' }), 'Replace token'),
                el('button', {
                    class: 'btn btn--sm btn--danger',
                    onclick: () => disconnect(draw),
                }, el('i', { class: 'bx bx-unlink' }), 'Disconnect')))),

        el('div', { class: 'panel' },
            el('div', { class: 'panel__head' }, 'What you can do now'),
            el('div', { class: 'rows' },
                action('Clone a repository into a project',
                    'Bring existing code onto this machine.',
                    () => cloneForm()),
                action('Connect a project to a repository',
                    'Open a project, then use the Repository panel.',
                    () => navigate('projects')),
                action('Share a project with someone',
                    'Add their GitHub username under Team; they are invited to the repository too.',
                    () => navigate('projects')))));
}

function stat(value, label, colour) {
    return el('div', {},
        el('span', {
            style: `display:block;font-size:19px;font-weight:700;${colour ? `color:${colour}` : ''}`,
        }, value ?? 0),
        el('span', {
            class: 'mono',
            style: 'display:block;margin-top:2px;font-size:9.5px;letter-spacing:.14em;' +
                   'text-transform:uppercase;color:#475569',
        }, label));
}

function action(title, description, onclick) {
    return el('button', {
        class: 'row', style: 'text-align:left;border-width:1px', onclick,
    },
        el('span', { class: 'row__main' },
            el('span', { class: 'row__title' }, title),
            el('span', { class: 'row__meta' }, description)),
		el('i', { class: 'bx bx-right-arrow-alt dim' }));
}

function disconnected(status, draw) {
    return el('div', { class: 'panel', style: 'max-width:620px' },
        el('div', { class: 'empty', style: 'padding-bottom:12px' },
			el('i', { class: 'bx bxl-github empty__ico' }),
            el('span', { class: 'empty__text' },
                'No GitHub account is connected to this node yet.')),
        status.error
            ? el('p', {
                style: 'margin:0 0 16px;font-size:12.5px;color:#fda4af;text-align:center',
            }, status.error)
            : null,
        el('div', { style: 'display:flex;justify-content:center' },
            el('button', { class: 'btn btn--primary', onclick: () => connectForm(draw, false) },
                el('i', { class: 'bx bxl-github' }), 'Connect GitHub')));
}

function connectForm(draw, replacing) {
    const token = el('input', {
        class: 'input input--mono', type: 'password', autocomplete: 'off',
        placeholder: 'ghp_… or github_pat_…',
    });

    modal({
        title: replacing ? 'Replace token' : 'Connect GitHub',
        confirmLabel: replacing ? 'Replace' : 'Connect',
        body: () => el('div', {},
            el('p', { style: 'margin:0 0 16px;font-size:12.5px;color:#94a3b8;line-height:1.6' },
                'Create a classic personal access token with the ',
                el('strong', {}, 'repo'),
                ' and ',
                el('strong', {}, 'workflow'),
                ' scopes. That is what lets Plainshow Cluster create repositories, push, ' +
                'pull, and manage who may work on a project.'),
            el('a', {
                class: 'btn btn--sm', href: TOKEN_URL, target: '_blank', rel: 'noreferrer',
                style: 'margin-bottom:16px',
            }, el('i', { class: 'bx bx-link-external' }), 'Create a token on GitHub'),
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Token'), token),
            el('p', { class: 'muted', style: 'margin:0;font-size:11.5px' },
                'The token is stored inside this node’s own directory, readable only ' +
                'by the account running it, and is never written into a project.')),
        onConfirm: async (close) => {
            const value = token.value.trim();
            if (value.length < 20) throw new Error('That does not look like a GitHub token.');
            const result = await api('/api/github', { method: 'POST', body: { token: value } });
            close();
            toast(`Connected as @${result.account}.`);
            await refresh();
            await draw();
        },
    });
}

function disconnect(draw) {
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
            await draw();
        },
    });
}

/** cloneForm creates a project from an existing repository. */
export function cloneForm(after) {
    const repo = el('input', { class: 'input input--mono', placeholder: 'owner/repository' });
    const name = el('input', { class: 'input', placeholder: 'Leave empty to use the repo name' });
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
            el('p', { class: 'field__label', style: 'margin:6px 0 0' }, 'Your repositories'),
            list),
        onConfirm: async (close) => {
            const project = await api('/api/github/clone', {
                method: 'POST',
                body: { repository: repo.value.trim(), name: name.value.trim() },
            });
            close();
            toast(`Cloned into ${project.name}.`);
            await refresh();
            if (after) await after();
            navigate(`projects/${encodeURIComponent(project.name)}`);
        },
    });
}
