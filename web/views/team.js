// The Team and Repository panels inside a project.
//
// Access here is the Plainshow console's capability model, with the two hosting
// capabilities replaced by the two a cluster actually needs: run and train.

import { el, mount, ago, initials } from '../lib/ui.js';
import { api, toast, modal } from '../lib/client.js';

const CAPABILITIES = [
    ['view', 'View', 'Open the project, read files, logs and job history'],
    ['code', 'Code', 'Change files in the editor'],
    ['push', 'Push', 'Fetch, pull, commit and push the repository'],
    ['run', 'Run', 'Start jobs and notebook kernels'],
    ['train', 'Train', 'Start distributed training runs'],
    ['manage', 'Manage', 'Add people and change what they may do'],
];

/** teamPanel renders the collaborator list for a project. */
export function teamPanel(project, onChange) {
    const box = el('div', {});

    const load = async () => {
        const data = await api(`/api/projects/${encodeURIComponent(project)}/members`);
        const linked = Boolean(data.repository);

        mount(box,
            el('div', { style: 'display:flex;align-items:center;gap:8px;margin-bottom:12px' },
                el('span', { class: 'chip' },
                    `${data.members.length} ${data.members.length === 1 ? 'person' : 'people'}`),
                linked ? el('span', { class: 'chip chip--cyan' }, 'synced with GitHub') : null,
                el('span', { style: 'flex:1' }),
                linked
                    ? el('button', {
                        class: 'btn btn--sm', title: 'Match this list with the repository',
                        onclick: () => sync(project, load),
                    }, 'Sync')
                    : null,
                el('button', {
                    class: 'btn btn--sm btn--primary',
                    onclick: () => addPerson(project, load),
                }, '+ Add')),

            el('div', { class: 'rows' },
                ...data.members.map((m) => memberRow(project, m, load))),

            !linked
                ? el('p', { class: 'muted', style: 'margin:12px 0 0;font-size:12px;line-height:1.55' },
                    'This project has no repository yet, so people added here exist only on ' +
                    'this machine. Connect a repository and they are invited to it too.')
                : null);

        if (onChange) onChange(data);
    };

    load().catch((err) => {
        mount(box, el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' }, err.message));
    });
    return { node: box, reload: load };
}

function memberRow(project, member, reload) {
    const held = CAPABILITIES.filter(([key]) => member.capabilities[key]).map(([, label]) => label);

    return el('div', { class: 'row', style: 'cursor:default;align-items:flex-start' },
        el('span', {
            class: 'nodecard__avatar',
            style: 'width:30px;height:30px;font-size:10px;border-radius:9px',
        }, initials(member.username)),
        el('span', { class: 'row__main' },
            el('span', { class: 'row__title' },
                member.github_login ? `@${member.github_login}` : member.username),
            el('span', { class: 'row__meta' },
                member.owner ? 'owner · everything' : held.join(' · ') || 'no access')),
        el('span', { style: 'display:flex;align-items:center;gap:6px' },
            el('span', {
                class: `chip ${member.owner ? 'chip--good' : ''}`,
            }, member.owner ? 'owner' : accessLabel(member)),
            !member.owner
                ? el('button', {
                    class: 'btn btn--sm btn--icon', title: 'Change access',
                    onclick: () => editAccess(project, member, reload),
                }, '⋯')
                : null));
}

function accessLabel(member) {
    const c = member.capabilities;
    if (c.manage) return 'admin';
    if (c.train) return 'maintainer';
    if (c.push) return 'contributor';
    if (c.code) return 'editor';
    return 'viewer';
}

/** capabilityChecklist builds the toggle list shared by add and edit. */
function capabilityChecklist(initial) {
    const state = { ...initial };
    const rows = CAPABILITIES.map(([key, label, description]) => {
        const toggle = el('button', {
            class: 'toggle', role: 'switch', 'aria-label': label,
            'aria-checked': String(Boolean(state[key])),
            onclick: () => {
                state[key] = !state[key];
                // Everything implies being able to open the project; granting
                // code without view is a state nothing can express.
                if (state[key] && key !== 'view') {
                    state.view = true;
                    viewToggle.setAttribute('aria-checked', 'true');
                }
                toggle.setAttribute('aria-checked', String(state[key]));
            },
        });
        if (key === 'view') viewToggle = toggle;
        return el('div', { class: 'switch' },
            el('div', { class: 'switch__text' },
                el('strong', {}, label), el('span', {}, description)),
            toggle);
    });
    let viewToggle = rows[0].querySelector('.toggle');
    return {
        node: el('div', {}, ...rows),
        selected: () => Object.keys(state).filter((k) => state[k]),
    };
}

function addPerson(project, reload) {
    const login = el('input', { class: 'input input--mono', placeholder: 'github-username' });
    const list = capabilityChecklist({ view: true, code: true, push: true, run: true, train: true });

    modal({
        title: 'Add someone to this project',
        confirmLabel: 'Add',
        body: () => el('div', {},
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'GitHub username'), login),
            el('p', { class: 'field__label', style: 'margin:14px 0 2px' }, 'They may'),
            list.node,
            el('p', { class: 'muted', style: 'margin:14px 0 0;font-size:11.5px;line-height:1.55' },
                'If this project is connected to a repository, they are invited to it with ' +
                'the closest role GitHub can express.')),
        onConfirm: async (close) => {
            const value = login.value.trim().replace(/^@/, '');
            if (!value) throw new Error('Give a GitHub username.');
            const result = await api(`/api/projects/${encodeURIComponent(project)}/members`, {
                method: 'POST', body: { login: value, capabilities: list.selected() },
            });
            close();
            toast(result.github || `Added ${value}.`);
            await reload();
        },
    });
}

function editAccess(project, member, reload) {
    const list = capabilityChecklist(member.capabilities);

    modal({
        title: `Access for ${member.github_login || member.username}`,
        confirmLabel: 'Save',
        body: () => el('div', {},
            list.node,
            el('div', { style: 'margin-top:16px' },
                el('button', {
                    class: 'btn btn--sm btn--danger',
                    onclick: async (e) => {
                        e.preventDefault();
                        const result = await api(
                            `/api/projects/${encodeURIComponent(project)}/members/` +
                            `${encodeURIComponent(member.username)}`, { method: 'DELETE' });
                        toast(result.github || 'Removed.');
                        document.getElementById('modal').replaceChildren();
                        await reload();
                    },
                }, 'Remove from project'))),
        onConfirm: async (close) => {
            const result = await api(
                `/api/projects/${encodeURIComponent(project)}/members/` +
                `${encodeURIComponent(member.username)}`,
                { method: 'PUT', body: { capabilities: list.selected() } });
            close();
            toast(result.github || 'Access updated.');
            await reload();
        },
    });
}

async function sync(project, reload) {
    try {
        const result = await api(
            `/api/projects/${encodeURIComponent(project)}/members/sync`, { method: 'POST' });
        await reload();

        if (!result.changes.length && !result.warnings.length) {
            toast('Already in step with GitHub.');
            return;
        }
        modal({
            title: 'Synced with GitHub',
            confirmLabel: 'Done',
            body: () => el('div', {},
                result.changes.length
                    ? el('div', { class: 'rows' }, ...result.changes.map((c) =>
                        el('div', { class: 'row', style: 'cursor:default' },
                            el('span', { class: 'row__main' },
                                el('span', { class: 'row__title' }, c)))))
                    : el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' },
                        'Nothing needed changing.'),
                ...result.warnings.map((warning) => el('p', {
                    style: 'margin:14px 0 0;font-size:12px;color:#fdba74;line-height:1.55',
                }, warning))),
            onConfirm: (close) => close(),
        });
    } catch (err) {
        toast(err.message, 'err');
    }
}

/** repositoryPanel renders the repository link and push/pull controls. */
export function repositoryPanel(project, reloadTree) {
    const box = el('div', {});

    const load = async () => {
        const git = await api(`/api/projects/${encodeURIComponent(project)}/git`);
        if (!git.available) {
            mount(box, el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' },
                'git is not installed on this machine, so this project has no history.'));
            return;
        }

        const linked = Boolean(git.repository);
        mount(box,
            el('div', { style: 'display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin-bottom:12px' },
                el('span', { class: 'chip chip--cyan' }, git.branch || 'main'),
                el('span', { class: 'chip' },
                    `${git.changes.length} change${git.changes.length === 1 ? '' : 's'}`),
                linked && git.ahead ? el('span', { class: 'chip chip--warn' }, `↑ ${git.ahead}`) : null,
                linked && git.behind ? el('span', { class: 'chip chip--warn' }, `↓ ${git.behind}`) : null,
                el('span', { style: 'flex:1' }),
                el('button', {
                    class: 'btn btn--sm', disabled: git.changes.length === 0,
                    onclick: () => commit(project, load),
                }, 'Commit'),
                linked ? el('button', {
                    class: 'btn btn--sm', onclick: () => pull(project, load, reloadTree),
                }, 'Pull') : null,
                linked ? el('button', {
                    class: 'btn btn--sm btn--primary', onclick: () => push(project, load),
                }, 'Push') : null),

            git.in_merge ? conflictNotice(project, git, load, reloadTree) : null,

            linked
                ? el('div', { style: 'display:flex;align-items:center;gap:8px;margin-bottom:12px' },
                    el('a', {
                        class: 'mono', style: 'font-size:11.5px',
                        href: `https://github.com/${git.repository}`,
                        target: '_blank', rel: 'noreferrer',
                    }, git.repository, ' ↗'),
                    el('span', { style: 'flex:1' }),
                    el('button', {
                        class: 'btn btn--sm', onclick: () => unlink(project, load),
                    }, 'Disconnect'))
                : el('div', { style: 'margin-bottom:12px' },
                    el('p', { class: 'muted', style: 'margin:0 0 10px;font-size:12.5px;line-height:1.55' },
                        'This project lives only on this machine. Connect a repository and it ' +
                        'can travel: everyone keeps a full copy, work continues while others ' +
                        'are offline, and git merges what diverged.'),
                    el('div', { style: 'display:flex;gap:8px' },
                        el('button', {
                            class: 'btn btn--sm btn--primary',
                            onclick: () => linkRepository(project, true, load),
                        }, 'Create a repository'),
                        el('button', {
                            class: 'btn btn--sm',
                            onclick: () => linkRepository(project, false, load),
                        }, 'Connect an existing one'))),

            git.changes.length
                ? el('div', { class: 'rows' }, ...git.changes.slice(0, 8).map((c) =>
                    el('div', { class: 'row', style: 'cursor:default' },
                        el('span', {
                            class: `chip ${c.status === 'new' ? 'chip--good'
                                : c.status === 'conflict' ? 'chip--bad' : 'chip--warn'}`,
                        }, c.status),
                        el('span', { class: 'row__main' },
                            el('span', { class: 'row__title mono', style: 'font-size:12px' },
                                c.path)))))
                : null,

            git.log.length
                ? el('div', { style: 'margin-top:14px' },
                    el('div', { class: 'panel__head' }, 'History'),
                    el('div', { class: 'rows' }, ...git.log.slice(0, 5).map((entry) =>
                        el('div', { class: 'row', style: 'cursor:default' },
                            el('span', { class: 'mono dim', style: 'font-size:10.5px' }, entry.short),
                            el('span', { class: 'row__main' },
                                el('span', { class: 'row__title' }, entry.subject),
                                el('span', { class: 'row__meta' },
                                    `${entry.author} · ${ago(entry.when)}`))))))
                : null);
    };

    load().catch((err) => {
        mount(box, el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' }, err.message));
    });
    return { node: box, reload: load };
}

function conflictNotice(project, git, reload, reloadTree) {
    return el('div', {
        style: 'border:1px solid rgba(251,113,133,.3);background:rgba(251,113,133,.06);' +
               'border-radius:14px;padding:14px;margin-bottom:14px',
    },
        el('div', {
            class: 'mono',
            style: 'font-size:9.5px;letter-spacing:.18em;text-transform:uppercase;' +
                   'color:#fb7185;margin-bottom:8px',
        }, 'Merge needs your help'),
        el('p', { style: 'margin:0 0 10px;font-size:12.5px;color:#cbd5e1;line-height:1.6' },
            'Two people changed the same lines. The files below have both versions in ' +
            'them, marked with <<<<<<< and >>>>>>>. Open each one, keep what is right, ' +
            'delete the markers, then commit.'),
        el('div', { class: 'rows' }, ...(git.conflicts || []).map((path) =>
            el('div', { class: 'row', style: 'cursor:default' },
                el('span', { class: 'chip chip--bad' }, 'conflict'),
                el('span', { class: 'row__main' },
                    el('span', { class: 'row__title mono', style: 'font-size:12px' }, path))))),
        el('button', {
            class: 'btn btn--sm', style: 'margin-top:12px',
            onclick: async () => {
                try {
                    await api(`/api/projects/${encodeURIComponent(project)}/merge/abort`,
                        { method: 'POST' });
                    toast('Merge abandoned. Your work is back as it was.');
                    await reload();
                    if (reloadTree) await reloadTree();
                } catch (err) { toast(err.message, 'err'); }
            },
        }, 'Abandon this merge'));
}

function linkRepository(project, create, reload) {
    const repo = el('input', {
        class: 'input input--mono',
        placeholder: create ? project : 'owner/repository',
        value: create ? project : '',
    });
    const priv = { on: true };
    const toggle = el('button', {
        class: 'toggle', role: 'switch', 'aria-checked': 'true', 'aria-label': 'Private',
        onclick: () => {
            priv.on = !priv.on;
            toggle.setAttribute('aria-checked', String(priv.on));
        },
    });

    modal({
        title: create ? 'Create a repository' : 'Connect a repository',
        confirmLabel: create ? 'Create' : 'Connect',
        body: () => el('div', {},
            el('div', { class: 'field' },
                el('label', { class: 'field__label' },
                    create ? 'Repository name' : 'Repository'), repo),
            create
                ? el('div', { class: 'switch' },
                    el('div', { class: 'switch__text' },
                        el('strong', {}, 'Private'),
                        el('span', {}, 'Only people you invite can see it')),
                    toggle)
                : null,
            el('p', { class: 'muted', style: 'margin:12px 0 0;font-size:11.5px;line-height:1.55' },
                create
                    ? 'Created under your GitHub account and connected to this project. ' +
                      'Nothing is pushed until you press Push.'
                    : 'The repository must already exist and your token must be able to see it.')),
        onConfirm: async (close) => {
            const result = await api(`/api/projects/${encodeURIComponent(project)}/repository`, {
                method: 'POST',
                body: { repository: repo.value.trim(), create, private: priv.on },
            });
            close();
            toast(`Connected to ${result.repository}.`);
            await reload();
        },
    });
}

function unlink(project, reload) {
    modal({
        title: 'Disconnect the repository?',
        confirmLabel: 'Disconnect',
        danger: true,
        body: () => el('p', { style: 'margin:0;font-size:13px;color:#cbd5e1;line-height:1.6' },
            'The project keeps its files and its full history. Only the link is removed, ' +
            'so pushing and pulling stop. Nothing is deleted on GitHub.'),
        onConfirm: async (close) => {
            await api(`/api/projects/${encodeURIComponent(project)}/repository`,
                { method: 'DELETE' });
            close();
            toast('Repository disconnected.');
            await reload();
        },
    });
}

function commit(project, reload) {
    const message = el('input', {
        class: 'input', placeholder: 'What changed?', value: 'Update from the workspace',
    });
    modal({
        title: 'Commit changes',
        confirmLabel: 'Commit',
        body: () => el('div', {},
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Message'), message),
            el('p', { class: 'muted', style: 'margin:0;font-size:12px' },
                'A commit is a point you can come back to, and how this project travels ' +
                'to other machines.')),
        onConfirm: async (close) => {
            const res = await api(`/api/projects/${encodeURIComponent(project)}/commit`, {
                method: 'POST', body: { message: message.value.trim() },
            });
            close();
            toast(res.committed ? 'Committed.' : 'Nothing to commit.');
            await reload();
        },
    });
}

async function push(project, reload) {
    try {
        toast('Pushing…');
        await api(`/api/projects/${encodeURIComponent(project)}/push`, { method: 'POST' });
        toast('Pushed to GitHub.');
        await reload();
    } catch (err) {
        toast(err.message, 'err');
        await reload();
    }
}

async function pull(project, reload, reloadTree) {
    try {
        toast('Pulling…');
        const result = await api(`/api/projects/${encodeURIComponent(project)}/pull`,
            { method: 'POST' });
        if (result.conflicts && result.conflicts.length) {
            toast(`${result.conflicts.length} file(s) need your help to merge.`, 'err');
        } else {
            toast(result.merged ? 'Up to date with GitHub.' : (result.output || 'Nothing to pull.'));
        }
        await reload();
        if (reloadTree) await reloadTree();
    } catch (err) {
        toast(err.message, 'err');
        await reload();
    }
}
