// Resolving a merge — the Plainshow console's screen, on a node.
//
// Conflict markers are a poor interface. They look like code, they compile in
// some languages, and a file committed with them still in it appears merged and
// is wrong. So both versions are shown side by side, the result underneath, and
// three buttons settle a file without anybody reading a diff.

import { el, mount } from '../lib/ui.js';
import { api, toast } from '../lib/client.js';

/** conflictPane builds the Resolve tab for a project. */
export function conflictPane(project, onDone) {
    const box = el('div', {});
    let content = '';
    let activePath = '';

    const load = async (wanted) => {
        let data;
        try {
            data = await api(`/api/projects/${encodeURIComponent(project)}/conflicts` +
                (wanted ? `?path=${encodeURIComponent(wanted)}` : ''));
        } catch (err) {
            mount(box, el('div', { class: 'panel' },
                el('p', { class: 'muted', style: 'margin:0;font-size:12.5px' }, err.message)));
            return;
        }
        if (!data.in_merge) {
            mount(box, settled(project, onDone, false));
            return;
        }
        const conflicts = data.conflicts || [];
        if (!conflicts.length) {
            mount(box, settled(project, onDone, true));
            return;
        }
        activePath = (data.file && data.file.path) || conflicts[0];
        content = (data.file && data.file.content) || '';
        draw(data, conflicts);
    };

    const draw = (data, conflicts) => {
        const sides = conflictSides(content);
        const editor = el('textarea', {
            class: 'textarea input--mono', rows: '16', spellcheck: 'false',
            oninput: (event) => { content = event.target.value; },
        }, content);

        const settle = async (choice) => {
            try {
                await api(`/api/projects/${encodeURIComponent(project)}/conflicts/resolve`,
                    { method: 'POST', body: { path: activePath, choice } });
                toast(`${activePath} resolved.`);
                await load();
            } catch (err) { toast(err.message, 'err'); }
        };

        mount(box,
            el('div', { class: 'panel', style: 'margin-bottom:14px' },
                el('div', { class: 'panel__head' },
                    el('span', { class: 'ps-project-mark', style: 'width:32px;height:32px;font-size:15px' },
                        el('i', { class: 'bx bx-git-compare' })),
                    el('span', { class: 'grow', style: 'margin-left:10px' },
                        el('strong', { style: 'display:block;font-size:13px' }, 'Resolve merge'),
                        el('span', { class: 'mono', style: 'font-size:10px;color:#fda4af' },
                            `${conflicts.length} conflicted file${conflicts.length === 1 ? '' : 's'}`)),
                    el('button', {
                        class: 'btn btn--sm btn--danger',
                        onclick: () => abandon(project, onDone),
                    }, el('i', { class: 'bx bx-x' }), 'Abandon merge')),

                conflicts.length > 1
                    ? el('div', { class: 'conflict-files' }, ...conflicts.map((file) =>
                        el('button', {
                            class: `conflict-file ${file === activePath ? 'conflict-file--on' : ''}`,
                            title: file, onclick: () => load(file),
                        }, file.split('/').pop())))
                    : null,

                el('div', { class: 'conflict-path' },
                    el('code', {}, activePath),
                    el('span', {}, 'Review both versions')),

                el('div', { class: 'ps-conflict-grid' },
                    conflictSide(`Local · ${data.branch || 'this branch'}`, sides.local, 'local',
                        'what this machine had'),
                    conflictSide('Incoming', sides.incoming, 'incoming',
                        'what arrived with the merge')),

                el('p', { class: 'field__label', style: 'margin:18px 0 6px' }, 'Resolved result'),
                editor,

                el('div', { class: 'conflict-actions' },
                    choiceButton('bx-code-alt', 'Keep local', 'local', () => settle('mine')),
                    choiceButton('bx-git-branch', 'Keep incoming', 'incoming', () => settle('theirs')),
                    choiceButton('bx-trash', 'Delete file', 'drop', () => settle('drop'))),

                el('button', {
                    class: 'btn btn--primary', style: 'width:100%;margin-top:10px;justify-content:center',
                    onclick: async () => {
                        try {
                            await api(`/api/projects/${encodeURIComponent(project)}/conflicts/mark`,
                                { method: 'POST', body: { path: activePath, content } });
                            toast(`${activePath} resolved.`);
                            await load();
                        } catch (err) { toast(err.message, 'err'); }
                    },
                }, el('i', { class: 'bx bx-check' }), 'Save resolved file')));
    };

    load();
    return { node: box, reload: load };
}

// conflictSides splits a file carrying markers into the two versions it holds,
// padding each so the same line number means the same place on both sides.
export function conflictSides(content) {
    const local = [];
    const incoming = [];
    let side = 'both';
    for (const line of String(content).split('\n')) {
        if (line.startsWith('<<<<<<<')) { side = 'local'; continue; }
        if (line.startsWith('=======') && side === 'local') { side = 'incoming'; continue; }
        if (line.startsWith('>>>>>>>')) { side = 'both'; continue; }
        if (side === 'local') {
            local.push({ text: line, conflict: true });
            incoming.push({ text: '', conflict: true });
        } else if (side === 'incoming') {
            local.push({ text: '', conflict: true });
            incoming.push({ text: line, conflict: true });
        } else {
            local.push({ text: line, conflict: false });
            incoming.push({ text: line, conflict: false });
        }
    }
    return { local, incoming };
}

function conflictSide(label, lines, tone, hint) {
    return el('section', { class: `ps-conflict-side ps-conflict-side--${tone}` },
        el('div', { class: 'ps-conflict-side__header' },
            el('i', { class: `bx ${tone === 'local' ? 'bx-code-alt' : 'bx-git-branch'}` }),
            el('span', {}, label),
            el('span', { class: 'ps-conflict-side__hint' }, hint)),
        el('pre', { class: 'ps-conflict-side__code' }, ...lines.map((line, index) =>
            el('div', { class: `ps-conflict-line ${line.conflict ? 'ps-conflict-line--active' : ''}` },
                el('span', { class: 'ps-conflict-line__number' }, String(index + 1)),
                el('code', {}, line.text || ' ')))));
}

function choiceButton(icon, label, tone, onclick) {
    return el('button', { class: `conflict-choice conflict-choice--${tone}`, onclick },
        el('span', { class: 'conflict-choice__in' },
            el('i', { class: `bx ${icon}` }), el('span', {}, label)));
}

function settled(project, onDone, wasMerging) {
    return el('div', { class: 'panel' },
        el('div', { class: 'empty' },
            el('i', { class: 'bx bx-check-circle empty__ico', style: 'color:var(--good)' }),
            el('strong', {}, wasMerging ? 'Every file is resolved.' : 'There is no merge in progress.'),
            el('span', { class: 'empty__text' }, wasMerging
                ? 'Complete the merge to commit the combined branch, then push it.'
                : 'This tab appears when a pull brings in changes that clash with yours.')),
        wasMerging
            ? el('button', {
                class: 'btn btn--primary', style: 'width:100%;justify-content:center',
                onclick: async () => {
                    try {
                        await api(`/api/projects/${encodeURIComponent(project)}/merge/finish`,
                            { method: 'POST', body: {} });
                        toast('Merge completed.');
                        if (onDone) onDone();
                    } catch (err) { toast(err.message, 'err'); }
                },
            }, el('i', { class: 'bx bx-git-merge' }), 'Complete merge')
            : null);
}

function abandon(project, onDone) {
    api(`/api/projects/${encodeURIComponent(project)}/merge/abort`, { method: 'POST', body: {} })
        .then(() => { toast('Merge abandoned. Your work is back as it was.'); if (onDone) onDone(); })
        .catch((err) => toast(err.message, 'err'));
}
