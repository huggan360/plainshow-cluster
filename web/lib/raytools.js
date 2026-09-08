import { el, mount } from './ui.js';
import { api, toast, navigate, watchRefresh } from './client.js';

const modes = [
    ['cpu', 'All CPUs', 'bx-chip', 'projects'],
    ['gpu', 'All GPUs', 'bx-chip', 'gpus'],
    ['mixed', 'GPU + CPU', 'bx-network-chart', 'networks'],
    ['nvidia', 'NVIDIA', 'bx-chip', 'networks'],
    ['amd', 'AMD', 'bx-chip', 'system'],
    ['intel', 'Intel GPU', 'bx-chip', 'projects'],
];

/** Project-scoped helpers. Nothing changes another device's worker policy. */
export function rayTools(project, activeTab) {
    const test = el('section', { class: 'panel ray-tool' });
    const preset = el('section', { class: 'panel ray-tool' });
    const base = `/api/projects/${encodeURIComponent(project.id)}/ray`;
    const network = encodeURIComponent(project.network_id);
    const result = el('div', { class: 'ray-result', role: 'status', 'aria-live': 'polite' },
        el('i', { class: 'bx bx-network-chart' }),
        el('strong', {}, 'Ready to test'),
        el('p', {}, 'Run a small task on each Ray worker and compare it with the online devices in this network.'));
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
                el('strong', {}, data.ok ? 'All online devices passed' : 'Some devices need attention'),
                el('div', { class: 'ray-checks' }, ...data.devices.map((device) =>
                    el('div', { class: 'ray-check' },
                        el('i', { class: `bx ${!device.online ? 'bx-wifi-off' : device.ok ? 'bx-check-circle' : 'bx-error-circle'}` }),
                        el('span', {}, el('strong', {}, device.name), el('small', {}, device.detail))))),
                el('p', {}, data.detail));
        } catch (error) {
            if (disposed) return;
            result.className = 'ray-result ray-result--error';
            mount(result, el('i', { class: 'bx bx-error-circle' }), el('strong', {}, 'Could not complete the test'),
                el('p', {}, error.message),
                el('a', { class: 'btn', href: `#/networks/${network}` }, 'Open network'));
        } finally { button.disabled = false; }
    } }, el('i', { class: 'bx bx-check-circle' }), 'Test cluster');
    const start = el('button', { class: 'btn', onclick: async () => {
        start.disabled = true;
        try {
            await api('/api/ray/start', { method: 'POST', body: { network_id: project.network_id } });
            toast('Ray started. Other devices attach when assigned to this network.');
        } catch (error) { toast(error.message, 'err'); }
        finally { start.disabled = false; }
    } }, el('i', { class: 'bx bx-play' }), 'Start / attach this device');
    mount(test, el('div', { class: 'panel__head' }, 'Test your cluster'), result,
        el('div', { class: 'ray-tool-actions' }, button, start),
        el('p', { class: 'muted' }, 'Starting Ray assigns this machine’s compute to this project’s network. Test does not install frameworks or run training.'));

    let mode = 'cpu';
    const inventory = el('div', { class: 'ray-inventory muted', 'aria-live': 'polite' }, 'Reading network hardware…');
    const filename = el('input', { class: 'input input--mono', value: 'ray_cpu.py', 'aria-label': 'Preset filename' });
    const limit = el('input', { class: 'input', type: 'number', min: '0', max: '1024', value: '0' });
    const options = el('div', { class: 'ray-preset-grid' }, ...modes.map(([key, title, icon, color]) => {
        const choice = el('button', {
            class: `ps-metric-card ps-metric-card--${color} ray-preset-choice`,
            'aria-pressed': String(key === mode),
            onclick: () => {
                mode = key;
                options.querySelectorAll('button').forEach((node) => node.setAttribute('aria-pressed', String(node === choice)));
                filename.value = `ray_${key}.py`;
            },
        }, el('span', { class: 'ps-metric-card__surface' }, el('i', { class: `bx ${icon}` }), el('strong', {}, title)));
        return choice;
    }));
    const create = el('button', { class: 'btn btn--primary', onclick: async () => {
        create.disabled = true;
        try {
            const data = await api(`${base}/preset`, { method: 'POST', body: { mode, filename: filename.value.trim(), limit: Number(limit.value) } });
            toast(`Created ${data.path}. Add your code in the marked function.`);
            navigate(`projects/${encodeURIComponent(project.id)}/branch/${encodeURIComponent(data.path)}`);
        } catch (error) { toast(error.message, 'err'); create.disabled = false; }
    } }, el('i', { class: 'bx bx-file-blank' }), 'Create preset file');
    mount(preset, el('div', { class: 'panel__head' }, 'Ray presets'),
        el('p', { class: 'muted' }, 'Choose the hardware to use. The generated file discovers live Ray workers each time you run it.'),
        inventory, options,
        el('div', { class: 'grid grid--2' },
            el('label', { class: 'field' }, el('span', { class: 'field__label' }, 'Filename'), filename),
            el('label', { class: 'field' }, el('span', { class: 'field__label' }, 'Workers per device · 0 = all allowed'), limit)),
        create,
        el('p', { class: 'muted' }, 'GPU + CPU uses GPUs on GPU machines and CPUs on CPU-only machines. Each worker runs an independent task. Install the matching CUDA, ROCm or XPU framework yourself; this does not combine unlike GPUs into one training device.'));
    const updateInventory = async () => {
        const data = await api(`/api/networks/${network}`);
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
    if (activeTab === 'preset') {
        updateInventory().catch((err) => { inventory.textContent = err.message; });
        off = watchRefresh(['peers.changed', 'connection.restored'], updateInventory);
    }
    return { test, preset, close: () => { disposed = true; abort?.abort(); off(); } };
}
