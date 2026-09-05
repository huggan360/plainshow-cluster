// The Team and Repository panels inside a project.
//
// Access here is the Plainshow console's capability model, with the two hosting
// capabilities replaced by the two a cluster actually needs: run and train.

import { el, mount, ago, initials } from '../lib/ui.js';
import { api, toast, modal } from '../lib/client.js';

const CAPABILITIES = [
    ['view', 'View', 'Open the project and read its status'],
    ['code', 'Code', 'Change files in the browser'],
    ['push', 'Git', 'Fetch, pull, commit and push'],
    ['run', 'Run', 'Start jobs and notebook kernels'],
    ['train', 'Train', 'Start distributed training runs'],
    ['manage', 'Manage', 'Add users and change permissions'],
];

// The starting levels mirror GitHub's repository roles, exactly as the console
// does, so an invitation means the same thing on both sides. Someone who has
// granted access on plainshow.se should not have to learn a second vocabulary
// here.
const ACCESS_LEVELS = [
    ['pull', 'Read', 'View only', ['view']],
    ['triage', 'Triage', 'View and edit files', ['view', 'code']],
    ['push', 'Write', 'Edit, commit, push and run', ['view', 'code', 'push', 'run']],
    ['maintain', 'Maintain', 'Write plus training runs',
        ['view', 'code', 'push', 'run', 'train']],
    ['admin', 'Admin', 'Full control',
        ['view', 'code', 'push', 'run', 'train', 'manage']],
];

// accessWord names a set of capabilities the way the console names it. The
// order matters: it reads downwards and stops at the first thing held.
function accessWord(capabilities) {
    if (!capabilities.view) return 'no access';
    if (capabilities.manage) return 'admin';
    if (capabilities.train) return 'maintain';
    if (capabilities.push) return 'write';
    if (capabilities.code) return 'triage';
    return 'read';
}

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
                    }, el('i', { class: 'bx bx-sync' }), 'Sync')
                    : null,
                el('button', {
                    class: 'btn btn--sm btn--primary',
                    onclick: () => addPerson(project, load),
                }, el('i', { class: 'bx bx-user-plus' }), 'Add')),

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
    const capabilities = { ...member.capabilities };

    // One click, one permission. The console puts the grid on the row rather
    // than behind a dialog, because the question people actually have is "what
    // can this person do", and that should be readable without opening
    // anything.
    const toggle = async (capability, enabled) => {
        const next = { ...capabilities, [capability]: !enabled };
        if (next[capability] && capability !== 'view') next.view = true;
        if (!next.view) Object.keys(next).forEach((key) => { next[key] = false; });
        try {
            const result = await api(
                `/api/projects/${encodeURIComponent(project)}/members/` +
                `${encodeURIComponent(member.username)}`,
                { method: 'PUT', body: { capabilities: Object.keys(next).filter((k) => next[k]) } });
            toast(result.github || `${member.username} · ${accessWord(next)}`);
            await reload();
        } catch (err) { toast(err.message, 'err'); }
    };

    return el('div', { class: 'panel member-card' },
        el('div', { class: 'member-card__head' },
            el('span', { class: 'avatar' }, initials(member.username)),
            el('span', { class: 'row__main' },
                el('span', { class: 'row__title' }, member.username,
                    el('span', { class: 'member-card__word' },
                        member.owner ? 'owner' : accessWord(member.capabilities))),
                el('span', { class: 'row__meta' },
                    member.owner
                        ? 'Project owner · unrestricted'
                        : member.github_login
                            ? el('span', {},
                                el('i', { class: 'bx bxl-github' }), ` ${member.github_login}`,
                                member.github_role ? ` · ${member.github_role}` : '')
                            : 'User')),
            !member.owner
                ? el('button', {
                    class: 'btn btn--sm btn--icon', title: 'Remove from project',
                    onclick: () => removeMember(project, member, reload),
                }, el('i', { class: 'bx bx-trash' }))
                : null),
        el('div', { class: 'member-card__grid' },
            ...CAPABILITIES.map(([capability, label, description]) => {
                const enabled = member.owner || Boolean(member.capabilities[capability]);
                return el('button', {
                    class: `cap ${enabled ? 'cap--on' : ''}`,
                    disabled: member.owner,
                    title: description,
                    onclick: () => toggle(capability, enabled),
                }, el('i', { class: `bx ${enabled ? 'bx-check' : 'bx-x'}` }), label);
            })));
}

function removeMember(project, member, reload) {
    modal({
        title: `Remove ${member.username}?`,
        confirmLabel: 'Remove',
        danger: true,
        body: () => el('p', { style: 'margin:0;font-size:13px;color:#cbd5e1;line-height:1.6' },
            'GitHub has no "no access" role, so withdrawing view withdraws the ' +
            'collaborator entirely. Anything less would leave them read access ' +
            'on the repository after being removed here.'),
        onConfirm: async (close) => {
            const result = await api(
                `/api/projects/${encodeURIComponent(project)}/members/` +
                `${encodeURIComponent(member.username)}`, { method: 'DELETE' });
            close();
            toast(result.github || 'Removed.');
            await reload();
        },
    });
}

function addPerson(project, reload) {
    const login = el('input', { class: 'input input--mono', placeholder: 'github-username' });
    const level = el('select', { class: 'input' }, ...ACCESS_LEVELS.map(([id, label, note]) =>
        el('option', { value: id, selected: id === 'push' }, `${label} — ${note}`)));

    modal({
        title: 'Add someone to this project',
        confirmLabel: 'Add',
        body: () => el('div', {},
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'GitHub username'), login),
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Access'), level),
            el('p', { class: 'muted', style: 'margin:6px 0 0;font-size:11.5px;line-height:1.55' },
                'These are GitHub\u2019s repository roles, so the invitation means the ' +
                'same thing on both sides. Fine-tune each permission on the row ' +
                'afterwards.')),
        onConfirm: async (close) => {
            const value = login.value.trim().replace(/^@/, '');
            if (!value) throw new Error('Give a GitHub username.');
            const chosen = ACCESS_LEVELS.find(([id]) => id === level.value);
            const result = await api(`/api/projects/${encodeURIComponent(project)}/members`, {
                method: 'POST', body: { login: value, capabilities: chosen[3] },
            });
            close();
            toast(result.github || `Added ${value}.`);
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
                    }, git.repository, ' ', el('i', { class: 'bx bx-link-external' })),
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
        class: 'input', placeholder: 'What changed?', value: 'Update current branch',
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
