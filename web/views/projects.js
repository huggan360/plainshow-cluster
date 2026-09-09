// Projects — branches, files, the editor, and running what you just wrote.

import { el, mount, ago, bytes, megabytes, fileIcon, stateDot } from '../lib/ui.js';
import { api, on, onConnection, send, isLive, toast, modal, navigate, refresh, state, watchRefresh } from '../lib/client.js';
import { highlight, languageOf } from '../lib/highlight.js';
import { teamPanel, repositoryPanel } from './team.js';
import { cloneForm } from './github.js';
import { conflictPane } from './conflicts.js';
import { branchPane as buildBranchPane, branchChip } from './branches.js';
import { downloadPane } from './download.js';
import { rayTools } from '../lib/raytools.js';

export async function renderProjects(host, args) {
    if (args.length > 0) return renderProject(host, args[0], args.slice(1));
    return renderProjectList(host);
}

// ------------------------------------------------------------ list view ----

async function renderProjectList(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    let query = '';
    let projects = [];
    // Branch projects are siblings of a main one, not separate work, so the
    // list opens on the trunk. Everything is one click away, and the Branch tab
    // inside a project is the other way to reach them.
    let showAll = false;
    const count = el('span', { class: 'chip' });
    const results = el('div');
    const search = el('input', { class: 'input', placeholder: 'Search projects, branches or repositories…' });
    const scope = el('div', { class: 'branch-scope' });
    const drawScope = () => {
        const extra = projects.filter((project) => !isTrunk(project)).length;
        mount(scope,
            scopeButton('Main', !showAll, () => { showAll = false; drawScope(); drawResults(); }),
            scopeButton(extra ? `All branches · ${extra}` : 'All branches', showAll,
                () => { showAll = true; drawScope(); drawResults(); }));
    };
    const drawResults = () => {
        const visible = projects.filter((project) =>
            (showAll || isTrunk(project)) &&
            `${project.name} ${project.description} ${project.repository} ${project.branch}`
                .toLowerCase().includes(query));
        count.textContent = `${visible.length} visible`;
        mount(results, visible.length
                ? el('div', { class: 'ps-card-grid' }, ...visible.map(card))
                : el('div', { class: 'panel' }, el('div', { class: 'empty' },
                    el('i', { class: 'bx bx-layer empty__ico' }),
                    el('span', { class: 'empty__text' },
                        projects.length ? 'No projects match this search.' :
                            'No projects yet. Create one and you get a folder, starter file and Git history.'),
                    el('button', {
                        class: 'btn btn--primary btn--sm',
                        onclick: () => navigate('new'),
                    }, el('i', { class: 'bx bx-plus' }), 'Create a project'))));
    };
    const orphanBox = el('div', {});
    const load = async () => {
        projects = await api('/api/projects');
        drawScope();
        drawResults();
        drawOrphans(orphanBox, load);
    };
    search.addEventListener('input', () => { query = search.value.toLowerCase(); drawResults(); });
    mount(page,
        el('div', { class: 'detail-head' },
            el('div', { class: 'detail-head__copy' },
                el('h1', { class: 'page__title' }, 'Projects')),
            el('button', { class: 'btn', onclick: () => cloneForm(load) },
                el('i', { class: 'bx bxl-github' }), 'Clone from GitHub'),
            el('button', { class: 'btn btn--primary', onclick: () => navigate('new') },
                el('i', { class: 'bx bx-plus' }), 'New project')),
        el('div', { class: 'panel ps-toolbar' },
            el('label', { class: 'ps-search' }, el('i', { class: 'bx bx-search' }), search),
            scope, count),
        orphanBox,
        results);
    await load();
    return watchRefresh(['project.created', 'project.deleted', 'project.updated', 'project.moved', 'networks.changed', 'connection.restored'], load);
}

// drawOrphans offers to clear up folders no project claims.
//
// A working tree outlives its row more often than you would think — a project
// deleted on another machine, a network left, an upgrade that renamed folders —
// and nothing else in the interface would ever mention them, because every page
// starts from the database. It never deletes on its own: a folder that looks
// orphaned to this node may be the only copy of somebody's work.
function drawOrphans(box, reload) {
    api('/api/projects-orphans').then((data) => {
        const orphans = data.orphans || [];
        if (!orphans.length) { mount(box); return; }
        const size = data.size_kb >= 1024
            ? `${(data.size_kb / 1024).toFixed(1)} MB`
            : `${data.size_kb} KB`;

        mount(box, el('section', { class: 'panel', style: 'margin:18px 0 24px' },
            el('div', { class: 'panel__head' },
                el('span', { class: 'grow' }, 'Leftover project files'),
                el('span', { class: 'chip chip--warn' }, size)),
            el('p', { class: 'muted', style: 'margin:0 0 12px;font-size:12.5px;line-height:1.6' },
                `${orphans.length} folder${orphans.length === 1 ? '' : 's'} on this machine ` +
                'belong to projects that no longer exist here. Deleting them frees the ' +
                'space; nothing on GitHub is affected.'),
            el('div', { class: 'rows' }, ...orphans.map((item) =>
                el('div', { class: 'row', style: 'cursor:default' },
                    el('i', { class: 'bx bx-folder', style: 'color:var(--warn)' }),
                    el('span', { class: 'row__main' },
                        el('span', { class: 'row__title mono', style: 'font-size:12px' }, item.name),
                        el('span', { class: 'row__meta mono' }, item.path)),
                    el('span', { class: 'chip' }, item.size_kb >= 1024
                        ? `${(item.size_kb / 1024).toFixed(1)} MB` : `${item.size_kb} KB`)))),
            el('div', { style: 'display:flex;justify-content:flex-end;margin-top:12px' },
                el('button', { class: 'btn btn--danger btn--sm',
                    onclick: () => confirmOrphans(orphans, size, reload) },
                    el('i', { class: 'bx bx-trash' }), 'Delete them'))));
    }).catch(() => mount(box));
}

function confirmOrphans(orphans, size, reload) {
    modal({
        title: 'Delete leftover files?',
        confirmLabel: 'Delete',
        danger: true,
        body: () => el('p', { style: 'margin:0;font-size:13px;color:#cbd5e1;line-height:1.6' },
            `${orphans.length} folder${orphans.length === 1 ? '' : 's'}, ${size}. ` +
            'Anything in them that was never pushed is gone for good. Your GitHub ' +
            'repositories are untouched.'),
        onConfirm: async (close) => {
            const result = await api('/api/projects-orphans/delete', {
                method: 'POST', body: { names: orphans.map((item) => item.name) },
            });
            close();
            toast(`Deleted ${(result.removed || []).length} folder(s).`);
            await reload();
        },
    });
}

// isTrunk reports whether a project is a repository's main line rather than one
// of its branches. A project with no repository is always its own trunk.
function isTrunk(project) {
    const branch = project.branch || 'main';
    return branch === 'main' || branch === 'master' || !project.repository;
}

function scopeButton(label, on, onclick) {
    return el('button', { class: `branch-scope__btn ${on ? 'branch-scope__btn--on' : ''}`, onclick },
        label);
}

function card(p) {
    return el('a', {
        class: 'panel ps-project-card', href: `#/projects/${encodeURIComponent(p.id)}`,
    },
        el('div', { class: 'ps-project-card__top' },
            el('span', { class: 'ps-project-mark' }, el('i', { class: 'bx bx-layer' })),
            el('span', { class: 'ps-project-card__copy' },
                el('strong', {}, p.name),
                el('span', {}, p.description || 'Local project')),
            el('span', { class: 'ps-project-card__status' }, branchChip(p.branch))),
        el('div', { class: 'ps-project-card__foot' },
            el('span', {}, el('i', { class: p.repository ? 'bx bxl-github' : 'bx bx-hdd' }),
                p.repository ? ' GitHub' : ' Local Git'),
            el('span', { class: 'push' }, `updated ${ago(p.updated_at)}`),
            el('i', { class: 'bx bx-right-arrow-alt' })));
}

// --------------------------------------------------------- project view ----

async function renderProject(host, reference, routeParts) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const projectTabs = new Set(['overview', 'branch', 'test', 'preset', 'run', 'git', 'branch-list', 'conflicts', 'devices', 'team', 'settings']);
    const requested = routeParts[0] || 'overview';
    const activeTab = requested === 'run' ? 'test' : projectTabs.has(requested) ? requested : 'branch';
    const initialPath = projectTabs.has(requested) ? routeParts.slice(1).join('/') : routeParts.join('/');
    // The list and the overview are separate requests. An invitation can add
    // a project after the overview snapshot while the list is already showing
    // its fresh account-adopted row; looking only in that old snapshot made
    // the card open into "No such local project." Resolve the stable id from
    // the current project list instead.
    const projects = await api('/api/projects');
    const projectSummary = projects.find((project) =>
        project.id === reference || project.name === reference);
    if (!projectSummary) throw new Error('This project is no longer available on this machine.');

    // A project can be here as a row long before its files are: adopted from
    // the account, or joined through somebody's invitation. Showing the usual
    // tabs then would be seven doors onto an empty room, so there is one thing
    // to do and this is it.
    if (projectSummary.has_files === false) {
        const pane = downloadPane(projectSummary);
        mount(page, pane.node);
        return pane.dispose;
    }

    const name = projectSummary.name;
    const projectRef = projectSummary.id;
    const gitSnapshot = await api(`/api/projects/${encodeURIComponent(projectRef)}/git`);
    const branch = gitSnapshot.branch || projectSummary.branch || 'main';

    const ctx = {
        name,
        path: initialPath || '',
        content: '',
        dirty: false,
        saving: false,
		projectId: projectSummary.id,
        networkId: projectSummary.network_id,
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
        const tree = await api(`/api/projects/${encodeURIComponent(projectRef)}/tree`);
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
						const icon = button.querySelector('.tree__ico .bx');
						if (icon) icon.className = `bx ${expanded ? 'bx-chevron-down' : 'bx-chevron-right'}`;
						if (expanded) ctx.expanded.add(entry.path);
						else ctx.expanded.delete(entry.path);
                    } else {
                        openFile(entry.path);
                    }
                },
            },
				el('span', { class: 'tree__ico' }, entry.dir
					? el('i', { class: `bx ${open ? 'bx-chevron-down' : 'bx-chevron-right'}` })
					: fileIcon(entry.name, false)),
                el('span', { class: 'tree__name' }, entry.name),
                !entry.dir && entry.size
                    ? el('span', { class: 'mono dim', style: 'margin-left:auto;font-size:9.5px' },
                        bytes(entry.size))
                    : null);
			const actions = el('div', { class: 'tree__actions' },
				!entry.dir ? el('a', {
					class: 'tree__action', title: `Download ${entry.name}`,
					'aria-label': `Download ${entry.name}`, download: entry.name,
					href: `/api/projects/${encodeURIComponent(projectRef)}/raw?path=${encodeURIComponent(entry.path)}`,
					}, el('i', { class: 'bx bx-download' })) : null,
				el('button', {
					class: 'tree__action', title: `Rename ${entry.name}`,
					'aria-label': `Rename ${entry.name}`,
					onclick: () => renameEntryDialog(entry),
					}, el('i', { class: 'bx bx-edit-alt' })),
				el('button', {
					class: 'tree__action tree__action--danger', title: `Delete ${entry.name}`,
					'aria-label': `Delete ${entry.name}`,
					onclick: () => removeEntryDialog(entry),
					}, el('i', { class: 'bx bx-trash' })));

            return el('div', { class: 'tree__node' },
				el('div', { class: 'tree__row' }, button, actions), kids);
        });
    }

	async function uploadFiles(files) {
		if (!files.length) return;
		const uploadButton = document.getElementById('branch-upload');
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
				await api(`/api/projects/${encodeURIComponent(projectRef)}/upload?path=${encodeURIComponent(target)}`, {
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
				await api(`/api/projects/${encodeURIComponent(projectRef)}/rename`, {
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
				await api(`/api/projects/${encodeURIComponent(projectRef)}/entry?path=${encodeURIComponent(entry.path)}`,
					{ method: 'DELETE' });
				if (deletingOpenFile) closeEditor();
				close();
				await loadTree();
				toast(`Deleted ${entry.name}.`);
			},
		});
	}

    // ---- editor ----

    const gutter = el('pre', { class: 'code__gutter', 'aria-hidden': 'true' });
    const highlightLayer = el('pre', { class: 'code__hl' });
    const input = el('textarea', {
        class: 'code__in', spellcheck: 'false', autocapitalize: 'off',
		autocomplete: 'off', wrap: 'off', disabled: true,
    });
	const emptyEditor = el('div', { class: 'code__empty' },
		el('i', { class: 'bx bx-code-alt code__empty-icon' }),
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
				`/api/projects/${encodeURIComponent(projectRef)}/collab?path=${encodeURIComponent(path)}`);
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
                `#/projects/${encodeURIComponent(projectRef)}/branch/${encodeURIComponent(path)}`);
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
		history.replaceState(null, '', `#/projects/${encodeURIComponent(projectRef)}/branch`);
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
                onclick: () => newEntry(projectRef, 'file', loadTree, openFile),
            }, el('i', { class: 'bx bx-file-blank' }), 'File'),
            el('button', {
                class: 'btn btn--sm', title: 'New folder',
                onclick: () => newEntry(projectRef, 'dir', loadTree),
            }, el('i', { class: 'bx bx-folder-plus' }), 'Folder'),
            saveBtn),
        el('div', { class: 'code' }, gutter,
			el('div', { class: 'code__wrap' }, highlightLayer, input, emptyEditor)));

    // ---- run ----

    const cmd = el('input', {
		class: 'input input--mono', placeholder: 'python main.py', value: 'python main.py',
    });
    const runBtn = el('button', { class: 'btn btn--primary', onclick: run },
		el('i', { class: 'bx bx-play' }), 'Run');
	const runNetworkLink = el('a', { class: 'btn btn--sm', href: '#/networks' });
	const showRunNetwork = () => {
		const network = state.overview.networks.find(n => n.id === state.overview.active_network);
		runNetworkLink.textContent = network ? `Run on: ${network.name}` : 'Choose run network';
	};
	showRunNetwork();
	const offRunNetwork = on('network.active', showRunNetwork);
	const offRunNetworks = on('networks.changed', showRunNetwork);
    const stopBtn = el('button', { class: 'btn btn--danger hide', onclick: stop },
		el('i', { class: 'bx bx-stop-circle' }), 'Stop');
	let jobPoll = null;
	let currentCommand = '';

    async function run() {
        if (ctx.path.endsWith('.py')) cmd.value = `python '${ctx.path.replaceAll("'", "'\"'\"'")}'`;
        const command = cmd.value.trim();
        if (!command) { toast('Type a command to run.', 'err'); return; }
        if (ctx.dirty) await save();
        mount(outputBox, el('div', { class: 'console__line console__line--meta' },
            `$ ${command}`));
        try {
			currentJob = await api('/api/ray/jobs', {
				method: 'POST', body: { project_id: projectRef, command },
            });
			currentCommand = command;
            runBtn.classList.add('hide');
            stopBtn.classList.remove('hide');
			await updateRayOutput();
			jobPoll = setInterval(updateRayOutput, 2000);
        } catch (err) {
            toast(err.message, 'err');
            outputBox.append(el('div', { class: 'console__line console__line--err' }, err.message));
        }
    }

    async function stop() {
        if (!currentJob) return;
        try {
			await api(`/api/ray/jobs/${encodeURIComponent(currentJob.id)}/stop?network_id=${encodeURIComponent(currentJob.network_id)}`,
				{ method: 'POST' });
        } catch (err) { toast(err.message, 'err'); }
    }

	async function updateRayOutput() {
		if (!currentJob) return;
		try {
			const [logData, listing] = await Promise.all([
				api(`/api/ray/jobs/${encodeURIComponent(currentJob.id)}/logs?network_id=${encodeURIComponent(currentJob.network_id)}`).catch(() => ({ logs: '' })),
				api(`/api/ray/jobs?network_id=${encodeURIComponent(currentJob.network_id)}`).catch(() => ({ jobs: [] })),
			]);
			const job = (listing.jobs || []).find((item) => item.id === currentJob.id);
			const lines = String(logData.logs || '').replace(/\s+$/, '').split('\n');
			mount(outputBox,
				el('div', { class: 'console__line console__line--meta' }, `$ ${currentCommand}`),
				...lines.filter(Boolean).map((line) =>
					el('div', { class: 'console__line' }, line)),
				job ? el('div', { class: 'console__line console__line--meta' },
					`— ${job.status.toLowerCase()} —`) : null);
			outputBox.scrollTop = outputBox.scrollHeight;
			if (job && !['PENDING', 'RUNNING'].includes(job.status)) {
				clearInterval(jobPoll);
				jobPoll = null;
				runBtn.classList.remove('hide');
				stopBtn.classList.add('hide');
				currentJob = null;
				await loadTree();
				await loadGit();
			}
		} catch (err) {
			// A short dashboard restart should not throw away the active run view.
		}
	}

    // ---- repository and team ----

    const repository = repositoryPanel(projectRef, loadTree);
    const team = teamPanel(projectRef);
    const loadGit = repository.reload;

    // ---- assemble ----

	const filePanel = el('div', { class: 'panel file-panel' },
		el('div', { class: 'panel__head' },
			el('span', { class: 'grow' }, 'Files'),
			el('button', {
				id: 'branch-upload', class: 'btn btn--sm', title: 'Upload files',
				onclick: () => uploadInput.click(),
			}, el('i', { class: 'bx bx-upload' }), 'Upload'),
			el('button', {
				class: 'btn btn--sm btn--icon', title: 'Refresh files',
				onclick: loadTree,
			}, el('i', { class: 'bx bx-refresh' }))),
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

	const codePane = el('div', { class: 'ws' },
		el('div', { class: 'ws__side' }, filePanel), editorBox);
	editorBox.append(el('div', { class: 'ray-tool-actions' }, runBtn, stopBtn, runNetworkLink), outputBox);
	const gitPane = el('div', { class: 'panel' },
		el('div', { class: 'panel__head' }, 'Repository and branch history'), repository.node);
	const teamPane = el('div', { class: 'panel' },
		el('div', { class: 'panel__head' }, 'Project team'), team.node);
	const settingsPane = projectSettingsPane(projectSummary);
	const devicesPane = projectDevicesPane(projectSummary, activeTab === 'devices');
	// Reloading the whole page once a merge is settled is right: the working
	// tree has been rewritten underneath every open file, so nothing on screen
	// can be trusted to still match the disk.
	const conflictsPane = conflictPane(name, () => location.reload()).node;
	const branchesPane = buildBranchPane(name).node;
	const raySnapshot = activeTab === 'overview'
		? await api('/api/ray')
			.catch((error) => ({ running: false, detail: error.message })) : null;
	const overviewPane = projectOverview(projectSummary, gitSnapshot, raySnapshot);
	const tools = rayTools(projectSummary, activeTab);
	const panes = { overview: overviewPane, branch: codePane, test: tools.test, preset: tools.preset,
		git: gitPane, 'branch-list': branchesPane, conflicts: conflictsPane, devices: devicesPane,
		team: teamPane, settings: settingsPane };

	const tabs = [
		['overview', 'Overview', 'bx-grid-alt'], ['branch', 'Code', 'bx-code-alt'],
		['test', 'Test', 'bx-check-circle'], ['preset', 'Preset', 'bx-file-blank'], ['git', 'Git', 'bx-git-commit'], ['branch-list', 'Branch', 'bx-git-branch'],
		// A conflict is the one thing here that must be dealt with before
		// anything else works, so the tab announces itself rather than waiting
		// to be found.
		...(gitSnapshot.in_merge ? [['conflicts', 'Resolve conflicts', 'bx-git-compare']] : []),
		['devices', 'Devices', 'bx-devices'],
		['team', 'Team', 'bx-group'], ['settings', 'Settings', 'bx-slider-alt'],
	];
	// bx-grid-alt is included below as an alias to the Boxicons grid glyph.
	    mount(page,
		el('div', { class: 'detail-head' },
			el('div', { class: 'detail-head__copy' },
				el('p', { class: 'page__eyebrow' },
					'Local project'),
				el('h1', { class: 'page__title' }, name),
				el('p', { class: 'page__sub' }, projectSummary.description || 'Local project folder'),
                el('p', { class: 'project-local-path mono muted' }, projectSummary.path || ''),
				el('div', { class: 'detail-head__meta' },
					el('span', { class: 'chip chip--cyan' }, el('i', { class: 'bx bx-git-branch' }), branch),
					el('span', { class: 'chip' }, gitSnapshot.changes.length === 0 ? 'clean' : `${gitSnapshot.changes.length} changes`))),
			el('button', { class: 'btn btn--sm', onclick: () => navigate('projects') },
				el('i', { class: 'bx bx-left-arrow-alt' }), 'All projects')),
		el('nav', { class: 'tabs', 'aria-label': 'Project sections' }, ...tabs.map(([key, label, icon]) =>
			el('a', { class: `tab ${activeTab === key ? 'tab--on' : ''}`,
				href: `#/projects/${encodeURIComponent(projectRef)}/${key}` },
				el('i', { class: `bx ${icon}` }), label))),
		panes[activeTab]);

    mount(outputBox, el('div', { class: 'console__line console__line--meta' },
        'Output appears here when you run something.'));

	const initialTree = await loadTree();
	if (activeTab === 'branch' && ctx.path) {
		await openFile(ctx.path);
	} else if (activeTab === 'branch') {
		const files = flattenFiles(initialTree);
		const first = files.find((entry) => entry.path === 'main.py') ||
			files.find((entry) => entry.path.toLowerCase() === 'readme.md') || files[0];
		if (first) await openFile(first.path);
	}
    paint();

    // ---- live updates ----

	const isThisProject = (event) => event.project_id
		? event.project_id === ctx.projectId : event.project === name;
    const offTree = on('tree.changed', (e) => { if (isThisProject(e)) loadTree(); });
	const offReplace = on('file.replaced', async (event) => {
		if (!isThisProject(event) || event.path !== ctx.path) return;
		if (ctx.dirty) {
			toast(`${event.path} was replaced while you had pending edits. Reload it before continuing.`, 'err');
			return;
		}
		await openFile(ctx.path);
	});
	const offRename = on('entry.renamed', async (event) => {
		if (!isThisProject(event) ||
			(ctx.path !== event.from && !ctx.path.startsWith(`${event.from}/`))) return;
		if (ctx.dirty) {
			toast('An open path was renamed while you had pending edits. Reload this branch.', 'err');
			return;
		}
		await openFile(`${event.to}${ctx.path.slice(event.from.length)}`);
	});
	const offDelete = on('entry.deleted', (event) => {
		if (!isThisProject(event) ||
			(ctx.path !== event.path && !ctx.path.startsWith(`${event.path}/`))) return;
		if (ctx.dirty) {
			toast('An open path was deleted while you had pending edits. Reload this branch.', 'err');
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
		tools.close();
		offRunNetwork(); offRunNetworks();
		devicesPane.close();
		if (jobPoll) clearInterval(jobPoll);
		offTree(); offReplace(); offRename(); offDelete();
		offCollab(); offReject(); offPresence(); offConnection();
        window.removeEventListener('beforeunload', beforeUnload);
    };
}

function projectOverview(project, git, ray) {
	const clean = git.available && git.changes.length === 0;
	const commits = git.log || [];
	return el('div', { class: 'detail-grid' },
		el('div', {},
			el('section', { class: 'ps-metrics', style: 'grid-template-columns:repeat(3,minmax(0,1fr))' },
				projectStatusMetric(clean ? 'ps-metric-card--networks' : 'ps-metric-card--system',
					clean ? 'bx-check-circle' : 'bx-git-commit', clean ? 'Up to date' : 'Changes waiting',
					git.available ? `${git.changes.length} working-tree changes` : 'Git unavailable'),
				projectStatusMetric('ps-metric-card--projects', 'bx-git-branch', git.branch || 'main',
					'The folder and branch are the same working copy'),
				projectStatusMetric(ray?.running ? 'ps-metric-card--gpus' : 'ps-metric-card--system',
					ray?.running ? 'bx-broadcast' : 'bx-error-circle', ray?.running ? 'Ray ready' : 'Ray offline',
					ray?.running ? `${ray.total_gpu || 0} GPU · ${ray.total_cpu || 0} CPU` : ray?.advice || ray?.detail || 'Start Ray from the network')),
			el('section', { class: 'panel' },
				el('div', { class: 'panel__head' },
					el('span', { class: 'grow' }, 'Recent commits'),
					el('a', { class: 'chip chip--cyan', href: `#/projects/${encodeURIComponent(project.id)}/git` }, 'full history')),
				commits.length ? el('div', { class: 'rows' }, ...commits.slice(0, 8).map((entry) =>
					el('div', { class: 'row', style: 'cursor:default' },
						el('i', { class: 'bx bx-git-commit', style: 'color:var(--cyan);font-size:18px' }),
						el('span', { class: 'row__main' }, el('span', { class: 'row__title' }, entry.subject),
							el('span', { class: 'row__meta' }, `${entry.author} · ${ago(entry.when)}`)),
						el('code', { class: 'chip' }, entry.short))))
					: el('div', { class: 'empty' }, el('i', { class: 'bx bx-git-commit empty__ico' }),
						el('span', { class: 'empty__text' }, 'No commits yet.')))),
		el('aside', { class: 'panel' },
			el('div', { class: 'panel__head' }, 'Project'),
			el('span', { class: 'ps-project-mark', style: 'width:56px;height:56px;font-size:23px' },
				el('i', { class: 'bx bx-layer' })),
			el('h2', { style: 'margin:16px 0 6px;font-size:18px' }, project.name),
			el('p', { class: 'muted', style: 'margin:0;line-height:1.6;font-size:12px' },
				project.description || 'Local project folder'),
			el('dl', { class: 'kv', style: 'margin-top:14px' },
				el('dt', {}, 'Branch'), el('dd', {}, git.branch || 'main'),
				el('dt', {}, 'Repository'), el('dd', {}, git.repository || 'Local Git'),
				el('dt', {}, 'Updated'), el('dd', {}, ago(project.updated_at)))));
}

function projectStatusMetric(tone, icon, title, detail) {
	return el('div', { class: `ps-metric-card ${tone}` },
		el('div', { class: 'ps-metric-card__surface', style: 'align-items:center;justify-content:center;text-align:center' },
			el('i', { class: `bx ${icon}`, style: 'font-size:30px;color:var(--tx-2)' }),
			el('strong', { style: 'margin-top:8px;font-size:13px;color:#e2e8f0' }, title),
			el('span', { class: 'mono muted', style: 'margin-top:4px;font-size:9px' }, detail)));
}

// projectDevicesPane answers the question that decides where a job can run:
// which machines have this project's files.
//
// Online and ready are different things, and conflating them is how somebody
// ends up submitting a run to a machine that has nothing to run. Nothing is
// downloaded when you join a network — a laptop should not receive somebody's
// eighteen gigabytes of training data because it was in the room — so the files
// travel when a person decides they should.
function projectDevicesPane(project, active) {
	const box = el('div', {});
	const pane = el('div', {}, box);
	if (!project.network_id) {
		mount(box, el('section', { class: 'panel' },
			el('strong', {}, 'Local project files'),
			el('p', { class: 'muted' }, 'Ray submits this project folder when you run a file. Choose the compute network on Networks. Use Git to share files with another machine.'),
			el('a', { class: 'btn', href: '#/networks' }, 'Choose network')));
		pane.close = () => {};
		return pane;
	}
	let disposed = false;

	const load = async () => {
		if (disposed) return;
		let data;
		try {
			data = await api(`/api/projects/${encodeURIComponent(project.id)}/devices`);
		} catch (err) {
			if (disposed) return;
			mount(box, el('div', { class: 'panel' },
				el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' }, err.message)));
			return;
		}
		if (disposed) return;
		const devices = data.devices || [];
		const ready = devices.filter((device) => device.online && device.has_files).length;
		mount(box,
			el('div', { class: 'panel ps-toolbar' },
				el('span', { class: 'grow' },
					el('strong', { style: 'font-size:13px' }, 'Where this project can run'),
					el('span', { class: 'muted', style: 'display:block;margin-top:3px;font-size:12px' },
						'A machine needs the files before it can run anything. Send them ' +
						'to whichever machines should do the work.')),
				el('span', { class: `chip ${ready ? 'chip--good' : 'chip--warn'}` },
					`${ready} ready`),
				el('button', { class: 'btn btn--sm', onclick: load },
					el('i', { class: 'bx bx-refresh' }), 'Refresh')),
			el('div', { class: 'rows' },
				...devices.map((device) => deviceReadinessRow(project, device, load))));
	};
	const off = active ? watchRefresh(['peers.changed', 'devices.changed', 'project.created',
		'project.deleted', 'tree.changed', 'connection.restored'], load) : () => {};
	if (active) load();
	pane.close = () => { disposed = true; off(); };
	return pane;
}

function deviceReadinessRow(project, device, reload) {
	const transfer = async (direction) => {
		const label = direction === 'send' ? 'Sending' : 'Fetching';
		toast(`${label} ${project.name}…`);
		try {
			const result = await api(
				`/api/projects/${encodeURIComponent(project.id)}/${direction}`,
				{ method: 'POST', body: { node_id: device.node_id } });
			toast(`${project.name} · ${megabytes(Math.round((result.bytes || 0) / 1048576))} transferred.`);
			await reload();
		} catch (err) { toast(err.message, 'err'); }
	};

	// Four states, and each leads somewhere different: ready, needs the files,
	// refuses files, or is not here to receive them.
	let action = null;
	if (device.is_self && !device.has_files) {
		action = el('button', { class: 'btn btn--sm btn--primary', onclick: () => transfer('fetch') },
			el('i', { class: 'bx bx-download' }), 'Get it here');
	} else if (!device.is_self && !device.has_files && device.online && device.accepts_files) {
		action = el('button', { class: 'btn btn--sm', onclick: () => transfer('send') },
			el('i', { class: 'bx bx-upload' }), 'Send files');
	}

	const status = device.has_files
		? el('span', { class: 'chip chip--good' }, el('i', { class: 'bx bx-check' }), ' has files')
		: !device.accepts_files
			? el('span', { class: 'chip' }, 'refuses files')
			: el('span', { class: 'chip chip--warn' }, 'no files');

	return el('div', { class: 'row', style: 'cursor:default;align-items:center' },
		el('span', { class: `dot ${device.online ? 'dot--on' : 'dot--off'}` }),
		el('span', { class: 'row__main' },
			el('span', { class: 'row__title' }, device.name,
				device.is_self ? el('span', { class: 'chip chip--cyan', style: 'margin-left:8px' },
					'this machine') : null),
			el('span', { class: 'row__meta' },
				device.online ? 'online' : `last seen ${ago(device.last_seen)}`)),
		status,
		action);
}

function projectSettingsPane(project) {
	const description = el('textarea', { class: 'textarea', rows: '4', maxlength: '500',
		placeholder: 'What is this project for?' }, project.description || '');
	return el('div', { class: 'detail-grid' },
		el('section', { class: 'panel' },
			el('div', { class: 'panel__head' }, 'Project details'),
			el('label', { class: 'field' }, el('span', { class: 'field__label' }, 'Name'),
				el('input', { class: 'input input--mono', value: project.name, disabled: true })),
			el('label', { class: 'field' }, el('span', { class: 'field__label' }, 'Description'), description),
			el('button', { class: 'btn btn--primary', onclick: async () => {
				const updated = await api(`/api/projects/${encodeURIComponent(project.id)}`, {
					method: 'PUT', body: { description: description.value },
				});
				project.description = updated.description;
				toast('Project details saved.');
			} }, el('i', { class: 'bx bx-save' }), 'Save details')),
		el('aside', {},
			networkPanel(project),
			el('section', { class: 'panel', style: 'margin-top:14px' },
				el('div', { class: 'panel__head' }, 'Delete project'),
				el('p', { class: 'muted', style: 'font-size:12px;line-height:1.6' },
					'Deleting removes the local folder and its complete Git history from this machine.'),
				el('button', { class: 'btn btn--danger', onclick: () => removeProject(project) },
					el('i', { class: 'bx bx-trash' }), 'Delete permanently'))));
}

// Compute selection never moves project files or changes sharing.
function networkPanel(project) {
	const current = (state.overview.networks || [])
		.find((network) => network.id === state.overview.active_network);

	return el('section', { class: 'panel' },
		el('div', { class: 'panel__head' }, 'Run network'),
		el('p', { class: 'muted', style: 'margin:0 0 12px;font-size:12px;line-height:1.6' },
			'Runs use ', el('strong', {}, current ? current.name : 'no network yet'),
			'. Choose the active network on Networks. Project files and Git history stay here.'),
		el('a', { class: 'btn', href: '#/networks' }, 'Choose network'));
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
        class: 'input', placeholder: 'What changed?', value: 'Update current branch',
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

function removeProject(project) {
    const name = project.name;
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
            await api(`/api/projects/${encodeURIComponent(project.id)}`, { method: 'DELETE' });
            close();
            toast(`Deleted ${name}.`);
            navigate('projects');
        },
    });
}
