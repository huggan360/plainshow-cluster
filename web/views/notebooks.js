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
    const [file, kernel] = await Promise.all([
        api(`/api/projects/${encodeURIComponent(project)}/file?path=${encodeURIComponent(path)}`),
        api(`/api/projects/${encodeURIComponent(project)}/kernel`),
    ]);
    let doc;
    try { doc = JSON.parse(file.content); } catch { throw new Error(`${path} is not valid notebook JSON.`); }
    if (!Array.isArray(doc.cells)) throw new Error(`${path} has no notebook cells.`);
    let dirty = false;
    let busy = false;
    const cellsBox = el('div', {});
    const kernelChip = el('span', {
        class: `chip ${kernel.running ? 'chip--good' : ''}`,
    }, kernel.running ? 'kernel running' : (kernel.available ? 'kernel ready' : 'Python missing'));
    const saveButton = el('button', { class: 'btn', disabled: true }, 'Save');

    const save = async () => {
        await api(`/api/projects/${encodeURIComponent(project)}/file`, {
            method: 'PUT', body: { path, content: `${JSON.stringify(doc, null, 2)}\n` },
        });
        dirty = false;
        saveButton.disabled = true;
        toast('Notebook saved.');
    };
    saveButton.onclick = save;

    const changed = () => {
        dirty = true;
        saveButton.disabled = false;
    };

    const renderCells = () => mount(cellsBox, ...doc.cells.map((cell, index) => {
        const code = el('textarea', {
            class: 'input input--mono',
            style: 'min-height:130px;resize:vertical;line-height:1.6;tab-size:4',
            value: sourceText(cell.source),
            oninput: () => { cell.source = sourceLines(code.value); changed(); },
        });
        const output = el('div', { class: 'console', style: 'min-height:0;margin-top:10px' });
        drawOutputs(output, cell.outputs || []);
        const run = el('button', {
            class: 'btn btn--sm btn--primary', disabled: !kernel.available,
            onclick: async () => {
                if (busy) return;
                busy = true;
                run.disabled = true;
                run.textContent = 'Running…';
                try {
                    cell.source = sourceLines(code.value);
                    const result = await api(`/api/projects/${encodeURIComponent(project)}/kernel/execute`, {
                        method: 'POST', body: { code: code.value },
                    });
                    cell.execution_count = result.execution_count;
                    cell.outputs = result.outputs.map(toNotebookOutput);
                    drawOutputs(output, cell.outputs);
                    kernelChip.className = 'chip chip--good';
                    kernelChip.textContent = 'kernel running';
                    changed();
                } catch (err) { toast(err.message, 'err'); }
                finally { busy = false; run.disabled = false; run.textContent = 'Run'; }
            },
        }, 'Run');

        return el('div', { class: 'frame', style: 'margin-bottom:14px' },
            el('div', { class: 'frame__in' },
                el('div', { style: 'display:flex;align-items:center;gap:8px;margin-bottom:10px' },
                    el('span', { class: 'mono dim', style: 'font-size:10px' },
                        `[${cell.execution_count ?? ' '}]`),
                    el('span', { class: 'chip' }, cell.cell_type || 'code'),
                    el('span', { style: 'flex:1' }), run,
                    el('button', {
                        class: 'btn btn--sm', title: 'Remove cell',
                        onclick: () => { doc.cells.splice(index, 1); changed(); renderCells(); },
                    }, 'Remove')),
                code, output));
    }));
    renderCells();

    mount(page,
        el('div', { class: 'page__head' },
            el('p', { class: 'page__eyebrow' }, project),
            el('h1', { class: 'page__title' }, path.split('/').pop()),
            el('p', { class: 'page__sub mono', style: 'font-size:11px' }, path),
            el('div', { style: 'display:flex;gap:8px;flex-wrap:wrap;margin-top:16px' },
                el('button', { class: 'btn', onclick: () => navigate(`notebooks/${encodeURIComponent(project)}`) },
                    '← Notebooks'),
                kernelChip,
                el('span', { style: 'flex:1' }),
                el('button', {
                    class: 'btn', onclick: async () => {
                        await api(`/api/projects/${encodeURIComponent(project)}/kernel/interrupt`, { method: 'POST' });
                        toast('Interrupt sent.');
                    },
                }, 'Interrupt'),
                el('button', {
                    class: 'btn', onclick: async () => {
                        await api(`/api/projects/${encodeURIComponent(project)}/kernel/restart`, { method: 'POST' });
                        kernelChip.className = 'chip'; kernelChip.textContent = 'kernel ready';
                        toast('Kernel restarted. Variables were cleared.');
                    },
                }, 'Restart'), saveButton)),
        cellsBox,
        el('button', {
            class: 'btn', onclick: () => {
                doc.cells.push({
                    id: `cell-${Date.now()}`, cell_type: 'code', metadata: {},
                    source: [''], outputs: [], execution_count: null,
                });
                changed(); renderCells();
            },
        }, '+ Add cell'));

    window.addEventListener('beforeunload', warn);
    function warn(event) {
        if (!dirty) return;
        event.preventDefault();
    }
    return () => window.removeEventListener('beforeunload', warn);
}

function sourceText(source) {
    return Array.isArray(source) ? source.join('') : String(source || '');
}

function sourceLines(text) {
    return text.match(/.*(?:\n|$)/g).filter(Boolean);
}

function toNotebookOutput(output) {
    if (output.type === 'result') {
        return { output_type: 'execute_result', data: { 'text/plain': [output.text] }, metadata: {} };
    }
    if (output.type === 'error') {
        return { output_type: 'error', ename: 'Error', evalue: '', traceback: output.text.split('\n') };
    }
    return { output_type: 'stream', name: output.type === 'stderr' ? 'stderr' : 'stdout', text: [output.text] };
}

function drawOutputs(host, outputs) {
    if (!outputs.length) { mount(host); return; }
    mount(host, ...outputs.map((output) => {
        const text = output.output_type === 'execute_result'
            ? sourceText(output.data && output.data['text/plain'])
            : output.output_type === 'error'
                ? (output.traceback || []).join('\n')
                : sourceText(output.text);
        return el('div', {
            class: `console__line ${output.name === 'stderr' || output.output_type === 'error'
                ? 'console__line--err' : ''}`,
        }, text);
    }));
}
