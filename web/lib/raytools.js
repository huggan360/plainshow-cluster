import { el, mount } from './ui.js';
import { api, toast, navigate, watchRefresh, state, on } from './client.js';

const modes = [
    ['cpu', 'All CPUs'],
    ['gpu', 'All GPUs'],
    ['mixed', 'GPU + CPU'],
    ['nvidia', 'NVIDIA'],
    ['amd', 'AMD'],
    ['intel', 'Intel GPU'],
];

function softwareCheck(check) {
    const good = check.status === 'passed' || check.status === 'available';
    const skipped = check.status === 'skipped';
    return el('div', { class: `ray-software-check ray-software-check--${good ? 'ok' : skipped ? 'skip' : 'warn'}` },
        el('span', { class: 'ray-software-check__title' },
            el('i', { class: `bx ${good ? 'bx-check-circle' : skipped ? 'bx-chip' : 'bx-error-circle'}` }),
            `${check.name} · ${check.status}`),
        el('small', {}, check.detail));
}

/** Project-scoped helpers. Nothing changes another device's worker policy. */
export function rayTools(project, activeTab) {
    const test = el('section', { class: 'panel ray-tool' });
    const preset = el('section', { class: 'panel ray-tool' });
    const testTarget = el('a', { class: 'btn btn--sm', href: '#/networks' });
    const presetTarget = el('a', { class: 'btn btn--sm', href: '#/networks' });
    const showTarget = () => {
        const name = state.overview.networks.find(n => n.id === state.overview.active_network)?.name;
        for (const link of [testTarget, presetTarget]) link.textContent = `Run network: ${name || 'choose a network'}`;
    };
    showTarget();
    const base = `/api/projects/${encodeURIComponent(project.id)}/ray`;
    const result = el('div', { class: 'ray-result', role: 'status', 'aria-live': 'polite' },
        el('i', { class: 'bx bx-network-chart' }),
        el('strong', {}, 'Ready to test'),
        el('p', {}, 'Check each online worker, run a CPU calculation, inspect GPU software and try one available GPU per device.'));
    let disposed = false, abort;
    const button = el('button', { class: 'btn btn--primary', onclick: async () => {
        button.disabled = true;
        abort = new AbortController();
        result.className = 'ray-result';
        mount(result, el('span', { class: 'spin', 'aria-hidden': 'true' }),
            el('strong', {}, 'Testing the network…'), el('p', {}, 'Waiting for each worker. Up to 75 seconds.'));
        try {
            const data = await api(`${base}/test`, { method: 'POST', body: {}, signal: abort.signal });
            if (disposed) return;
            result.className = `ray-result ray-result--${data.ok ? 'ok' : 'error'}`;
            mount(result,
                el('i', { class: `bx ${data.ok ? 'bx-check-circle' : 'bx-error-circle'}` }),
                el('strong', {}, data.ok ? 'All online devices passed · connectivity' : 'Some devices need attention'),
                el('div', { class: 'ray-checks' }, ...data.devices.map((device) =>
                    el('div', { class: 'ray-check' },
                        el('i', { class: `bx ${!device.online ? 'bx-wifi-off' : device.ok ? 'bx-check-circle' : 'bx-error-circle'}` }),
                        el('div', { class: 'ray-check__body' }, el('strong', {}, device.name), el('small', {}, device.detail),
                            device.cpu ? softwareCheck(device.cpu) : null,
                            device.gpu ? softwareCheck(device.gpu) : null,
                            device.software?.length ? el('details', { class: 'ray-software' },
                                el('summary', {}, 'GPU software & framework'),
                                ...device.software.map(softwareCheck)) : null)))),
                el('p', {}, data.detail));
        } catch (error) {
            if (disposed) return;
            result.className = 'ray-result ray-result--error';
            mount(result, el('i', { class: 'bx bx-error-circle' }), el('strong', {}, 'Could not complete the test'),
                el('p', {}, error.message),
                el('a', { class: 'btn', href: '#/networks' }, 'Choose network'));
        } finally { button.disabled = false; }
    } }, el('i', { class: 'bx bx-check-circle' }), 'Test cluster');
    const start = el('button', { class: 'btn', onclick: async () => {
        start.disabled = true;
        try {
            await api('/api/ray/start', { method: 'POST', body: {} });
            if (disposed) return;
            toast('Ray started. Other devices attach when assigned to this network.');
        } catch (error) { if (!disposed) toast(error.message, 'err'); }
        finally { start.disabled = false; }
    } }, el('i', { class: 'bx bx-play' }), 'Start / attach this device');
    mount(test, el('div', { class: 'panel__head' }, 'Test your cluster'), result,
        el('div', { class: 'ray-tool-actions' }, button, start),
        testTarget,
        el('p', { class: 'muted' }, 'Test and presets use the active network selected on Networks. Starting Ray contributes this machine’s compute there. Nothing is installed automatically.'));

    let mode = 'cpu';
    const inventory = el('div', { class: 'ray-inventory muted', 'aria-live': 'polite' }, 'Reading network hardware…');
    const filename = el('input', { class: 'input input--mono', value: 'ray_cpu.py', 'aria-label': 'Preset filename' });
    const limit = el('input', { class: 'input', type: 'number', min: '0', max: '1024', value: '0' });
    const options = el('div', { class: 'ray-preset-grid' }, ...modes.map(([key, title]) => {
        const choice = el('button', {
            class: 'ps-metric-card ray-preset-choice',
            'aria-pressed': String(key === mode),
            onclick: () => {
                mode = key;
                options.querySelectorAll('button').forEach((node) => node.setAttribute('aria-pressed', String(node === choice)));
                filename.value = `ray_${key}.py`;
            },
        }, el('span', { class: 'ps-metric-card__surface ray-toggle__surface' },
            el('strong', { class: 'ray-preset-choice__label' }, title)));
        return choice;
    }));
    const create = el('button', { class: 'btn btn--primary', onclick: async () => {
        create.disabled = true;
        try {
            const data = await api(`${base}/preset`, { method: 'POST', body: { mode, filename: filename.value.trim(), limit: Number(limit.value) } });
            if (disposed) return;
            toast(`Created ${data.path}. Add your code in the marked function.`);
            navigate(`projects/${encodeURIComponent(project.id)}/branch/${encodeURIComponent(data.path)}`);
        } catch (error) { if (!disposed) toast(error.message, 'err'); create.disabled = false; }
    } }, el('i', { class: 'bx bx-file-blank' }), 'Create preset file');
    mount(preset, el('div', { class: 'panel__head' }, 'Ray presets'),
        el('p', { class: 'muted' }, 'Choose the hardware to use. The generated file discovers live Ray workers each time you run it.'),
        presetTarget, inventory, options,
        el('div', { class: 'grid grid--2' },
            el('label', { class: 'field' }, el('span', { class: 'field__label' }, 'Filename'), filename),
            el('label', { class: 'field' }, el('span', { class: 'field__label' }, 'Workers per device · 0 = all allowed'), limit)),
        create,
        el('p', { class: 'muted' }, 'GPU + CPU uses GPUs on GPU machines and CPUs on CPU-only machines. Each worker runs an independent task. Install the matching CUDA, ROCm or XPU framework yourself; this does not combine unlike GPUs into one training device.'));
    const updateInventory = async () => {
        if (!state.overview.active_network) { inventory.textContent = 'Choose an active network on Networks first.'; return; }
        const data = await api(`/api/networks/${encodeURIComponent(state.overview.active_network)}`);
        if (disposed) return;
        const nodes = (data.nodes || []).filter((n) => n.online ?? (n.is_self || Date.now() - Date.parse(n.last_seen) < 120000));
        const cpu = nodes.reduce((n, device) => n + Number(device.capacity?.cpu_cores || 0), 0);
        const gpu = nodes.flatMap((device) => device.capacity?.gpus || []);
        mount(inventory,
            el('span', { class: 'chip' }, `${nodes.length} online devices`),
            el('span', { class: 'chip' }, `${cpu} detected CPUs`),
            ...['nvidia', 'amd', 'intel'].map((vendor) => el('span', { class: 'chip' },
                `${gpu.filter((g) => g.vendor === vendor && g.trainable).length} ${vendor.toUpperCase()} compute GPUs`)),
            el('small', {}, 'Actual workers follow Ray assignments and each device’s resource limits.'));
    };
    let off = () => {};
    const offTarget = on('network.active', showTarget);
    const offTargets = on('networks.changed', showTarget);
    if (activeTab === 'preset') {
        updateInventory().catch((err) => { inventory.textContent = err.message; });
        off = watchRefresh(['peers.changed', 'network.active', 'networks.changed', 'connection.restored'], updateInventory);
    }
    return { test, preset, close: () => { disposed = true; abort?.abort(); off(); offTarget(); offTargets(); } };
}
