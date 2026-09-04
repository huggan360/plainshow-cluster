// Projects — where a project is, not where you edit it.
//
// The folder is on your machine; open it in whatever editor you use. This page
// tells you what git thinks: which branch, how far ahead or behind, what has
// changed, and who else is on it.

import { el, mount, ago } from '../lib/ui.js';
import { api, toast, modal, navigate, refresh } from '../lib/client.js';
import { teamPanel, repositoryPanel } from './team.js';
import { cloneForm } from './github.js';

export async function renderProjects(host, args) {
    if (args.length > 0) return renderProject(host, args[0]);
    return renderList(host);
}

async function renderList(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const draw = async () => {
        const projects = await api('/api/projects');
        mount(page,
            el('div', {
                class: 'page__head',
                style: 'display:flex;align-items:flex-end;gap:16px;flex-wrap:wrap',
            },
                el('div', { style: 'flex:1;min-width:260px' },
                    el('p', { class: 'page__eyebrow' }, 'Projects'),
                    el('h1', { class: 'page__title' }, 'Projects'),
                    el('p', { class: 'page__sub' },
                        'A project is a folder on this machine, kept in step with the ' +
                        'others in its network through git. Open it in your own editor.')),
                el('div', { style: 'display:flex;gap:8px' },
                    el('button', { class: 'btn', onclick: () => cloneForm(draw) },
                        'Clone from GitHub'),
                    el('button', { class: 'btn btn--primary', onclick: () => newProject(draw) },
                        '+ New project'))),

            projects.length
                ? el('div', { class: 'grid grid--3' }, ...projects.map(card))
                : el('div', { class: 'panel' }, el('div', { class: 'empty' },
                    el('span', { class: 'empty__ico' }, '◫'),
                    el('span', { class: 'empty__text' },
                        'No projects yet. Clone one from GitHub, or create one and ' +
                        'connect a repository to it.'),
                    el('button', {
                        class: 'btn btn--primary btn--sm',
                        onclick: () => cloneForm(draw),
                    }, 'Clone from GitHub'))));
    };

    await draw();
    return null;
}

function card(project) {
    return el('a', {
        class: 'panel',
        href: `#/projects/${encodeURIComponent(project.name)}`,
        style: 'display:block;text-decoration:none;color:inherit',
    },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'Project'),
            project.repository
                ? el('span', { class: 'chip chip--cyan' }, 'connected')
                : el('span', { class: 'chip' }, 'local only')),
        el('div', { style: 'font-size:16px;font-weight:600;letter-spacing:-.015em' },
            project.name),
        el('p', { class: 'muted', style: 'margin:6px 0 0;font-size:12.5px' },
            project.description || project.repository || 'No description'),
        el('p', { class: 'mono dim', style: 'margin:12px 0 0;font-size:10.5px' },
            `updated ${ago(project.updated_at)}`));
}

async function renderProject(host, name) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const repository = repositoryPanel(name, async () => {});
    const team = teamPanel(name);
    const files = el('div', {});

    // A read-only listing, so you can confirm the folder holds what you expect
    // without this becoming an editor.
    api(`/api/projects/${encodeURIComponent(name)}/tree`).then((tree) => {
        mount(files, tree.length
            ? el('div', { class: 'rows' }, ...tree.slice(0, 12).map((entry) =>
                el('div', { class: 'row', style: 'cursor:default' },
                    el('span', { class: 'dim' }, entry.dir ? '▸' : '·'),
                    el('span', { class: 'row__main' },
                        el('span', { class: 'row__title mono', style: 'font-size:12px' },
                            entry.name)))))
            : el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' },
                'This folder is empty.'));
    }).catch(() => {
        mount(files, el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' },
            'Could not read the folder.'));
    });

    mount(page,
        el('div', {
            class: 'page__head',
            style: 'display:flex;align-items:flex-end;gap:16px;flex-wrap:wrap',
        },
            el('div', { style: 'flex:1;min-width:240px' },
                el('p', { class: 'page__eyebrow' }, 'Project'),
                el('h1', { class: 'page__title' }, name)),
            el('button', { class: 'btn btn--sm', onclick: () => navigate('projects') },
                'All projects'),
            el('button', {
                class: 'btn btn--sm btn--danger',
                onclick: () => removeProject(name),
            }, 'Delete')),

        el('div', { class: 'grid grid--2' },
            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Repository'), repository.node),
            el('div', {},
                el('div', { class: 'panel', style: 'margin-bottom:14px' },
                    el('div', { class: 'panel__head' }, 'Folder'), files),
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' }, 'Team'), team.node))));

    return null;
}

function newProject(after) {
    const name = el('input', { class: 'input', placeholder: 'vision-model', autocomplete: 'off' });
    const description = el('input', { class: 'input', placeholder: 'What is it for? (optional)' });

    modal({
        title: 'New project',
        confirmLabel: 'Create',
        body: () => el('div', {},
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Name'), name),
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Description'), description),
            el('p', { class: 'muted', style: 'margin:0;font-size:12px' },
                'Creates a folder with a git repository. Connect it to GitHub ' +
                'afterwards so the others in your network can clone it.')),
        onConfirm: async (close) => {
            const project = await api('/api/projects', {
                method: 'POST',
                body: { name: name.value.trim(), description: description.value.trim() },
            });
            close();
            toast(`Created ${project.name}.`);
            await refresh();
            if (after) await after();
            navigate(`projects/${encodeURIComponent(project.name)}`);
        },
    });
}

function removeProject(name) {
    const confirmation = el('input', { class: 'input input--mono', placeholder: name });
    modal({
        title: `Delete ${name}?`,
        confirmLabel: 'Delete permanently',
        danger: true,
        body: () => el('div', {},
            el('p', { style: 'margin:0 0 14px;font-size:13px;color:#cbd5e1;line-height:1.6' },
                'This removes the folder on this machine and everything in it, ' +
                'including its git history. Anything you pushed stays on GitHub.'),
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, `Type ${name} to confirm`),
                confirmation)),
        onConfirm: async (close) => {
            if (confirmation.value.trim() !== name) {
                throw new Error('That name does not match.');
            }
            await api(`/api/projects/${encodeURIComponent(name)}`, { method: 'DELETE' });
            close();
            toast(`Deleted ${name}.`);
            navigate('projects');
        },
    });
}
