// Settings — what this machine will allow, how it starts, and where it keeps
// things.

import { el, mount } from '../lib/ui.js';
import { api, toast } from '../lib/client.js';
import { confirmShutdown } from '../lib/statusbar.js';

export async function renderSettings(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    // The service state is a nice-to-have: a node started from a terminal has
    // no unit, which is a normal answer rather than a reason to fail the page.
    const [settings, updateStatus, service] = await Promise.all([
        api('/api/settings'), api('/api/update'),
        api('/api/service').catch(() => ({ managed: false, detail: '' })),
    ]);
    const worker = { ...settings.worker };
    const update = { ...settings.update };

    /** toggle renders one policy switch that is saved with the Save button. */
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
                    'Run project code submitted from the branch editor.'),
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

        startupPanel(service),
        updatePanel(update, updateStatus, toggle));

    return null;
}

/** startupPanel controls whether this machine works for the cluster
 *  unattended, and lets you stop it entirely from here. */
function startupPanel(service) {
    const detail = el('p', { class: 'muted', style: 'margin:10px 0 0;font-size:12px' },
        service.detail || '');
    const boot = el('button', {
        class: 'toggle', role: 'switch',
        'aria-checked': String(Boolean(service.boot_enabled)),
        'aria-label': 'Start with the machine',
        disabled: !service.can_change,
        onclick: async () => {
            const next = boot.getAttribute('aria-checked') !== 'true';
            boot.disabled = true;
            try {
                const state = await api('/api/service/boot', {
                    method: 'PUT', body: { enabled: next },
                });
                boot.setAttribute('aria-checked', String(Boolean(state.boot_enabled)));
                detail.textContent = state.detail || '';
                toast(state.boot_enabled
                    ? 'This node will start with the machine.'
                    : 'This node will no longer start with the machine.');
            } catch (err) {
                toast(err.message, 'err');
            }
            boot.disabled = false;
        },
    });

    return el('div', { class: 'panel', style: 'margin-top:14px' },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'Startup and background service'),
            service.unit
                ? el('span', { class: 'mono dim', style: 'font-size:10px' }, service.unit)
                : null),
        el('div', { class: 'switch' },
            el('div', { class: 'switch__text' },
                el('strong', {}, 'Start with the machine'),
                el('span', {}, 'Join the network and be available for work after a ' +
                    'reboot, without anyone signing in.')),
            boot),
        detail,
        el('div', {
            style: 'display:flex;align-items:center;gap:12px;margin-top:18px;' +
                   'padding-top:16px;border-top:1px solid var(--line)',
        },
            el('div', { style: 'flex:1' },
                el('strong', { style: 'display:block;font-size:13px' }, 'Stop everything now'),
                el('span', { class: 'muted', style: 'font-size:12px' },
                    'Stops running jobs, leaves the Ray cluster and shuts the node ' +
                    'service down. Nothing of Plainshow keeps running here.')),
            el('button', { class: 'btn btn--danger', onclick: confirmShutdown },
                el('i', { class: 'bx bx-power-off' }), 'Stop')));
}

/** updatePanel is one line about the version, and everything that configures
 *  how updates are found folded away behind it. Somebody opening Settings
 *  wants to know whether they are current, not to review a release channel. */
function updatePanel(update, initial, toggle) {
    const line = el('div', {});

    const draw = (status) => {
        mount(line,
            el('div', { style: 'display:flex;align-items:center;gap:10px;flex-wrap:wrap' },
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
                            draw(await api('/api/update/check', { method: 'POST' }));
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
                : null);
    };
    draw(initial);

    const text = (key, label, placeholder) => {
        const input = el('input', {
            class: 'input input--mono', value: update[key], placeholder,
            oninput: (event) => { update[key] = event.target.value.trim(); },
        });
        return el('div', { class: 'field' },
            el('label', { class: 'field__label' }, label), input);
    };
    const channel = el('select', {
        class: 'input',
        onchange: (event) => { update.channel = event.target.value; },
    }, ...['stable', 'beta', 'any'].map((name) =>
        el('option', { value: name, selected: update.channel === name }, name)));

    return el('div', { class: 'panel', style: 'margin-top:14px' },
        el('div', { class: 'panel__head' }, 'Updates'),
        line,
        el('details', { class: 'fold' },
            el('summary', {}, 'Where updates come from'),
            el('p', { class: 'muted', style: 'margin:12px 0 14px;font-size:12.5px' },
                'Releases come from GitHub and must carry a SHA-256 checksum. ' +
                'Automatic installation is off by default so a training run is ' +
                'never interrupted.'),
            el('div', { class: 'grid grid--3', style: 'gap:10px' },
                text('repository', 'Repository', 'owner/repository'),
                el('div', { class: 'field' },
                    el('label', { class: 'field__label' }, 'Channel'), channel),
                text('check_every', 'Check every', '6h')),
            toggle(update, 'enabled', 'Check for updates',
                'Periodically look for a compatible release on GitHub.'),
            toggle(update, 'automatic', 'Install automatically',
                'Apply a verified update and restart this node when one is found.'),
            el('p', { class: 'muted', style: 'margin:10px 0 0;font-size:11.5px' },
                'Saved with Save changes above.')));
}
