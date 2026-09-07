// Settings — what this machine will allow, how it starts, and where it keeps
// things.

import { el, mount, ago } from '../lib/ui.js';
import { api, toast, modal, state, on } from '../lib/client.js';
import { confirmShutdown } from '../lib/statusbar.js';

const TABS = [
    ['general', 'General', 'bx-slider-alt', '#/settings/general'],
    ['devices', 'Devices', 'bx-devices', '#/devices'],
    ['updates', 'Updates', 'bx-refresh', '#/settings/updates'],
    ['about', 'About', 'bx-info-circle', '#/settings/about'],
];

/** renderDevicesPage is the sidebar entry. Devices has its own place in the
 *  navigation because the other machines are the point of the product, but it
 *  keeps the Settings tab bar so it still reads as part of Settings. */
export async function renderDevicesPage(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);
    return renderDevices(page, 'devices');
}

export async function renderSettings(host, args = []) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const requested = args[0];
    const tab = TABS.some(([id]) => id === requested) ? requested : 'general';
    // An older link to the tab still works; the page itself lives at #/devices.
    if (tab === 'devices') return renderDevices(page, tab);

    // The service state is a nice-to-have: a node started from a terminal has
    // no unit, which is a normal answer rather than a reason to fail the page.
    const [settings, updateStatus, service] = await Promise.all([
        api('/api/settings'), api('/api/update'),
        api('/api/service').catch(() => ({ managed: false, detail: '' })),
    ]);
    const worker = { ...settings.worker };
    const update = { ...settings.update };
    const auth = { ...(settings.auth || {}) };

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
                await api('/api/settings', { method: 'PUT', body: { worker, update, auth } });
                toast('Settings saved.');
            } catch (err) {
                toast(err.message, 'err');
                saveBtn.disabled = false;
            }
        },
    }, 'Save changes');

    const paths = Object.entries(settings.paths);

    mount(page,
        settingsHead(tab),

        tab !== 'general' ? null : el('div', { class: 'grid grid--2' },
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
                toggle(worker, 'allow_project_sync', 'Project files',
                    'Let other machines in your networks put a project’s files here, ' +
                    'and read them back. A machine cannot run anything without them.')),

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

        tab !== 'general' ? null : startupPanel(service, settings,
            toggle(auth, 'remember_this_machine',
                'Stay signed in on this machine',
                'The desktop window cannot keep a cookie, so without this you are ' +
                'signed out every time you close it. Turn it off on a computer other ' +
                'people can log in to.')),
        tab !== 'general' ? null : el('div', {
            style: 'display:flex;justify-content:flex-end;margin-top:14px',
        }, saveBtn),
        tab !== 'updates' ? null : updatePanel(update, updateStatus, toggle),
        tab !== 'updates' ? null : el('div', {
            style: 'display:flex;justify-content:flex-end;margin-top:14px',
        }, saveBtn),
        tab !== 'about' ? null : aboutPanel(settings, service));

    return null;
}

function settingsHead(active) {
    return el('div', {},
        el('div', { class: 'page__head' },
            el('p', { class: 'page__eyebrow' }, 'Settings'),
            el('h1', { class: 'page__title' }, 'This machine'),
            el('p', { class: 'page__sub' },
                'These limits are enforced here, by this machine. Nothing in the ' +
                'cluster can widen them — which is what makes it reasonable to lend ' +
                'someone else your computer.')),
        el('nav', { class: 'tabs', 'aria-label': 'Settings sections' },
            ...TABS.map(([id, label, icon, href]) => el('a', {
                class: `tab ${active === id ? 'tab--on' : ''}`, href,
            }, el('i', { class: `bx ${icon}` }), label))));
}

function aboutPanel(settings, service) {
    return el('div', { class: 'grid grid--2' },
        el('div', { class: 'panel' },
            el('div', { class: 'panel__head' }, 'Plainshow Cluster'),
            el('p', { class: 'muted', style: 'margin:0 0 14px;font-size:12.5px;line-height:1.7' },
                'An interface over programs that already work: Tailscale makes the ' +
                'machines reachable, git moves the code, Ray runs the work. ' +
                'Plainshow sets those up and shows what is happening.'),
            el('dl', { class: 'kv' },
                el('dt', {}, 'Version'), el('dd', {}, settings.version),
                el('dt', {}, 'Node'), el('dd', {}, settings.node.name),
                el('dt', {}, 'Node id'), el('dd', {}, settings.node.id),
                el('dt', {}, 'Service'), el('dd', {}, service.unit || 'started by hand'))),
        el('div', { class: 'panel' },
            el('div', { class: 'panel__head' }, 'Where things are kept'),
            el('dl', { class: 'kv' },
                el('dt', {}, 'root'), el('dd', {}, settings.root),
                el('dt', {}, 'config'), el('dd', {}, settings.paths.config))));
}

/** startupPanel controls whether this machine works for the cluster
 *  unattended, and lets you stop it entirely from here. */
function startupPanel(service, settings, rememberSwitch) {
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
        rememberSwitch,
        // Once the interface answers on the network, "local" stops meaning "the
        // person at the keyboard", so the node refuses to honour it and the
        // switch has to say so rather than looking simply ignored.
        settings.network && settings.network.bind &&
        !['', 'localhost', '127.0.0.1', '::1'].includes(settings.network.bind)
            ? el('p', { class: 'muted', style: 'margin:8px 0 0;font-size:12px;color:var(--warn)' },
                `This node listens on ${settings.network.bind}, so staying signed in ` +
                'is refused: anyone who can reach it is no longer necessarily you.')
            : null,
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

// ---------------------------------------------------------------- devices --

/** renderDevices lists every machine on the account, not only this one.
 *
 * A device row is the only place where "which network is this machine working
 * in" is a thing you can change from somewhere else. Nothing here reaches into
 * a machine: each action records a request that the machine reads on its next
 * check-in and carries out itself. */
async function renderDevices(page, tab) {
    const draw = async () => {
        let data;
        try {
            data = await api('/api/devices');
        } catch (err) {
            mount(page, settingsHead(tab), el('div', { class: 'panel empty' },
                el('i', { class: 'bx bx-devices empty__ico' }),
                el('strong', {}, 'Devices are listed by your Plainshow account.'),
                el('span', { class: 'empty__text' }, err.message)));
            return;
        }
        const networks = (state.overview && state.overview.networks) || [];
        const devices = data.devices || [];
        const online = devices.filter((device) => device.online).length;

        mount(page, settingsHead(tab),
            el('div', { class: 'panel ps-toolbar' },
                el('span', { class: 'grow' },
                    el('strong', { style: 'font-size:13px' }, 'Your machines'),
                    el('span', {
                        class: 'muted', style: 'display:block;margin-top:3px;font-size:12px',
                    }, 'Every computer signed in to your account. Changes are picked ' +
                       'up by a machine on its next check-in, within about a minute.')),
                el('span', { class: 'chip chip--good' }, `${online} online`),
                el('span', { class: 'chip' }, `${devices.length} total`),
                el('button', { class: 'btn btn--sm', onclick: draw },
                    el('i', { class: 'bx bx-refresh' }), 'Refresh')),
            devices.length
                ? el('div', { class: 'rows' },
                    ...devices.map((device) => deviceRow(device, networks, data.this_device, draw)))
                : el('div', { class: 'panel empty' },
                    el('i', { class: 'bx bx-devices empty__ico' }),
                    el('strong', {}, 'No devices have checked in yet.')));
    };
    await draw();
    // Another machine can move or sign out a device while this page is open.
    return on('devices.changed', draw);
}

function deviceRow(device, networks, thisDevice, reload) {
    const isThis = device.id === thisDevice;
    const current = device.desired_network || device.active_network || '';
    const picker = el('select', {
        class: 'select', 'aria-label': `Compute network for ${device.name}`,
        title: 'The one Ray cluster receiving this machine’s CPU and GPUs',
        onchange: async () => {
            picker.disabled = true;
            try {
                await api(`/api/devices/${encodeURIComponent(device.id)}/network`,
                    { method: 'PUT', body: { network_id: picker.value } });
                toast(device.online
                    ? `${device.name} is moving to that network.`
                    : `${device.name} will move when it comes back online.`);
                await reload();
            } catch (err) {
                toast(err.message, 'err');
                picker.disabled = false;
            }
        },
    },
        el('option', { value: '', selected: current === '' }, 'No network'),
        ...networks.map((network) => el('option', {
            value: network.id, selected: network.id === current,
        }, network.name)));

    return el('div', { class: 'row', style: 'cursor:default;align-items:center' },
        el('span', { class: `dot ${device.online ? 'dot--on' : 'dot--off'}` }),
        el('span', { class: 'row__main' },
            el('span', { class: 'row__title' }, device.name,
                isThis ? el('span', { class: 'chip chip--cyan', style: 'margin-left:8px' },
                    'this machine') : null,
                device.desired_network && device.desired_network !== device.active_network
                    ? el('span', { class: 'chip chip--warn', style: 'margin-left:8px' }, 'moving')
                    : null),
            el('span', { class: 'row__meta' },
                [
                    `${device.os || 'linux'} · ${device.arch || '?'}`,
                    `${device.gpu_count} GPU${device.gpu_count === 1 ? '' : 's'}`,
                    device.version ? `v${device.version}` : null,
                    device.online ? 'online' : `last seen ${ago(device.last_seen)}`,
                ].filter(Boolean).join(' · '))),
        picker,
        el('button', {
            class: 'btn btn--sm', title: 'Sign this device out of your account',
            onclick: () => signOutDevice(device, isThis, reload),
        }, el('i', { class: 'bx bx-log-out' })),
        el('button', {
            class: 'btn btn--sm btn--icon', title: 'Remove this device',
            onclick: () => removeDevice(device, reload),
        }, el('i', { class: 'bx bx-trash' })));
}

function signOutDevice(device, isThis, reload) {
    modal({
        title: `Sign out ${device.name}?`,
        confirmLabel: 'Sign out',
        danger: true,
        body: () => el('p', { style: 'margin:0;font-size:13px;color:#cbd5e1;line-height:1.6' },
            isThis
                ? 'This is the machine you are using. You will be asked to sign in again.'
                : 'That machine forgets its account credential and closes any browser ' +
                  'signed in on it. Its projects, networks and settings are untouched, ' +
                  'and it stays offline to the cluster until somebody signs in there.'),
        onConfirm: async (close) => {
            await api(`/api/devices/${encodeURIComponent(device.id)}/sign-out`,
                { method: 'POST', body: {} });
            close();
            if (isThis) { location.reload(); return; }
            toast(`${device.name} will sign out on its next check-in.`);
            await reload();
        },
    });
}

function removeDevice(device, reload) {
    modal({
        title: `Remove ${device.name}?`,
        confirmLabel: 'Remove',
        danger: true,
        body: () => el('div', {},
            el('p', { style: 'margin:0 0 12px;font-size:13px;color:#cbd5e1;line-height:1.6' },
                'It disappears from this list and from your account’s totals.'),
            // Saying this plainly matters: somebody removing a lost laptop is
            // trying to revoke it, and removal alone does not do that.
            el('p', { class: 'muted', style: 'margin:0;font-size:12px;line-height:1.6' },
                'This is bookkeeping, not revocation. A machine that still holds a ' +
                'valid credential registers itself again the next time it checks in. ' +
                'Sign it out first if that is what you mean.')),
        onConfirm: async (close) => {
            await api(`/api/devices/${encodeURIComponent(device.id)}`, { method: 'DELETE' });
            close();
            toast(`${device.name} was removed.`);
            await reload();
        },
    });
}
