// Workspace — projects, files, the editor, and running what you just wrote.

import { el, mount, ago, bytes, fileIcon, stateDot } from '../lib/ui.js';
import { api, on, toast, modal, navigate, refresh, state } from '../lib/client.js';
import { highlight, languageOf } from '../lib/highlight.js';

export async function renderWorkspace(host, args) {
    if (args.length > 0) return renderProject(host, args[0], args.slice(1).join('/'));
    return renderProjectList(host);
}

// ------------------------------------------------------------ list view ----

async function renderProjectList(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const draw = async () => {
        const projects = await api('/api/projects');
        mount(page,
            el('div', { class: 'page__head',
                style: 'display:flex;align-items:flex-end;gap:16px;flex-wrap:wrap' },
                el('div', { style: 'flex:1;min-width:260px' },
                    el('p', { class: 'page__eyebrow' }, 'Workspace'),
                    el('h1', { class: 'page__title' }, 'Projects'),
                    el('p', { class: 'page__sub' },
                        'A project is a folder of code. Edit it here, run it on this ' +
                        'machine, and keep its history in git.')),
                el('button', { class: 'btn btn--primary', onclick: () => newProject(draw) },
                    '+ New project')),
            projects.length
                ? el('div', { class: 'grid grid--3' }, ...projects.map(card))
                : el('div', { class: 'panel' }, el('div', { class: 'empty' },
                    el('span', { class: 'empty__ico' }, '◫'),
                    el('span', { class: 'empty__text' },
                        'No projects yet. Create one and you get a folder, a starter ' +
                        'file and a git history straight away.'),
                    el('button', {
                        class: 'btn btn--primary btn--sm',
                        onclick: () => newProject(draw),
                    }, 'Create a project'))));
    };

    await draw();
    return on('project.created', draw);
}

function card(p) {
    return el('a', {
        class: 'panel', href: `#/workspace/${encodeURIComponent(p.name)}`,
        style: 'display:block;text-decoration:none;color:inherit',
    },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'Project'),
            el('span', { class: 'chip chip--cyan' }, 'open')),
        el('div', { style: 'font-size:16px;font-weight:600;letter-spacing:-.015em' }, p.name),
        el('p', { class: 'muted', style: 'margin:6px 0 0;font-size:12.5px' },
            p.description || 'No description'),
        el('p', { class: 'mono dim', style: 'margin:12px 0 0;font-size:10.5px' },
            `updated ${ago(p.updated_at)}`));
}

function newProject(after) {
    const name = el('input', {
        class: 'input', placeholder: 'vision-model', autocomplete: 'off',
    });
    const desc = el('input', { class: 'input', placeholder: 'What is it for? (optional)' });

    modal({
        title: 'New project',
        confirmLabel: 'Create',
        body: () => el('div', {},
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Name'), name),
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Description'), desc),
            el('p', { class: 'muted', style: 'margin:0;font-size:12px' },
                'Letters, numbers, dashes and underscores.')),
        onConfirm: async (close) => {
            const project = await api('/api/projects', {
                method: 'POST',
                body: { name: name.value.trim(), description: desc.value.trim() },
            });
            close();
            toast(`Created ${project.name}.`);
            await refresh();
            if (after) await after();
            navigate(`workspace/${encodeURIComponent(project.name)}`);
        },
    });
}

// --------------------------------------------------------- project view ----

async function renderProject(host, name, initialPath) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const ctx = {
        name,
        path: initialPath || '',
        content: '',
        dirty: false,
        saving: false,
    };

    const treeBox = el('div', { class: 'tree' });
    const editorBox = el('div', { class: 'editor' });
    const outputBox = el('div', { class: 'console' });
    const gitBox = el('div', {});
    let currentJob = null;

    // ---- file tree ----

    async function loadTree() {
        const tree = await api(`/api/projects/${encodeURIComponent(name)}/tree`);
        mount(treeBox, ...renderTree(tree, 0));
        if (tree.length === 0) {
            mount(treeBox, el('p', { class: 'muted', style: 'font-size:12px;padding:8px' },
                'This project is empty.'));
        }
    }

    function renderTree(entries, depth) {
        return entries.map((entry) => {
            const button = el('button', {
                class: `tree__item ${entry.path === ctx.path ? 'tree__item--on' : ''}`,
                title: entry.path,
                onclick: () => {
                    if (entry.dir) {
                        kids.classList.toggle('hide');
                        button.querySelector('.tree__ico').textContent =
                            kids.classList.contains('hide') ? '▸' : '▾';
                    } else {
                        openFile(entry.path);
                    }
                },
            },
                el('span', { class: 'tree__ico' }, fileIcon(entry.name, entry.dir)),
                el('span', { class: 'tree__name' }, entry.name),
                !entry.dir && entry.size
                    ? el('span', { class: 'mono dim', style: 'margin-left:auto;font-size:9.5px' },
                        bytes(entry.size))
                    : null);

            const kids = entry.dir && entry.children && entry.children.length
                ? el('div', { class: 'tree__kids hide' }, ...renderTree(entry.children, depth + 1))
                : null;

            return el('div', {}, button, kids);
        });
    }

    // ---- editor ----

    const gutter = el('div', { class: 'code__gutter' });
    const highlightLayer = el('pre', { class: 'code__hl' });
    const input = el('textarea', {
        class: 'code__in', spellcheck: 'false', autocapitalize: 'off',
        autocomplete: 'off', wrap: 'off',
    });

    /** paint re-renders the highlight layer, gutter and textarea height. */
    function paint() {
        const text = input.value;
        highlightLayer.innerHTML = highlight(text, languageOf(ctx.path)) + '\n';
        const lines = text.split('\n').length;
        gutter.textContent = Array.from({ length: lines }, (_, i) => i + 1).join('\n');
        // The textarea is transparent and sits over the highlight layer, so it
        // has to grow to the content rather than scroll independently.
        input.style.height = 'auto';
        input.style.height = `${Math.max(input.scrollHeight, 320)}px`;
    }

    input.addEventListener('input', () => {
        ctx.content = input.value;
        setDirty(true);
        paint();
    });
    input.addEventListener('keydown', (e) => {
        if (e.key === 'Tab') {
            e.preventDefault();
            const { selectionStart: a, selectionEnd: b } = input;
            input.value = `${input.value.slice(0, a)}    ${input.value.slice(b)}`;
            input.selectionStart = input.selectionEnd = a + 4;
            ctx.content = input.value;
            setDirty(true);
            paint();
        }
        if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
            e.preventDefault();
            save();
        }
    });
    input.addEventListener('scroll', () => { highlightLayer.scrollTop = input.scrollTop; });

    const pathLabel = el('span', { class: 'editor__path' }, 'No file open');
    const dirtyDot = el('span', { class: 'editor__dirty hide' });
    const saveBtn = el('button', {
        class: 'btn btn--sm btn--primary', disabled: true, onclick: save,
    }, 'Save');

    function setDirty(value) {
        ctx.dirty = value;
        dirtyDot.classList.toggle('hide', !value);
        saveBtn.disabled = !value || ctx.saving || !ctx.path;
    }

    async function openFile(path) {
        if (ctx.dirty && !window.confirm('You have unsaved changes. Discard them?')) return;
        try {
            const file = await api(
                `/api/projects/${encodeURIComponent(name)}/file?path=${encodeURIComponent(path)}`);
            ctx.path = path;
            ctx.content = file.content;
            input.value = file.content;
            pathLabel.textContent = path;
            setDirty(false);
            paint();
            history.replaceState(null, '',
                `#/workspace/${encodeURIComponent(name)}/${path}`);
            treeBox.querySelectorAll('.tree__item').forEach((node) => {
                node.classList.toggle('tree__item--on', node.title === path);
            });
        } catch (err) {
            toast(err.message, 'err');
        }
    }

    async function save() {
        if (!ctx.path || ctx.saving) return;
        ctx.saving = true;
        saveBtn.disabled = true;
        try {
            await api(`/api/projects/${encodeURIComponent(name)}/file`, {
                method: 'PUT', body: { path: ctx.path, content: input.value },
            });
            setDirty(false);
            toast(`Saved ${ctx.path}.`);
            await loadGit();
        } catch (err) {
            toast(err.message, 'err');
        } finally {
            ctx.saving = false;
            saveBtn.disabled = !ctx.dirty;
        }
    }

    mount(editorBox,
        el('div', { class: 'editor__bar' },
            dirtyDot, pathLabel, el('span', { style: 'flex:1' }),
            el('button', {
                class: 'btn btn--sm', title: 'New file',
                onclick: () => newEntry(name, 'file', loadTree, openFile),
            }, '+ File'),
            el('button', {
                class: 'btn btn--sm', title: 'New folder',
                onclick: () => newEntry(name, 'dir', loadTree),
            }, '+ Folder'),
            saveBtn),
        el('div', { class: 'code' }, gutter,
            el('div', { class: 'code__wrap' }, highlightLayer, input)));

    // ---- run ----

    const cmd = el('input', {
        class: 'input input--mono', placeholder: 'python main.py', value: 'python3 main.py',
    });
    const runBtn = el('button', { class: 'btn btn--primary', onclick: run }, 'Run');
    const stopBtn = el('button', { class: 'btn btn--danger hide', onclick: stop }, 'Stop');

    async function run() {
        const command = cmd.value.trim();
        if (!command) { toast('Type a command to run.', 'err'); return; }
        if (ctx.dirty) await save();
        mount(outputBox, el('div', { class: 'console__line console__line--meta' },
            `$ ${command}`));
        try {
            currentJob = await api('/api/jobs', {
                method: 'POST', body: { project: name, command, title: command },
            });
            runBtn.classList.add('hide');
            stopBtn.classList.remove('hide');
        } catch (err) {
            toast(err.message, 'err');
            outputBox.append(el('div', { class: 'console__line console__line--err' }, err.message));
        }
    }

    async function stop() {
        if (!currentJob) return;
        try {
            await api(`/api/jobs/${currentJob.id}/stop`, { method: 'POST' });
        } catch (err) { toast(err.message, 'err'); }
    }

    // ---- git ----

    async function loadGit() {
        let git;
        try {
            git = await api(`/api/projects/${encodeURIComponent(name)}/git`);
        } catch { return; }

        if (!git.available) {
            mount(gitBox, el('p', { class: 'muted', style: 'font-size:12.5px;margin:0' },
                'git is not installed on this machine, so this project has no history.'));
            return;
        }
        mount(gitBox,
            el('div', { style: 'display:flex;align-items:center;gap:8px;margin-bottom:12px' },
                el('span', { class: 'chip chip--cyan' }, git.branch || 'main'),
                el('span', { class: 'chip' },
                    `${git.changes.length} change${git.changes.length === 1 ? '' : 's'}`),
                el('span', { style: 'flex:1' }),
                el('button', {
                    class: 'btn btn--sm', disabled: git.changes.length === 0,
                    onclick: () => commit(name, loadGit),
                }, 'Commit')),
            git.changes.length
                ? el('div', { class: 'rows' }, ...git.changes.slice(0, 8).map((c) =>
                    el('div', { class: 'row', style: 'cursor:default' },
                        el('span', {
                            class: `chip ${c.status === 'new' ? 'chip--good' : 'chip--warn'}`,
                        }, c.status),
                        el('span', { class: 'row__main' },
                            el('span', { class: 'row__title mono', style: 'font-size:12px' },
                                c.path)))))
                : el('p', { class: 'muted', style: 'font-size:12.5px;margin:0' },
                    'Everything is committed.'),
            git.log.length
                ? el('div', { style: 'margin-top:14px' },
                    el('div', { class: 'panel__head' }, 'History'),
                    el('div', { class: 'rows' }, ...git.log.slice(0, 5).map((entry) =>
                        el('div', { class: 'row', style: 'cursor:default' },
                            el('span', { class: 'mono dim', style: 'font-size:10.5px' },
                                entry.short),
                            el('span', { class: 'row__main' },
                                el('span', { class: 'row__title' }, entry.subject),
                                el('span', { class: 'row__meta' }, ago(entry.when)))))))
                : null);
    }

    // ---- assemble ----

    mount(page,
        el('div', { class: 'page__head',
            style: 'display:flex;align-items:flex-end;gap:16px;flex-wrap:wrap' },
            el('div', { style: 'flex:1;min-width:240px' },
                el('p', { class: 'page__eyebrow' }, 'Workspace'),
                el('h1', { class: 'page__title' }, name)),
            el('button', { class: 'btn btn--sm', onclick: () => navigate('workspace') },
                'All projects'),
            el('button', {
                class: 'btn btn--sm btn--danger',
                onclick: () => removeProject(name),
            }, 'Delete')),

        el('div', { class: 'ws' },
            el('div', { class: 'ws__side' },
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' },
                        el('span', { class: 'grow' }, 'Files'),
                        el('button', {
                            class: 'btn btn--sm btn--icon', title: 'Refresh',
                            onclick: loadTree,
                        }, '↻')),
                    treeBox)),

            el('div', { style: 'display:flex;flex-direction:column;gap:14px;min-width:0' },
                editorBox,
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' }, 'Run'),
                    el('div', { class: 'run' },
                        el('div', { class: 'field' },
                            el('label', { class: 'field__label' }, 'Command'), cmd),
                        el('div', { class: 'field', style: 'flex:0 0 150px;min-width:150px' },
                            el('label', { class: 'field__label' }, 'Run on'),
                            el('select', { class: 'select', disabled: true },
                                el('option', {}, state.overview.node.name))),
                        runBtn, stopBtn),
                    el('div', { style: 'margin-top:12px' }, outputBox)),
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' }, 'Version history'), gitBox))));

    mount(outputBox, el('div', { class: 'console__line console__line--meta' },
        'Output appears here when you run something.'));

    await loadTree();
    await loadGit();
    if (ctx.path) await openFile(ctx.path);
    paint();

    // ---- live updates ----

    const offLog = on('job.log', (line) => {
        if (!currentJob || line.job_id !== currentJob.id) return;
        const atBottom = outputBox.scrollHeight - outputBox.scrollTop
            - outputBox.clientHeight < 40;
        outputBox.append(el('div', {
            class: `console__line ${line.stream === 'stderr' ? 'console__line--err' : ''}`,
        }, line.text));
        if (atBottom) outputBox.scrollTop = outputBox.scrollHeight;
    });

    const offState = on('job.state', async (job) => {
        if (!currentJob || job.id !== currentJob.id) return;
        if (['succeeded', 'failed', 'stopped'].includes(job.state)) {
            outputBox.append(el('div', { class: 'console__line console__line--meta' },
                `— ${job.state}${job.exit_code >= 0 ? ` (exit ${job.exit_code})` : ''} —`));
            outputBox.scrollTop = outputBox.scrollHeight;
            runBtn.classList.remove('hide');
            stopBtn.classList.add('hide');
            currentJob = null;
            await loadTree();
            await loadGit();
        }
    });

    const offTree = on('tree.changed', (e) => { if (e.project === name) loadTree(); });

    // Warn before losing unsaved work to a reload or a closed tab.
    const beforeUnload = (e) => { if (ctx.dirty) { e.preventDefault(); e.returnValue = ''; } };
    window.addEventListener('beforeunload', beforeUnload);

    return () => {
        offLog(); offState(); offTree();
        window.removeEventListener('beforeunload', beforeUnload);
    };
}

// ------------------------------------------------------------- dialogues ---

function newEntry(project, kind, afterTree, openFile) {
    const path = el('input', {
        class: 'input input--mono',
        placeholder: kind === 'dir' ? 'notebooks' : 'train.py',
    });
    modal({
        title: kind === 'dir' ? 'New folder' : 'New file',
        confirmLabel: 'Create',
        body: () => el('div', {},
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Path in the project'), path),
            el('p', { class: 'muted', style: 'margin:0;font-size:12px' },
                'Nested paths are fine — parent folders are created for you.')),
        onConfirm: async (close) => {
            const value = path.value.trim();
            if (!value) throw new Error('Give the path a name.');
            const base = `/api/projects/${encodeURIComponent(project)}`;
            if (kind === 'dir') {
                await api(`${base}/dir`, { method: 'POST', body: { path: value } });
            } else {
                await api(`${base}/file`, { method: 'PUT', body: { path: value, content: '' } });
            }
            close();
            toast(`Created ${value}.`);
            await afterTree();
            if (kind === 'file' && openFile) await openFile(value);
        },
    });
}

function commit(project, after) {
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
                'A commit is a point you can come back to, and how this project ' +
                'will travel to other machines.')),
        onConfirm: async (close) => {
            const res = await api(`/api/projects/${encodeURIComponent(project)}/commit`, {
                method: 'POST', body: { message: message.value.trim() },
            });
            close();
            toast(res.committed ? 'Committed.' : 'Nothing to commit.');
            await after();
        },
    });
}

function removeProject(name) {
    const confirmName = el('input', { class: 'input input--mono', placeholder: name });
    modal({
        title: `Delete ${name}?`,
        confirmLabel: 'Delete permanently',
        danger: true,
        body: () => el('div', {},
            el('p', { style: 'margin:0 0 14px;font-size:13px;color:#cbd5e1' },
                'This removes the project directory and everything in it, including ' +
                'its git history. It cannot be undone.'),
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, `Type ${name} to confirm`),
                confirmName)),
        onConfirm: async (close) => {
            if (confirmName.value.trim() !== name) {
                throw new Error('That name does not match.');
            }
            await api(`/api/projects/${encodeURIComponent(name)}`, { method: 'DELETE' });
            close();
            toast(`Deleted ${name}.`);
            navigate('workspace');
        },
    });
}
