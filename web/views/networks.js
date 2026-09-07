// Networks — Plainshow-style network cards and one focused network workspace.

import { el, mount, initials, megabytes, ago } from '../lib/ui.js';
import { api, modal, toast, navigate, state, on } from '../lib/client.js';

export async function renderNetworks(host, args = []) {
    if (args.length) return renderNetwork(host, args[0], args[1] || 'connected');
    return renderNetworkList(host);
}

async function renderNetworkList(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);
    const [data, invites] = await Promise.all([
        api('/api/networks'),
        // A node with no account authority has nowhere to ask, which is a
        // normal answer for a standalone machine rather than a page failure.
        api('/api/invitations').then((result) => result.invitations || []).catch(() => []),
    ]);
    const networks = [...(data.networks || [])].sort((a, b) =>
        (a.name || '').localeCompare(b.name || ''));
    let query = '';
    const count = el('span', { class: 'chip' });
    const results = el('div');
    const search = el('input', { class: 'input', placeholder: 'Search networks…' });
    const drawResults = () => {
        const visible = networks.filter((network) =>
            (network.name || '').toLowerCase().includes(query));
        count.textContent = `${visible.length} visible`;
        mount(results, visible.length
                ? el('div', { class: 'ps-card-grid' }, ...visible.map(networkCard))
                : emptyNetworkList(networks.length > 0));
    };
    search.addEventListener('input', () => { query = search.value.toLowerCase(); drawResults(); });
    mount(page,
        el('div', { class: 'detail-head' },
            el('div', { class: 'detail-head__copy' },
                el('h1', { class: 'page__title' }, 'Networks'),
                el('p', { class: 'page__sub' },
                    'Your networks are available together. Projects choose where work runs; ' +
                    'each machine controls which network receives its compute resources.')),
            el('button', { class: 'btn', onclick: joinNetwork },
                el('i', { class: 'bx bx-link' }), 'Join by code'),
            el('button', { class: 'btn btn--primary', onclick: createNetwork },
                el('i', { class: 'bx bx-plus' }), 'New network')),
        invitationsPanel(invites),
        el('div', { class: 'panel ps-toolbar' },
            el('label', { class: 'ps-search' }, el('i', { class: 'bx bx-search' }), search), count),
        results);
    drawResults();
    // An invitation is the one thing on this page that arrives while you are
    // looking at it, so it should not wait for a reload.
    return on('invitations.changed', () => renderNetworkList(host));
}

// invitationsPanel is the first thing on the page when somebody has been
// invited, because an invitation is the one item here that is waiting on you.
function invitationsPanel(invites) {
    if (!invites.length) return null;
    return el('section', { class: 'panel', style: 'margin-bottom:20px' },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'Invitations'),
            el('span', { class: 'chip chip--warn' }, String(invites.length))),
        el('div', { class: 'rows' }, ...invites.map(invitationRow)));
}

function invitationRow(invite) {
    const respond = async (accept) => {
        try {
            const result = await api(
                `/api/invitations/${encodeURIComponent(invite.id)}/${accept ? 'accept' : 'decline'}`,
                { method: 'POST', body: {} });
            toast(accept
                ? `You joined ${invite.network_name}.${
                    result && result.adopted ? '' : ' It will appear shortly.'}`
                : `Declined ${invite.network_name}.`);
            location.reload();
        } catch (error) { toast(error.message, 'err'); }
    };
    const from = invite.invited_by_name || 'someone';
    return el('div', { class: 'row', style: 'cursor:default;align-items:center' },
        el('span', { class: 'ps-project-mark ps-network-mark', style: 'width:32px;height:32px;font-size:14px' },
            el('i', { class: 'bx bx-envelope' })),
        el('span', { class: 'row__main' },
            el('span', { class: 'row__title' }, invite.network_name),
            el('span', { class: 'row__meta' },
                `${from} invited you as ${invite.role} · ${ago(invite.created_at)}`)),
        el('button', { class: 'btn btn--sm', onclick: () => respond(false) }, 'Decline'),
        el('button', { class: 'btn btn--sm btn--primary', onclick: () => respond(true) },
            el('i', { class: 'bx bx-check' }), 'Accept'));
}

function networkCard(network) {
    const open = () => navigate(`networks/${encodeURIComponent(network.id)}`);
    return el('div', {
        class: 'panel ps-project-card',
        role: 'link', tabindex: '0', onclick: open,
        onkeydown: (event) => {
            if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); open(); }
        },
    },
        el('div', { class: 'ps-project-card__top' },
            el('span', { class: 'ps-project-mark ps-network-mark' }, el('i', { class: 'bx bx-network-chart' })),
            el('span', { class: 'ps-project-card__copy' },
                el('strong', {}, network.name),
                el('span', {}, network.role || 'member')),
            el('span', { class: 'ps-project-card__status' },
                el('span', { class: `ps-status-dot ${network.enabled ? 'ps-status-dot--online' : 'ps-status-dot--offline'}` }),
                network.enabled ? 'Ready' : 'Paused')),
        el('div', { class: 'ps-project-card__foot' },
            el('span', {}, el('i', { class: 'bx bx-devices' }), ` ${network.node_count} devices`),
            el('span', {}, el('i', { class: 'bx bx-chip' }), ` ${network.gpu_count} GPUs`),
            el('span', { class: 'push' }, el('i', { class: 'bx bx-layer' }), ` ${network.project_count}`)),
        el('div', { class: 'ps-card-actions' },
            el('span', { class: 'push' }),
            el('button', {
                class: 'btn btn--sm',
                onclick: (event) => { event.stopPropagation(); open(); },
            }, 'Open', el('i', { class: 'bx bx-right-arrow-alt' }))));
}

async function renderNetwork(host, id, requestedTab) {
    const page = el('div', { class: 'page' });
    mount(host, page);
    const data = await api(`/api/networks/${encodeURIComponent(id)}`);
    const tabs = new Set(['connected', 'settings', 'machine']);
    const activeTab = tabs.has(requestedTab) ? requestedTab : 'connected';
    const network = data.network;

    mount(page,
        el('div', { class: 'detail-head' },
            el('div', { class: 'detail-head__copy' },
                el('p', { class: 'page__eyebrow' }, 'Network'),
                el('h1', { class: 'page__title' }, network.name),
                el('div', { class: 'detail-head__meta' },
                    el('span', { class: 'chip chip--cyan' }, network.role || data.membership.account_role),
                    el('span', { class: `chip ${network.enabled ? 'chip--good' : 'chip--warn'}` },
                        network.enabled ? 'participating' : 'paused'),
                    el('span', { class: 'mono dim', style: 'font-size:10px' }, network.id))),
            el('button', { class: 'btn', onclick: () => navigate('networks') },
                el('i', { class: 'bx bx-left-arrow-alt' }), 'All networks')),
        tabBar(id, activeTab),
        activeTab === 'connected' ? await connectedTab(data) : null,
        activeTab === 'settings' ? settingsTab(data) : null,
        activeTab === 'machine' ? machineTab(data) : null);
    return null;
}

function tabBar(id, active) {
    const tabs = [
        ['connected', 'Connected devices', 'bx-devices'],
        ['settings', 'Settings', 'bx-slider-alt'],
        ['machine', 'My machine', 'bx-server'],
    ];
    return el('nav', { class: 'tabs', 'aria-label': 'Network sections' }, ...tabs.map(([key, label, icon]) =>
        el('a', { class: `tab ${active === key ? 'tab--on' : ''}`,
            href: `#/networks/${encodeURIComponent(id)}/${key}` },
            el('i', { class: `bx ${icon}` }), label)));
}

async function connectedTab(data) {
    const gpus = networkGPUs(data.nodes);
    const networkID = encodeURIComponent(data.network.id);
    const ray = await api(`/api/ray?network_id=${networkID}`)
        .catch((error) => ({ detail: error.message, network_id: data.network.id }));
    return el('div', {},
        el('section', { class: 'ps-metrics', style: 'grid-template-columns:repeat(3,minmax(0,1fr))' },
            signalMetric(data), gpuSummaryMetric(gpus), rayMetric(data, ray)),
        el('div', { class: 'detail-grid' },
            el('section', { class: 'panel' },
                el('div', { class: 'panel__head' },
                    el('span', { class: 'grow' }, 'Connected devices'),
                    el('span', { class: 'chip' }, String(data.nodes.length))),
                data.nodes.length ? el('div', { class: 'device-grid' }, ...data.nodes.map(deviceCard))
                    : emptyBlock('bx-devices', 'No devices have connected yet.')),
            el('section', { class: 'panel' },
                el('div', { class: 'panel__head' },
                    el('span', { class: 'grow' }, 'Connected GPUs'),
                    el('span', { class: 'chip chip--warn' }, String(gpus.length))),
                gpus.length ? el('div', { class: 'ps-status-list' }, ...gpus.map(gpuRow))
                    : emptyBlock('bx-chip', 'No graphics cards are connected to this network.'))),
        el('section', { style: 'margin-top:28px' },
            el('div', { class: 'section-heading' },
                el('strong', {}, 'Projects connected to this network'),
                el('a', { href: '#/projects' }, 'View all ', el('i', { class: 'bx bx-right-arrow-alt' }))),
            data.projects.length
                ? el('div', { class: 'ps-card-grid' }, ...data.projects.map((project) => projectCard(project, data)))
                : emptyBlock('bx-layer', 'This network has no projects yet.')));
}

function signalMetric(data) {
    const live = data.nodes.filter(nodeOnline).length;
    return el('div', { class: 'ps-metric-card ps-metric-card--networks' },
        el('div', { class: 'ps-metric-card__surface' },
            el('div', { class: 'ps-metric-head' },
                el('span', { class: 'ps-metric-card__icon' }, el('i', { class: 'bx bx-devices' }))),
            el('div', { class: 'ps-metric-values' }, metricValue(live, 'Online'), metricValue(data.nodes.length, 'Devices'))));
}

function gpuSummaryMetric(gpus) {
    const vram = gpus.reduce((total, gpu) => total + (Number(gpu.vram_total_mb) || 0), 0);
    const vramLabel = vram >= 1024 ? `${Math.round(vram / 1024)}G` : vram ? `${Math.round(vram)}M` : '0';
    return el('div', { class: 'ps-metric-card ps-metric-card--gpus' },
        el('div', { class: 'ps-metric-card__surface' },
            el('div', { class: 'ps-metric-head' },
                el('span', { class: 'ps-metric-card__icon' }, el('i', { class: 'bx bx-chip' }))),
            el('div', { class: 'ps-metric-values' }, metricValue(gpus.length, 'GPUs'),
                metricValue(vramLabel, 'VRAM'))));
}

function rayMetric(data, ray) {
    const running = Boolean(ray?.running);
    const localRunning = Boolean(ray?.local_running);
    const networkID = encodeURIComponent(data.network.id);
    const action = async (actionName) => {
        try {
            await api(`/api/ray/${actionName}?network_id=${networkID}`, {
                method: 'POST', body: { network_id: data.network.id },
            });
            toast(actionName === 'stop' ? 'Ray stopped on this machine.' : 'Ray is starting.');
            location.reload();
        } catch (error) { toast(error.message, 'err'); }
    };
    return el('div', { class: `ps-metric-card ${running ? 'ps-metric-card--projects' : 'ps-metric-card--system'}` },
        el('div', { class: 'ps-metric-card__surface' },
            el('div', { class: 'ps-metric-head' },
                el('span', { class: 'ps-metric-card__icon' }, el('i', { class: 'bx bx-broadcast' })),
                el('span', { class: `chip ${running ? 'chip--good' : 'chip--warn'}` }, running ? 'running' : 'stopped')),
            el('p', { class: 'ps-status-row__title', style: 'margin:10px 6px 3px' },
                running ? `${ray.total_gpu || 0} GPU · ${ray.total_cpu || 0} CPU`
                    : ray?.advice || 'Ray is not running.'),
            el('div', { style: 'display:flex;gap:6px;margin:6px' },
                ray?.installed && ray?.eligible && !localRunning
                    ? el('button', { class: 'btn btn--sm btn--primary', onclick: () => action('start') },
                        el('i', { class: 'bx bx-play' }), ray.head ? 'Attach' : 'Start') : null,
                localRunning
                    ? el('button', { class: 'btn btn--sm', onclick: () => action('stop') },
                        el('i', { class: 'bx bx-stop-circle' }), 'Stop') : null)));
}

function deviceCard(node) {
    const capacity = node.capacity || {};
    const gpuCount = (capacity.gpus || []).length;
    return el('div', { class: 'panel device-card' },
        el('div', { class: 'device-card__head' },
            el('span', { class: 'avatar' }, initials(node.name)),
            el('span', { class: 'row__main' },
                el('span', { class: 'row__title' }, node.name),
                el('span', { class: 'row__meta' }, `${node.os || 'linux'} · ${node.arch || 'unknown'} · ${ago(node.last_seen)}`)),
            el('span', { class: `dot ${nodeOnline(node) ? 'dot--on' : 'dot--off'}` }),
            node.is_self ? el('span', { class: 'chip chip--cyan' }, 'this machine') : null),
        el('div', { class: 'device-card__stats' },
            deviceStat(capacity.cpu_cores || 0, 'CPU cores'),
            deviceStat(megabytes(capacity.ram_total_mb), 'Memory'),
            deviceStat(gpuCount, 'GPUs')));
}

function gpuRow(gpu) {
    return el('div', { class: 'ps-status-row' }, el('i', { class: 'bx bx-chip ps-gpu-icon' }),
        el('span', { class: 'ps-status-row__main' },
            el('span', { class: 'ps-status-row__title' }, gpu.name || 'Graphics card'),
            el('span', { class: 'ps-status-row__meta' },
                `${gpu.machine} · ${megabytes(gpu.vram_total_mb)} VRAM`)),
        gpu.util_percent !== undefined ? el('span', { class: 'chip chip--warn' }, `${gpu.util_percent}%`) : null);
}

function projectCard(project, data) {
    return el('a', { class: 'panel ps-project-card', href: `#/projects/${encodeURIComponent(project.id)}` },
        el('div', { class: 'ps-project-card__top' },
            el('span', { class: 'ps-project-mark' }, el('i', { class: 'bx bx-layer' })),
            el('span', { class: 'ps-project-card__copy' }, el('strong', {}, project.name),
                el('span', {}, project.description || data.network.name)),
            el('span', { class: 'ps-project-card__status' }, el('i', { class: 'bx bx-git-branch' }), project.branch || 'main')),
        el('div', { class: 'ps-project-card__foot' },
            el('span', {}, el('i', { class: project.repository ? 'bx bxl-github' : 'bx bx-hdd' }),
                project.repository ? ' GitHub' : ' Local Git'),
            el('span', { class: 'push' }, `updated ${ago(project.updated_at)}`),
            el('i', { class: 'bx bx-right-arrow-alt' })));
}

function settingsTab(data) {
    const canManage = ['owner', 'admin'].includes(data.network.role || data.membership.account_role);
    const isOwner = (data.network.role || data.membership.account_role) === 'owner';
    const pending = el('div', {});
    // Outstanding invitations are loaded after the page rather than blocking
    // it: a node with no account authority has nowhere to ask, and that is not
    // a reason to fail the settings tab.
    api(`/api/networks/${encodeURIComponent(data.network.id)}/invitations`)
        .then((result) => mount(pending, pendingPanel(result.invitations || [], canManage)))
        .catch(() => {});

    return el('div', { class: 'detail-grid' },
        el('section', {},
            el('section', { class: 'panel' },
                el('div', { class: 'panel__head' },
                    el('span', { class: 'grow' }, 'Accounts and access'),
                    canManage ? el('button', { class: 'btn btn--sm btn--primary', onclick: () => createInvite(data.network) },
                        el('i', { class: 'bx bx-user-plus' }), 'Invite') : null),
                el('div', { class: 'rows' }, ...data.members.map((member) => memberRow(data.network, member, canManage)))),
            pending),
        el('aside', { style: 'display:flex;flex-direction:column;gap:14px' },
            el('section', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Network'),
                el('dl', { class: 'kv' },
                    el('dt', {}, 'Name'), el('dd', {}, data.network.name),
                    el('dt', {}, 'ID'), el('dd', {}, data.network.id),
                    el('dt', {}, 'Created'), el('dd', {}, new Date(data.network.created_at).toLocaleString()))),
            el('section', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Cowork controller'),
                data.controllers.length ? el('div', { class: 'rows' }, ...data.controllers.map((controller) =>
                    el('div', { class: 'row', style: 'cursor:default' },
                        el('span', { class: 'dot dot--on' }), el('span', { class: 'row__main' },
                            el('span', { class: 'row__title' }, controller.name),
                            el('span', { class: 'row__meta' }, controller.address)))))
                    : emptyBlock('bx-broadcast', 'No controller currently supplies Cowork relay to this network.')),
            canManage ? el('section', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Headless machines'),
                el('p', { class: 'muted', style: 'margin:0 0 12px;font-size:12px;line-height:1.6' },
                    'A machine with no browser to sign in on cannot answer an ' +
                    'invitation. Give it a single-use code instead and run ' +
                    'pscluster join there.'),
                el('button', { class: 'btn btn--sm', onclick: () => createJoinCode(data.network) },
                    el('i', { class: 'bx bx-key' }), 'Create a machine code')) : null,
            isOwner ? el('section', { class: 'panel', style: 'border-color:rgba(255,83,112,.3)' },
                el('div', { class: 'panel__head bad-text' }, 'Delete network'),
                el('p', { class: 'muted', style: 'margin:0 0 12px;font-size:12px;line-height:1.6' },
                    'Removes this network from every account and machine. Local project folders are preserved.'),
                el('button', { class: 'btn btn--sm btn--danger', onclick: () => deleteNetwork(data.network) },
                    el('i', { class: 'bx bx-trash' }), 'Delete network')) : null));
}

async function deleteNetwork(network) {
    if (!window.confirm(`Delete ${network.name} from every machine? Project folders stay on disk.`)) return;
    try {
        await api(`/api/networks/${encodeURIComponent(network.id)}`, { method: 'DELETE', body: {} });
        toast(`${network.name} was deleted.`);
        navigate('networks');
    } catch (error) {
        toast(error.message, 'err');
    }
}

function pendingPanel(invitations, canManage) {
    if (!invitations.length) return null;
    return el('section', { class: 'panel', style: 'margin-top:14px' },
        el('div', { class: 'panel__head' },
            el('span', { class: 'grow' }, 'Waiting to be answered'),
            el('span', { class: 'chip chip--warn' }, String(invitations.length))),
        el('div', { class: 'rows' }, ...invitations.map((invite) =>
            el('div', { class: 'row', style: 'cursor:default' },
                el('span', { class: 'avatar' }, initials(invite.display_name || invite.username)),
                el('span', { class: 'row__main' },
                    el('span', { class: 'row__title' }, invite.display_name || invite.username),
                    el('span', { class: 'row__meta' },
                        `@${invite.username} · invited as ${invite.role} ${ago(invite.created_at)}`)),
                canManage ? el('button', {
                    class: 'btn btn--sm btn--icon', title: 'Withdraw this invitation',
                    onclick: async () => {
                        try {
                            await api(`/api/invitations/${encodeURIComponent(invite.id)}`,
                                { method: 'DELETE' });
                            toast(`Invitation to ${invite.username} withdrawn.`);
                            location.reload();
                        } catch (error) { toast(error.message, 'err'); }
                    },
                }, el('i', { class: 'bx bx-x' })) : null))));
}

// createJoinCode is the escape hatch for a machine that has no browser: it
// proves possession of a secret instead of naming a person.
function createJoinCode(network) {
    const endpoint = el('input', { class: 'input input--mono',
        placeholder: 'https://host-or-private-address:10000' });
    modal({ title: `Machine code for ${network.name}`, confirmLabel: 'Create code',
    body: () => el('div', {},
        field('Reachable address', endpoint,
            'Leave empty to use this machine’s private Plainshow address.')),
    onConfirm: async (close) => {
        const body = { minutes: 15, max_uses: 1 };
        if (endpoint.value.trim()) body.endpoint = endpoint.value.trim();
        const invite = await api(`/api/networks/${encodeURIComponent(network.id)}/invites`,
            { method: 'POST', body });
        close(); showInvite(invite);
    } });
}

function memberRow(network, member, canManage) {
    return el('div', { class: 'row', style: 'cursor:default' },
        el('span', { class: 'avatar' }, initials(member.account.display_name || member.account.username)),
        el('span', { class: 'row__main' },
            el('span', { class: 'row__title' }, member.account.display_name || member.account.username),
            el('span', { class: 'row__meta' }, `@${member.account.username}`)),
        canManage && member.role !== 'owner' ? memberControls(network, member)
            : el('span', { class: `chip ${member.role === 'owner' ? 'chip--good' : ''}` }, member.role));
}

function memberControls(network, member) {
    const roles = ['admin', 'operator', 'member', 'viewer'];
    const selector = el('select', { class: 'select', 'aria-label': `Role for ${member.account.display_name}`,
        onchange: async () => {
            selector.disabled = true;
            try {
                await api(`/api/networks/${encodeURIComponent(network.id)}/members/${encodeURIComponent(member.account.id)}`,
                    { method: 'PUT', body: { role: selector.value } });
                member.role = selector.value;
                toast(`${member.account.display_name} is now ${selector.value}.`);
            } catch (error) { selector.value = member.role; toast(error.message, 'err'); }
            selector.disabled = false;
        } }, ...roles.map((role) => el('option', { value: role, selected: role === member.role }, role)));
    return el('span', { style: 'display:flex;align-items:center;gap:7px' }, selector,
        el('button', { class: 'btn btn--sm btn--icon', title: 'Remove from network',
            onclick: async () => {
                if (!window.confirm(`Remove ${member.account.display_name} from ${network.name}?`)) return;
                await api(`/api/networks/${encodeURIComponent(network.id)}/members/${encodeURIComponent(member.account.id)}`,
                    { method: 'DELETE', body: {} });
                toast(`${member.account.display_name} was removed.`); location.reload();
            } }, el('i', { class: 'bx bx-trash' })));
}

function machineTab(data) {
    const membership = data.membership;
    const policy = { ...membership.policy };
    const participation = toggleRow('Participate in this network',
        'This machine remains a member but disconnects its Ray worker when paused.', membership.enabled);
    const worker = toggleRow('Accept distributed jobs',
        'Allow this machine to join the network’s Ray compute pool.', policy.enabled && policy.allow_jobs);
    const gpu = toggleRow('Offer graphics cards',
        'Advertise this machine’s GPUs to Ray jobs in this network.', policy.allow_gpu);
    const terminal = toggleRow('Allow terminal tasks',
        'Permit interactive task execution requested by collaborators.', policy.allow_terminal);
    const files = toggleRow('Accept project files',
        'Let this network put project files on this machine. Without them it can ' +
        'be online and still have nothing to run.', policy.allow_project_sync);
    const cpu = el('input', { class: 'input input--mono', type: 'number', min: '0', step: '1',
        value: policy.max_cpu || 0 });
    const ram = el('input', { class: 'input input--mono', type: 'number', min: '0', step: '256',
        value: policy.max_ram_mb || 0 });

    return el('div', { class: 'detail-grid' },
        el('section', { class: 'panel' },
            el('div', { class: 'panel__head' }, 'My machine in this network'),
            participation.node, worker.node, gpu.node, terminal.node, files.node,
            el('div', { class: 'policy-grid', style: 'margin-top:18px' },
                field('Maximum CPU cores', cpu, '0 means all available cores.'),
                field('Maximum RAM (MB)', ram, '0 means all available memory.')),
            el('button', { class: 'btn btn--primary', style: 'margin-top:16px', onclick: async () => {
                const accepting = worker.value();
                const body = { enabled: participation.value(), policy: {
                    ...policy, enabled: accepting, allow_jobs: accepting,
                    allow_gpu: gpu.value(), allow_terminal: terminal.value(),
                    allow_project_sync: files.value(),
                    max_cpu: Math.max(0, Number.parseInt(cpu.value, 10) || 0),
                    max_ram_mb: Math.max(0, Number.parseInt(ram.value, 10) || 0),
                } };
                await api(`/api/networks/${encodeURIComponent(data.network.id)}/policy`, { method: 'PUT', body });
                toast('This machine’s network limits were saved.');
                location.reload();
            } }, el('i', { class: 'bx bx-save' }), 'Save machine limits')),
        el('aside', { class: 'panel' },
            el('div', { class: 'panel__head' }, 'Detected resources'),
            el('div', { class: 'device-card__stats' },
                deviceStat(state.system.cpu_cores, 'CPU cores'),
                deviceStat(megabytes(state.system.ram_total_mb), 'Memory'),
                deviceStat(state.system.gpus.length, 'GPUs')),
            el('p', { class: 'muted', style: 'font-size:12px;line-height:1.6;margin:16px 0 0' },
                'Network limits can only narrow the device-wide ceiling in global Settings. ' +
                'No network can silently grant itself more of your machine.')));
}

function toggleRow(title, detail, initial) {
    let on = Boolean(initial);
    const toggle = el('button', { class: 'toggle', role: 'switch', 'aria-checked': String(on),
        'aria-label': title, onclick: () => { on = !on; toggle.setAttribute('aria-checked', String(on)); } });
    return { node: el('div', { class: 'switch' }, el('span', { class: 'switch__text' },
        el('strong', {}, title), el('span', {}, detail)), toggle), value: () => on };
}

function field(label, input, note) {
    return el('label', { class: 'field', style: 'margin:0' }, el('span', { class: 'field__label' }, label),
        input, el('span', { class: 'muted', style: 'font-size:10px' }, note));
}

function metricValue(value, label) {
    return el('span', {}, el('span', { class: 'ps-metric-label' }, label),
        el('strong', { class: 'ps-metric-value' }, String(value)));
}

function deviceStat(value, label) {
    return el('span', { class: 'device-stat' }, el('strong', {}, value ?? '—'), el('span', {}, label));
}

function networkGPUs(nodes) {
    return nodes.filter(nodeOnline).flatMap((node) => {
        const gpus = node.is_self ? (state.system?.gpus || node.capacity?.gpus || []) : (node.capacity?.gpus || []);
        return gpus.map((gpu) => ({ ...gpu, machine: node.name }));
    });
}

function nodeOnline(node) {
    if (node.is_self) return true;
    const seen = Date.parse(node.last_seen);
    return Number.isFinite(seen) && Date.now() - seen < 120000;
}

function emptyBlock(icon, text) {
    return el('div', { class: 'empty' }, el('i', { class: `bx ${icon} empty__ico` }),
        el('span', { class: 'empty__text' }, text));
}

// emptyNetworkList tells the two empty cases apart. "Nothing matched your
// search" and "this node belongs to nothing" look identical and lead somewhere
// completely different, and the second one is usually a person looking at a
// different node than the one they set up — one machine can run a packaged
// system node and a personal one, each with its own database — so it names the
// node it is actually talking to.
function emptyNetworkList(filtered) {
    if (filtered) {
        return el('div', { class: 'panel empty' },
            el('i', { class: 'bx bx-search empty__ico' }),
            el('strong', {}, 'No networks match that search.'));
    }
    const overview = state.overview || {};
    const node = overview.node || {};
    return el('div', { class: 'panel empty' }, el('i', { class: 'bx bx-network-chart empty__ico' }),
        el('strong', {}, 'This node belongs to no networks.'),
        el('span', { class: 'empty__text' },
            'Create one, or join with a code from another machine.'),
        overview.networks_error
            ? el('span', { class: 'empty__text', style: 'color:var(--bad)' }, overview.networks_error)
            : null,
        el('span', { class: 'mono dim', style: 'font-size:10px;margin-top:10px' },
            `node ${node.name || '?'} · ${node.root || 'unknown root'}`));
}

function joinNetwork() {
    const code = el('textarea', { class: 'textarea input--mono', rows: '6',
        placeholder: 'psc1_…', spellcheck: 'false' });
    const endpoint = el('input', { class: 'input input--mono',
        placeholder: 'https://this-machine.example:10000 (optional)' });
    modal({ title: 'Join a network', confirmLabel: 'Join', body: () => el('div', {},
        field('Join code', code, 'Paste the private, single-use code.'),
        field('Reachable address', endpoint, 'Normally empty; Plainshow networking supplies it automatically.')),
    onConfirm: async (close) => {
        const body = { code: code.value.trim() };
        if (endpoint.value.trim()) body.endpoint = endpoint.value.trim();
        const joined = await api('/api/networks/join', { method: 'POST', body });
        close(); toast(`Joined ${joined.network.name}.`); location.reload();
    } });
}

// createInvite invites a person, not a machine.
//
// A join code proves somebody was handed a secret; it says nothing about who
// they are, and it has to be carried out of band to whoever is standing at the
// right computer. An invitation is addressed to an account and waits until that
// person looks, from whichever machine they happen to be on. Codes remain for
// headless machines with no account session, under "Join by code".
function createInvite(network) {
    let chosen = null;
    const results = el('div', { class: 'rows', style: 'max-height:190px;overflow-y:auto;margin-top:8px' });
    const search = el('input', { class: 'input', placeholder: 'Username or name…', autocomplete: 'off' });
    const role = el('select', { class: 'input' }, ...['member', 'operator', 'admin', 'viewer']
        .map((name) => el('option', { value: name, selected: name === 'member' }, name)));

    const pick = (account) => {
        chosen = account;
        search.value = account.username;
        mount(results, el('div', { class: 'row', style: 'cursor:default' },
            el('span', { class: 'avatar' }, initials(account.display_name || account.username)),
            el('span', { class: 'row__main' },
                el('span', { class: 'row__title' }, account.display_name || account.username),
                el('span', { class: 'row__meta' }, `@${account.username}`)),
            el('i', { class: 'bx bx-check', style: 'color:var(--good)' })));
    };

    // Debounced: one request per pause in typing rather than one per keystroke.
    let timer = null;
    search.addEventListener('input', () => {
        chosen = null;
        clearTimeout(timer);
        const query = search.value.trim();
        if (query.length < 2) { mount(results); return; }
        timer = setTimeout(async () => {
            try {
                const found = await api(`/api/accounts/search?q=${encodeURIComponent(query)}`);
                const accounts = found.accounts || [];
                mount(results, ...(accounts.length
                    ? accounts.map((account) => el('button', {
                        class: 'row', style: 'text-align:left',
                        onclick: () => pick(account),
                    },
                        el('span', { class: 'avatar' },
                            initials(account.display_name || account.username)),
                        el('span', { class: 'row__main' },
                            el('span', { class: 'row__title' },
                                account.display_name || account.username),
                            el('span', { class: 'row__meta' }, `@${account.username}`))))
                    : [el('p', { class: 'muted', style: 'margin:0;font-size:12px' },
                        'No Plainshow account matches that.')]));
            } catch (error) {
                mount(results, el('p', { class: 'muted', style: 'margin:0;font-size:12px' },
                    error.message));
            }
        }, 220);
    });

    modal({ title: `Invite to ${network.name}`, confirmLabel: 'Send invitation',
    body: () => el('div', {},
        field('Person', search, 'They are notified in their own Plainshow Cluster.'),
        results,
        el('div', { style: 'margin-top:14px' },
            field('Role', role, 'What they may do in this network. Ownership cannot be given away here.'))),
    onConfirm: async (close) => {
        const username = (chosen && chosen.username) || search.value.trim();
        if (!username) throw new Error('Choose who to invite.');
        await api(`/api/networks/${encodeURIComponent(network.id)}/invitations`,
            { method: 'POST', body: { username, role: role.value } });
        close();
        toast(`Invited ${username} to ${network.name}.`);
        location.reload();
    } });
}

function showInvite(invite) {
    const code = el('textarea', { class: 'textarea input--mono', rows: '8', readonly: true }, invite.code);
    modal({ title: 'Machine join code', confirmLabel: 'Copy code', body: () => el('div', {},
        field('Single-use code', code, `Expires ${new Date(invite.expires_at).toLocaleString()}.`)),
    onConfirm: async (close) => { await navigator.clipboard.writeText(invite.code); close(); toast('Join code copied.'); } });
}

function createNetwork() {
    const name = el('input', { class: 'input', placeholder: 'Research lab' });
    modal({ title: 'Create a network', confirmLabel: 'Create', body: () => el('div', {},
        field('Name', name, 'You become the owner and this is the first device.')),
    onConfirm: async (close) => {
        const network = await api('/api/networks', { method: 'POST', body: { name: name.value } });
        close(); toast('Network created.');
        location.hash = `#/networks/${encodeURIComponent(network.id)}`;
        location.reload();
    } });
}
