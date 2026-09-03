// Workspace — projects, files, the editor, and running what you just wrote.

import { el, mount, ago, bytes, fileIcon, stateDot } from '../lib/ui.js';
import { api, on, onConnection, send, isLive, toast, modal, navigate, refresh, state } from '../lib/client.js';
import { highlight, languageOf } from '../lib/highlight.js';
import { teamPanel, repositoryPanel } from './team.js';
import { cloneForm } from './github.js';

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
	const availableDatasets = await api('/api/datasets');
    const page = el('div', { class: 'page' });
    mount(host, page);

    const ctx = {
        name,
        path: initialPath || '',
        content: '',
        dirty: false,
        saving: false,
		projectId: state.overview.projects.find((project) => project.name === name)?.id || '',
        networkId: state.overview.active_network,
		revision: 0,
		nextBase: 0,
		pending: new Set(),
		expanded: new Set(),
    };
	const clientId = sessionStorage.getItem('plainshow.client') || crypto.randomUUID();
	sessionStorage.setItem('plainshow.client', clientId);
	const sequenceKey = `plainshow.sequence.${clientId}`;
	let operationSequence = Number(sessionStorage.getItem(sequenceKey)) || Date.now();
	const nextSequence = () => {
		operationSequence = Math.max(operationSequence + 1, Date.now());
		sessionStorage.setItem(sequenceKey, String(operationSequence));
		return operationSequence;
	};

    const treeBox = el('div', { class: 'tree' });
    const editorBox = el('div', { class: 'editor' });
    const outputBox = el('div', { class: 'console' });
	const uploadInput = el('input', {
		type: 'file', multiple: true, class: 'hide',
		onchange: async () => {
			await uploadFiles([...uploadInput.files]);
			uploadInput.value = '';
		},
	});
    let currentJob = null;

    // ---- file tree ----

    async function loadTree() {
        const tree = await api(`/api/projects/${encodeURIComponent(name)}/tree`);
        mount(treeBox, ...renderTree(tree, 0));
        if (tree.length === 0) {
            mount(treeBox, el('p', { class: 'muted', style: 'font-size:12px;padding:8px' },
                'This project is empty.'));
        }
		return tree;
    }

    function renderTree(entries, depth) {
        return entries.map((entry) => {
			const open = ctx.expanded.has(entry.path);
			const kids = entry.dir && entry.children && entry.children.length
				? el('div', { class: `tree__kids ${open ? '' : 'hide'}` },
					...renderTree(entry.children, depth + 1))
				: null;
            const button = el('button', {
                class: `tree__item ${entry.path === ctx.path ? 'tree__item--on' : ''}`,
                title: entry.path,
                onclick: () => {
                    if (entry.dir) {
						if (!kids) return;
                        kids.classList.toggle('hide');
						const expanded = !kids.classList.contains('hide');
						button.querySelector('.tree__ico').textContent = expanded ? '▾' : '▸';
						if (expanded) ctx.expanded.add(entry.path);
						else ctx.expanded.delete(entry.path);
                    } else {
                        openFile(entry.path);
                    }
                },
            },
				el('span', { class: 'tree__ico' }, entry.dir ? (open ? '▾' : '▸') : fileIcon(entry.name, false)),
                el('span', { class: 'tree__name' }, entry.name),
                !entry.dir && entry.size
                    ? el('span', { class: 'mono dim', style: 'margin-left:auto;font-size:9.5px' },
                        bytes(entry.size))
                    : null);
			const actions = el('div', { class: 'tree__actions' },
				!entry.dir ? el('a', {
					class: 'tree__action', title: `Download ${entry.name}`,
					'aria-label': `Download ${entry.name}`, download: entry.name,
					href: `/api/projects/${encodeURIComponent(name)}/raw?path=${encodeURIComponent(entry.path)}`,
				}, '↓') : null,
				el('button', {
					class: 'tree__action', title: `Rename ${entry.name}`,
					'aria-label': `Rename ${entry.name}`,
					onclick: () => renameEntryDialog(entry),
				}, '✎'),
				el('button', {
					class: 'tree__action tree__action--danger', title: `Delete ${entry.name}`,
					'aria-label': `Delete ${entry.name}`,
					onclick: () => removeEntryDialog(entry),
				}, '×'));

            return el('div', { class: 'tree__node' },
				el('div', { class: 'tree__row' }, button, actions), kids);
        });
    }

	async function uploadFiles(files) {
		if (!files.length) return;
		const uploadButton = document.getElementById('workspace-upload');
		if (uploadButton) {
			uploadButton.disabled = true;
			uploadButton.textContent = `Uploading 0/${files.length}`;
		}
		const uploaded = [];
		try {
			for (const [index, file] of files.entries()) {
				const rawPath = file.webkitRelativePath || file.name;
				const target = rawPath.replaceAll('\\', '/').split('/')
					.filter((part) => part && part !== '.' && part !== '..').join('/');
				if (!target) continue;
				const form = new FormData();
				form.append('file', file, file.name);
				await api(`/api/projects/${encodeURIComponent(name)}/upload?path=${encodeURIComponent(target)}`, {
					method: 'POST', body: form,
				});
				uploaded.push(target);
				if (uploadButton) uploadButton.textContent = `Uploading ${index + 1}/${files.length}`;
			}
			await loadTree();
			if (uploaded.length === 1) {
				try { await openFile(uploaded[0]); } catch { /* binary uploads stay downloadable */ }
			}
			toast(`Uploaded ${uploaded.length} ${uploaded.length === 1 ? 'file' : 'files'}.`);
		} catch (err) {
			toast(err.message, 'err');
		} finally {
			if (uploadButton) {
				uploadButton.disabled = false;
				uploadButton.textContent = 'Upload';
			}
		}
	}

	function renameEntryDialog(entry) {
		const destination = el('input', { class: 'input input--mono', value: entry.path });
		modal({
			title: `Rename ${entry.name}`,
			confirmLabel: 'Rename',
			body: () => el('div', {},
				el('div', { class: 'field' },
					el('label', { class: 'field__label' }, 'New path'), destination)),
			onConfirm: async (close) => {
				const to = destination.value.trim();
				if (!to || to === entry.path) throw new Error('Choose a different path.');
				const movingOpenFile = ctx.path === entry.path || ctx.path.startsWith(`${entry.path}/`);
				if (movingOpenFile && ctx.dirty) {
					throw new Error('Wait for the open file to finish syncing before renaming it.');
				}
				await api(`/api/projects/${encodeURIComponent(name)}/rename`, {
					method: 'POST', body: { from: entry.path, to },
				});
				const movedPath = movingOpenFile ? `${to}${ctx.path.slice(entry.path.length)}` : '';
				close();
				await loadTree();
				if (movedPath) await openFile(movedPath);
				toast(`Renamed to ${to}.`);
			},
		});
	}

	function removeEntryDialog(entry) {
		modal({
			title: `Delete ${entry.name}?`, confirmLabel: 'Delete', danger: true,
			body: () => el('p', { class: 'muted', style: 'margin:0;font-size:13px' },
				entry.dir
					? 'The folder and everything inside it will be removed. This cannot be undone.'
					: 'The file will be removed. You can recover committed versions with Git.'),
			onConfirm: async (close) => {
				const deletingOpenFile = ctx.path === entry.path || ctx.path.startsWith(`${entry.path}/`);
				if (deletingOpenFile && ctx.dirty) {
					throw new Error('Wait for the open file to finish syncing before deleting it.');
				}
				await api(`/api/projects/${encodeURIComponent(name)}/entry?path=${encodeURIComponent(entry.path)}`,
					{ method: 'DELETE' });
				if (deletingOpenFile) closeEditor();
				close();
				await loadTree();
				toast(`Deleted ${entry.name}.`);
			},
		});
	}

    // ---- editor ----

    const gutter = el('div', { class: 'code__gutter' });
    const highlightLayer = el('pre', { class: 'code__hl' });
    const input = el('textarea', {
        class: 'code__in', spellcheck: 'false', autocapitalize: 'off',
		autocomplete: 'off', wrap: 'off', disabled: true,
    });
	const emptyEditor = el('div', { class: 'code__empty' },
		el('span', { class: 'code__empty-icon' }, '⌁'),
		el('strong', {}, 'Open a file to start editing'),
		el('span', {}, 'Create a file, upload one, or choose it from the explorer.'));

    /** paint re-renders the highlight layer, gutter and textarea height. */
    function paint() {
        const text = input.value;
		emptyEditor.classList.toggle('hide', Boolean(ctx.path));
		gutter.classList.toggle('hide', !ctx.path);
		input.classList.toggle('hide', !ctx.path);
		highlightLayer.classList.toggle('hide', !ctx.path);
        highlightLayer.innerHTML = highlight(text, languageOf(ctx.path)) + '\n';
        const lines = text.split('\n').length;
        gutter.textContent = Array.from({ length: lines }, (_, i) => i + 1).join('\n');
        // The textarea is transparent and sits over the highlight layer, so it
        // has to grow to the content rather than scroll independently.
        input.style.height = 'auto';
        input.style.height = `${Math.max(input.scrollHeight, 320)}px`;
    }

    input.addEventListener('input', () => {
		const before = ctx.content;
		const after = input.value;
		const edit = textEdit(before, after);
		ctx.content = after;
		if (ctx.path && edit) {
			const sequence = nextSequence();
			ctx.pending.add(sequence);
			send('collab.op', { network_id: ctx.networkId, project_id: ctx.projectId,
				project: name, path: ctx.path, client_id: clientId,
				sequence, base_revision: ctx.nextBase++, ...edit }, true);
			setDirty(true);
		}
        paint();
    });
    input.addEventListener('keydown', (e) => {
        if (e.key === 'Tab') {
            e.preventDefault();
            const { selectionStart: a, selectionEnd: b } = input;
            input.value = `${input.value.slice(0, a)}    ${input.value.slice(b)}`;
            input.selectionStart = input.selectionEnd = a + 4;
			input.dispatchEvent(new Event('input', { bubbles: true }));
        }
        if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
            e.preventDefault();
            save();
        }
		if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
			e.preventDefault();
			run();
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
				`/api/projects/${encodeURIComponent(name)}/collab?path=${encodeURIComponent(path)}`);
            ctx.path = path;
            ctx.content = file.content;
            ctx.revision = file.revision;
			ctx.nextBase = file.revision;
			ctx.pending.clear();
            input.value = file.content;
			input.disabled = false;
            pathLabel.textContent = path;
            setDirty(false);
            paint();
            history.replaceState(null, '',
				`#/workspace/${encodeURIComponent(name)}/${encodeURIComponent(path)}`);
            treeBox.querySelectorAll('.tree__item').forEach((node) => {
                node.classList.toggle('tree__item--on', node.title === path);
            });
			sendPresence();
        } catch (err) {
            toast(err.message, 'err');
        }
    }

    async function save() {
        if (!ctx.path || ctx.saving) return;
        ctx.saving = true;
        saveBtn.disabled = true;
		try {
			if (!isLive()) toast(`${ctx.path} is saved locally and will sync when reconnected.`);
			else if (ctx.pending.size) toast(`Saving ${ctx.path}…`);
			else toast(`Saved ${ctx.path}.`);
            await loadGit();
        } catch (err) {
            toast(err.message, 'err');
        } finally {
            ctx.saving = false;
            saveBtn.disabled = !ctx.dirty;
        }
    }

	function closeEditor() {
		ctx.path = '';
		ctx.content = '';
		ctx.pending.clear();
		input.value = '';
		input.disabled = true;
		pathLabel.textContent = 'No file open';
		setDirty(false);
		history.replaceState(null, '', `#/workspace/${encodeURIComponent(name)}`);
		paint();
	}

	function sendPresence() {
		if (!ctx.path) return;
		send('collab.presence', { network_id: ctx.networkId, project_id: ctx.projectId,
			project: name, path: ctx.path, client_id: clientId,
			name: state.overview.node.name, from: input.selectionStart, to: input.selectionEnd });
	}
	input.addEventListener('keyup', sendPresence);
	input.addEventListener('click', sendPresence);

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
			el('div', { class: 'code__wrap' }, highlightLayer, input, emptyEditor)));

    // ---- run ----

    const cmd = el('input', {
        class: 'input input--mono', placeholder: 'python main.py', value: 'python3 main.py',
    });
	const machineSelect = el('select', { class: 'select' },
		...state.overview.machines.filter((machine) => machine.roles.includes('worker')).map((machine) =>
			el('option', { value: machine.node_id || machine.id },
				`${machine.name}${machine.is_self ? ' · this machine' : ' · remote'}`)));
	const datasetSelect = el('select', { class: 'select' }, el('option', { value: '' }, 'No dataset'),
		...availableDatasets.map((dataset) => el('option', { value: dataset.id }, `${dataset.name} · ${dataset.version}`)));
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
                method: 'POST', body: { project: name, command, title: command,
					machine_id: machineSelect.value,
					datasets: datasetSelect.value ? [datasetSelect.value] : [] },
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

    // ---- repository and team ----

    const repository = repositoryPanel(name, loadTree);
    const team = teamPanel(name);
    const loadGit = repository.reload;

    // ---- assemble ----

	const filePanel = el('div', { class: 'panel file-panel' },
		el('div', { class: 'panel__head' },
			el('span', { class: 'grow' }, 'Files'),
			el('button', {
				id: 'workspace-upload', class: 'btn btn--sm', title: 'Upload files',
				onclick: () => uploadInput.click(),
			}, 'Upload'),
			el('button', {
				class: 'btn btn--sm btn--icon', title: 'Refresh files',
				onclick: loadTree,
			}, '↻')),
		treeBox, uploadInput,
		el('div', { class: 'file-panel__drop' }, 'Drop files to upload'));
	let dragDepth = 0;
	filePanel.addEventListener('dragenter', (event) => {
		event.preventDefault();
		dragDepth += 1;
		filePanel.classList.add('file-panel--drop');
	});
	filePanel.addEventListener('dragover', (event) => {
		event.preventDefault();
		if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
	});
	filePanel.addEventListener('dragleave', () => {
		dragDepth -= 1;
		if (dragDepth <= 0) {
			dragDepth = 0;
			filePanel.classList.remove('file-panel--drop');
		}
	});
	filePanel.addEventListener('drop', (event) => {
		event.preventDefault();
		dragDepth = 0;
		filePanel.classList.remove('file-panel--drop');
		uploadFiles([...event.dataTransfer.files]);
	});

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
				filePanel),

            el('div', { style: 'display:flex;flex-direction:column;gap:14px;min-width:0' },
                editorBox,
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' }, 'Run'),
                    el('div', { class: 'run' },
                        el('div', { class: 'field' },
                            el('label', { class: 'field__label' }, 'Command'), cmd),
                        el('div', { class: 'field', style: 'flex:0 0 150px;min-width:150px' },
                            el('label', { class: 'field__label' }, 'Run on'),
							machineSelect),
						el('div', { class: 'field', style: 'flex:0 0 180px;min-width:160px' },
							el('label', { class: 'field__label' }, 'Dataset'), datasetSelect),
                        runBtn, stopBtn),
                    el('div', { style: 'margin-top:12px' }, outputBox)),
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' }, 'Repository'), repository.node),
                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' }, 'Team'), team.node))));

    mount(outputBox, el('div', { class: 'console__line console__line--meta' },
        'Output appears here when you run something.'));

	const initialTree = await loadTree();
	if (ctx.path) {
		await openFile(ctx.path);
	} else {
		const files = flattenFiles(initialTree);
		const first = files.find((entry) => entry.path === 'main.py') ||
			files.find((entry) => entry.path.toLowerCase() === 'readme.md') || files[0];
		if (first) await openFile(first.path);
	}
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
	const offReplace = on('file.replaced', async (event) => {
		if (event.project !== name || event.path !== ctx.path) return;
		if (ctx.dirty) {
			toast(`${event.path} was replaced while you had pending edits. Reload it before continuing.`, 'err');
			return;
		}
		await openFile(ctx.path);
	});
	const offRename = on('entry.renamed', async (event) => {
		if (event.project !== name ||
			(ctx.path !== event.from && !ctx.path.startsWith(`${event.from}/`))) return;
		if (ctx.dirty) {
			toast('An open path was renamed while you had pending edits. Reload the workspace.', 'err');
			return;
		}
		await openFile(`${event.to}${ctx.path.slice(event.from.length)}`);
	});
	const offDelete = on('entry.deleted', (event) => {
		if (event.project !== name ||
			(ctx.path !== event.path && !ctx.path.startsWith(`${event.path}/`))) return;
		if (ctx.dirty) {
			toast('An open path was deleted while you had pending edits. Reload the workspace.', 'err');
			return;
		}
		closeEditor();
	});
	const offCollab = on('collab.op', (op) => {
		if (op.project_id !== ctx.projectId || op.path !== ctx.path) return;
		ctx.revision = Math.max(ctx.revision, op.revision);
		ctx.nextBase = Math.max(ctx.nextBase, op.revision);
		if (op.client_id === clientId) {
			ctx.pending.delete(op.sequence);
			if (ctx.dirty && ctx.pending.size === 0) setDirty(false);
			return;
		}
		const chars = Array.from(ctx.content);
		chars.splice(op.from, op.to - op.from, ...Array.from(op.insert));
		ctx.content = chars.join(''); input.value = ctx.content; paint();
	});
	const offReject = on('collab.reject', async (rejected) => {
		if (rejected.client_id !== clientId || rejected.project_id !== ctx.projectId || rejected.path !== ctx.path) return;
		toast(`${rejected.error} Reloading the shared document.`, 'err'); await openFile(ctx.path);
	});
	const presence = new Map();
	const presenceBox = el('span', { class: 'presence' });
	pathLabel.after(presenceBox);
	const offPresence = on('collab.presence', (peer) => {
		if (peer.client_id === clientId || peer.project_id !== ctx.projectId || peer.path !== ctx.path) return;
		presence.set(peer.client_id, peer.name || 'Collaborator');
		mount(presenceBox, ...[...presence.values()].map((person) => el('span', { class: 'chip chip--cyan' }, person)));
	});
	const offlineStrip = el('div', { class: 'offline-strip hide' }, 'Offline · edits are saved here and will sync when reconnected');
	page.prepend(offlineStrip);
	const offConnection = onConnection((connected) => offlineStrip.classList.toggle('hide', connected));

    // Warn before losing unsaved work to a reload or a closed tab.
    const beforeUnload = (e) => { if (ctx.dirty) { e.preventDefault(); e.returnValue = ''; } };
    window.addEventListener('beforeunload', beforeUnload);

    return () => {
		offLog(); offState(); offTree(); offReplace(); offRename(); offDelete();
		offCollab(); offReject(); offPresence(); offConnection();
        window.removeEventListener('beforeunload', beforeUnload);
    };
}

function textEdit(before, after) {
	const a = Array.from(before), b = Array.from(after);
	let from = 0;
	while (from < a.length && from < b.length && a[from] === b[from]) from += 1;
	let aEnd = a.length, bEnd = b.length;
	while (aEnd > from && bEnd > from && a[aEnd - 1] === b[bEnd - 1]) { aEnd -= 1; bEnd -= 1; }
	if (from === aEnd && from === bEnd) return null;
	return { from, to: aEnd, insert: b.slice(from, bEnd).join('') };
}

function flattenFiles(entries) {
	const files = [];
	for (const entry of entries) {
		if (entry.dir) files.push(...flattenFiles(entry.children || []));
		else files.push(entry);
	}
	return files;
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
