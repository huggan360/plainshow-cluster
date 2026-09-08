// Branches — one working tree each.
//
// Two branches in one directory means checking out to switch, which throws away
// uncommitted work and pulls the files out from under whatever is running. So
// each branch gets its own project directory, and this is where you move
// between them.

import { el, mount } from '../lib/ui.js';
import { api, toast, modal, navigate } from '../lib/client.js';

// main is the trunk and reads as such; everything else takes a colour from its
// name so the same branch is the same colour everywhere in the interface.
const BRANCH_TONES = ['amber', 'violet', 'emerald', 'rose', 'sky'];

export function branchTone(branch) {
    if (branch === 'main' || branch === 'master') return 'main';
    let hash = 0;
    for (const character of String(branch)) hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
    return BRANCH_TONES[hash % BRANCH_TONES.length];
}

/** branchChip is the small coloured label used on cards and rows. */
export function branchChip(branch) {
    return el('span', { class: `branch-chip branch-chip--${branchTone(branch)}` },
        el('i', { class: 'bx bx-git-branch' }), branch || 'main');
}

/** branchPane builds the Branch tab for a project. */
export function branchPane(project) {
    const box = el('div', {});

    const load = async () => {
        let data;
        try {
            data = await api(`/api/projects/${encodeURIComponent(project)}/branches`);
        } catch (err) {
            mount(box, el('div', { class: 'panel' },
                el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' }, err.message)));
            return;
        }
        const branches = data.branches || [];
        mount(box, el('section', { class: 'ps-metric-card ps-metric-card--branch' },
            el('div', { class: 'ps-metric-card__surface', style: 'padding:16px' },
                el('div', { class: 'branch-head' },
                    el('span', { class: 'ps-metric-card__icon' }, el('i', { class: 'bx bx-git-branch' })),
                    el('div', { class: 'grow' },
                        el('h2', { class: 'branch-head__title' }, 'Branches'),
                        el('p', { class: 'branch-head__meta' },
                            data.repository
                                ? `${branches.length} branch${branches.length === 1 ? '' : 'es'} · ${data.repository}`
                                : 'No repository connected')),
                    el('button', {
                        class: 'btn btn--primary', disabled: !data.repository,
                        onclick: () => pushDialog(project, data, load),
                    }, el('i', { class: 'bx bx-upload' }), 'Push to branch')),

                branches.length
                    ? el('div', { class: 'branch-list' },
                        ...branches.map((row) => branchRow(row, data.current)))
                    : el('p', { class: 'muted', style: 'margin:14px 0 0;font-size:12.5px' },
                        'This project has no git history yet.'),

                !data.repository
                    ? el('p', { class: 'muted', style: 'margin:14px 0 0;font-size:12px;line-height:1.6' },
                        'Connect a GitHub repository under Git, and pushing to a branch ' +
                        'will give that branch its own folder here.')
                    : null)));
    };

    load();
    return { node: box, reload: load };
}

function branchRow(row, current) {
    const held = row.project;
    return el('div', { class: 'ps-status-row branch-row' },
        el('span', { class: `ps-status-dot ${held ? 'ps-status-dot--online' : 'ps-status-dot--offline'}` }),
        el('span', { class: 'branch-row__main' },
            el('span', { class: 'branch-row__name' }, row.branch,
                row.branch === current
                    ? el('span', { class: 'branch-row__here' }, 'you are here') : null),
            el('span', { class: 'branch-row__meta' },
                held ? held.name : 'no folder on this machine yet')),
        branchChip(row.branch),
        held && !row.is_this_project
            ? el('button', {
                class: 'btn btn--sm',
                onclick: () => navigate(`projects/${encodeURIComponent(held.id)}`),
            }, 'Open', el('i', { class: 'bx bx-right-arrow-alt' }))
            : null);
}

function pushDialog(project, data, reload) {
    const known = (data.branches || []).map((row) => row.branch);
    const picker = el('select', { class: 'input' },
        ...known.map((branch) => el('option', {
            value: branch, selected: branch === data.current,
        }, branch)),
        el('option', { value: '__new' }, 'A new branch…'));
    const fresh = el('input', {
        class: 'input input--mono', placeholder: 'dev', style: 'display:none;margin-top:8px',
    });
    picker.addEventListener('change', () => {
        fresh.style.display = picker.value === '__new' ? '' : 'none';
        if (picker.value === '__new') fresh.focus();
    });

    modal({
        title: 'Push to a branch',
        confirmLabel: 'Push',
        body: () => el('div', {},
            el('label', { class: 'field' },
                el('span', { class: 'field__label' }, 'Branch'), picker),
            fresh,
            el('p', { class: 'muted', style: 'margin:14px 0 0;font-size:12px;line-height:1.65' },
                'A branch that does not exist yet is created on GitHub, and given its ' +
                'own folder here so both branches can be checked out at once. If a ' +
                'folder already holds that branch, nothing new is made — pull there ' +
                'to pick this up.')),
        onConfirm: async (close) => {
            const branch = picker.value === '__new' ? fresh.value.trim() : picker.value;
            if (!branch) throw new Error('Name the branch to push to.');
            const result = await api(
                `/api/projects/${encodeURIComponent(project)}/branches/push`,
                { method: 'POST', body: { branch } });
            close();
            toast(result.detail || `Pushed to ${branch}.`);
            await reload();
        },
    });
}
