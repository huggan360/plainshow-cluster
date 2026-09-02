// Notebooks — standard .ipynb files backed by one persistent Python kernel per project.

import { el, mount } from '../lib/ui.js';
import { api, modal, navigate, toast } from '../lib/client.js';

export async function renderNotebooks(host, args) {
    const page = el('div', { class: 'page' });
    mount(host, page);
    const projects = await api('/api/projects');
    if (!projects.length) {
        mount(page, heading(), el('div', { class: 'panel' },
            el('div', { class: 'empty' },
                el('strong', {}, 'Create a project first'),
                el('span', {}, 'A notebook is kept in a project beside the code it uses.'),
                el('button', { class: 'btn btn--primary', onclick: () => navigate('workspace') },
                    'Open Workspace'))));
        return null;
    }

    const project = args[0] || projects[0].name;
    const requested = args.slice(1).join('/');
    const tree = await api(`/api/projects/${encodeURIComponent(project)}/tree`);
    const notebooks = flatten(tree).filter((entry) =>
        !entry.dir && entry.path.toLowerCase().endsWith('.ipynb'));

    if (requested) {
        return renderEditor(page, project, decodeURIComponent(requested));
    }

    mount(page, heading(),
        el('div', { class: 'page__head', style: 'padding-top:0' },
            el('div', { style: 'display:flex;gap:8px;flex-wrap:wrap' },
                ...projects.map((p) => el('button', {
                    class: `btn btn--sm ${p.name === project ? 'btn--primary' : ''}`,
                    onclick: () => navigate(`notebooks/${encodeURIComponent(p.name)}`),
                }, p.name))),
            el('button', { class: 'btn btn--primary', onclick: () => createNotebook(project) },
                '+ New notebook')),
        notebooks.length
            ? el('div', { class: 'grid grid--3' }, ...notebooks.map((book) =>
                el('button', {
                    class: 'panel', style: 'text-align:left;cursor:pointer',
                    onclick: () => navigate(`notebooks/${encodeURIComponent(project)}/${encodeURIComponent(book.path)}`),
                },
                el('div', { class: 'panel__head' }, 'Python notebook'),
                el('strong', {}, book.name),
                el('p', { class: 'muted', style: 'margin:5px 0 0;font-size:11px' }, book.path))))
            : el('div', { class: 'panel' }, el('div', { class: 'empty' },
                el('strong', {}, 'No notebooks yet'),
                el('span', {}, 'Create one here; it remains a normal .ipynb file that other tools can open.'))));
    return null;
}

function heading() {
    return el('div', { class: 'page__head' },
        el('p', { class: 'page__eyebrow' }, 'Experiment'),
        el('h1', { class: 'page__title' }, 'Notebooks'),
        el('p', { class: 'page__sub' },
            'Run Python a cell at a time. Variables and imports stay alive in the project kernel, ' +
            'and the result is saved in a standard notebook file.'));
}

function flatten(entries) {
    return entries.flatMap((entry) => [entry, ...(entry.children ? flatten(entry.children) : [])]);
}

function createNotebook(project) {
    const input = el('input', { class: 'input input--mono', value: 'experiment.ipynb' });
    modal({
        title: 'New notebook', confirmLabel: 'Create',
        body: () => el('div', { class: 'field' },
            el('label', { class: 'field__label' }, 'Path inside the project'), input),
        onConfirm: async (close) => {
            const result = await api(`/api/projects/${encodeURIComponent(project)}/notebooks`, {
                method: 'POST', body: { path: input.value },
            });
            close();
            navigate(`notebooks/${encodeURIComponent(project)}/${encodeURIComponent(result.path)}`);
        },
    });
}

async function renderEditor(page, project, path) {
    const status = await api(`/api/projects/${encodeURIComponent(project)}/jupyter`, { method: 'POST' });
    const target = status.url.replace('/tree?', `/notebooks/${path.split('/').map(encodeURIComponent).join('/')}?`);
    mount(page,
        el('div', { class: 'page__head' },
            el('p', { class: 'page__eyebrow' }, project),
            el('h1', { class: 'page__title' }, path.split('/').pop()),
            el('p', { class: 'page__sub' }, 'Jupyter Server provides kernels, rich output, completion, inspection and widgets.')),
        el('iframe', { src: target, title: path,
            style: 'width:100%;height:calc(100vh - 190px);border:1px solid var(--line);border-radius:10px;background:white' }));
    return null;
}
