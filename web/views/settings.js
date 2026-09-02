// Settings — what this machine will allow, and where it keeps things.

import { el, mount } from '../lib/ui.js';
import { api, toast } from '../lib/client.js';

export async function renderSettings(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const settings = await api('/api/settings');
    const worker = { ...settings.worker };

    /** toggle renders one policy switch. */
    function toggle(key, title, description) {
        const button = el('button', {
            class: 'toggle', role: 'switch',
            'aria-checked': String(Boolean(worker[key])),
            'aria-label': title,
            onclick: () => {
                worker[key] = !worker[key];
                button.setAttribute('aria-checked', String(worker[key]));
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
                await api('/api/settings', { method: 'PUT', body: { worker } });
                toast('Settings saved.');
            } catch (err) {
                toast(err.message, 'err');
                saveBtn.disabled = false;
            }
        },
    }, 'Save changes');

    const paths = Object.entries(settings.paths);

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
                toggle('enabled', 'Accept work',
                    'Turn this off and the machine stays in the cluster but runs nothing.'),
                toggle('allow_jobs', 'Scripts and commands',
                    'Run project code submitted from the workspace.'),
                toggle('allow_gpu', 'GPU access',
                    'Let jobs use the accelerators on this machine.'),
                toggle('allow_terminal', 'Terminal access',
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
                        ]))))));

    return null;
}
