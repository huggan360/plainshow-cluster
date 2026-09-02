// Settings — what this machine will allow, and where it keeps things.

import { el, mount } from '../lib/ui.js';
import { api, toast } from '../lib/client.js';

export async function renderSettings(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const [settings, initialUpdateStatus] = await Promise.all([
        api('/api/settings'), api('/api/update'),
    ]);
    const worker = { ...settings.worker };
    const update = { ...settings.update };

    /** toggle renders one policy switch. */
    function toggle(target, key, title, description) {
        const button = el('button', {
            class: 'toggle', role: 'switch',
            'aria-checked': String(Boolean(target[key])),
            'aria-label': title,
            onclick: () => {
                target[key] = !target[key];
                button.setAttribute('aria-checked', String(target[key]));
                saveBtn.disabled = false;
            },
        });
        return el('div', { class: 'switch' },
            el('div', { class: 'switch__text' },
                el('strong', {}, title), el('span', {}, description)),
            button);
    }

    const saveBtn = el('button', {
        class: 'btn btn--primary', disabled: true,
        onclick: async () => {
            saveBtn.disabled = true;
            try {
                await api('/api/settings', { method: 'PUT', body: { worker, update } });
                toast('Settings saved.');
            } catch (err) {
                toast(err.message, 'err');
                saveBtn.disabled = false;
            }
        },
    }, 'Save changes');

    const paths = Object.entries(settings.paths);
    const repository = el('input', {
        class: 'input input--mono', value: update.repository,
        placeholder: 'owner/repository',
        oninput: (event) => {
            update.repository = event.target.value.trim();
            saveBtn.disabled = false;
        },
    });
    const interval = el('input', {
        class: 'input input--mono', value: update.check_every,
        placeholder: '6h',
        oninput: (event) => {
            update.check_every = event.target.value.trim();
            saveBtn.disabled = false;
        },
    });
    const channel = el('select', {
        class: 'input',
        onchange: (event) => {
            update.channel = event.target.value;
            saveBtn.disabled = false;
        },
    }, ...['stable', 'beta', 'any'].map((name) =>
        el('option', { value: name, selected: update.channel === name }, name)));

    const updateBox = el('div', {});
    const drawUpdate = (status) => {
        const release = status.release;
        mount(updateBox,
            el('div', { style: 'display:flex;align-items:center;gap:8px;flex-wrap:wrap' },
                el('span', { class: `chip ${status.available ? 'chip--warn' : 'chip--good'}` },
                    status.available ? 'update available' : 'up to date'),
                el('span', { class: 'mono muted', style: 'font-size:11px' },
                    `${status.current}${status.latest ? ` → ${status.latest}` : ''}`),
                el('span', { style: 'flex:1' }),
                el('button', {
                    class: 'btn btn--sm', disabled: status.checking,
                    onclick: async () => {
                        try {
                            toast('Checking for a release…');
                            drawUpdate(await api('/api/update/check', { method: 'POST' }));
                        } catch (err) { toast(err.message, 'err'); }
                    },
                }, status.checking ? 'Checking…' : 'Check now'),
                status.available ? el('button', {
                    class: 'btn btn--sm btn--primary',
                    onclick: async () => {
                        try {
                            toast('Downloading and verifying the update…');
                            await api('/api/update/apply', { method: 'POST' });
                            toast('Installed. This node is restarting.');
                        } catch (err) { toast(err.message, 'err'); }
                    },
                }, 'Install') : null),
            status.error
                ? el('p', { style: 'color:#fb7185;margin:10px 0 0;font-size:12px' }, status.error)
                : null,
            release && release.notes
                ? el('p', { class: 'muted', style: 'margin:10px 0 0;font-size:12px' },
                    release.notes.slice(0, 360))
                : null);
    };
    drawUpdate(initialUpdateStatus);

    mount(page,
        el('div', { class: 'page__head' },
            el('p', { class: 'page__eyebrow' }, 'Settings'),
            el('h1', { class: 'page__title' }, 'This machine'),
            el('p', { class: 'page__sub' },
                'These limits are enforced here, by this machine. Nothing in the ' +
                'cluster can widen them — which is what makes it reasonable to lend ' +
                'someone else your computer.')),

        el('div', { class: 'grid grid--2' },
            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'What this machine allows'),
                toggle(worker, 'enabled', 'Accept work',
                    'Turn this off and the machine stays in the cluster but runs nothing.'),
                toggle(worker, 'allow_jobs', 'Scripts and commands',
                    'Run project code submitted from the workspace.'),
                toggle(worker, 'allow_gpu', 'GPU access',
                    'Let jobs use the accelerators on this machine.'),
                toggle(worker, 'allow_terminal', 'Terminal access',
                    'An interactive shell. Off by default, and worth keeping that way.'),
                el('div', { style: 'display:flex;justify-content:flex-end;margin-top:16px' },
                    saveBtn)),

            el('div', {},
                el('div', { class: 'panel', style: 'margin-bottom:14px' },
                    el('div', { class: 'panel__head' }, 'Identity'),
                    el('dl', { class: 'kv' },
                        el('dt', {}, 'Cluster'), el('dd', {}, settings.cluster.name),
                        el('dt', {}, 'Cluster id'), el('dd', {}, settings.cluster.id),
                        el('dt', {}, 'Node'), el('dd', {}, settings.node.name),
                        el('dt', {}, 'Node id'), el('dd', {}, settings.node.id),
                        el('dt', {}, 'Roles'), el('dd', {}, settings.node.roles.join(', ')),
                        el('dt', {}, 'Listen'), el('dd', {},
                            `${settings.network.bind}:${settings.network.port}`),
                        el('dt', {}, 'Version'), el('dd', {}, settings.version))),

                el('div', { class: 'panel' },
                    el('div', { class: 'panel__head' }, 'Where things are kept'),
                    el('p', { class: 'muted', style: 'margin:0 0 12px;font-size:12.5px' },
                        'Everything this node stores is inside one directory. Move that ' +
                        'directory and you have moved the node.'),
                    el('dl', { class: 'kv' },
                        el('dt', {}, 'root'), el('dd', {}, settings.root),
                        ...paths.flatMap(([k, v]) => [
                            el('dt', {}, k),
                            el('dd', {}, v.replace(settings.root, '')),
                        ]))))),

        el('div', { class: 'panel', style: 'margin-top:14px' },
            el('div', { class: 'panel__head' }, 'Updates'),
            el('p', { class: 'muted', style: 'margin:0 0 14px;font-size:12.5px' },
                'Releases come from GitHub and must include a SHA-256 checksum. Automatic ' +
                'installation is off by default so a training run is never interrupted.'),
            updateBox,
            el('div', { class: 'grid grid--3', style: 'margin-top:16px;gap:10px' },
                el('div', { class: 'field' },
                    el('label', { class: 'field__label' }, 'Repository'), repository),
                el('div', { class: 'field' },
                    el('label', { class: 'field__label' }, 'Channel'), channel),
                el('div', { class: 'field' },
                    el('label', { class: 'field__label' }, 'Check every'), interval)),
            toggle(update, 'enabled', 'Check for updates',
                'Periodically look for a compatible release on GitHub.'),
            toggle(update, 'automatic', 'Install automatically',
                'Apply a verified update and restart this node when one is found.')),

        el('div', { style: 'display:flex;justify-content:flex-end;margin-top:14px' }, saveBtn));

    return null;
}
