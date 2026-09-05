// The top bar: where this machine is, and the switch that turns it all off.
//
// It is deliberately not a selector. Choosing a network is a decision about
// which cluster your machine joins and which Ray process it runs, so it is made
// on the Networks page where you can see what you are joining. This only
// reports: which network, whether the private network is up, this machine's
// address on it, and whether Ray is running.

import { el, mount } from './ui.js';
import { api, on, state, refresh, modal, toast } from './client.js';

/** networkStatus builds the live status pill shown in the top bar. */
export function networkStatus() {
    const dot = el('span', { class: 'dot dot--off netpill__dot' });
    const name = el('span', { class: 'netpill__name' }, 'No network selected');
    const meta = el('span', { class: 'netpill__meta' }, 'Open Networks to choose one');
    const pill = el('a', {
        class: 'netpill', href: '#/networks',
        title: 'Networks — choose which one this machine works in',
    },
        dot,
        el('span', { class: 'netpill__body' }, name, meta),
        el('i', { class: 'bx bx-chevron-right netpill__chev' }));

    const draw = (network, tailnet, ray) => {
        if (!network) {
            dot.className = 'dot dot--off netpill__dot';
            name.textContent = 'No network selected';
            mount(meta, document.createTextNode('Open Networks to choose one'));
            return;
        }
        name.textContent = network.name;

        // Three states worth telling apart, because each has a different fix:
        // no tailnet client, a client that is not signed in, and a working one.
        const address = tailnet && tailnet.self ? tailnet.self.address : '';
        const connected = Boolean(address);
        dot.className = `dot ${connected ? 'dot--on' : 'dot--bad'} netpill__dot`;

        const parts = [];
        if (connected) {
            parts.push(el('span', { class: 'mono netpill__ip' }, address));
        } else {
            parts.push(el('span', { class: 'netpill__warn' },
                tailnet && tailnet.installed ? 'not connected' : 'no private network'));
        }
        if (ray && ray.running) {
            const nodes = Array.isArray(ray.nodes) ? ray.nodes.length : 0;
            parts.push(el('span', {}, `Ray · ${nodes || 1} machine${nodes === 1 ? '' : 's'}`));
        } else {
            parts.push(el('span', { class: 'dim' }, 'Ray off'));
        }
        mount(meta, ...parts.flatMap((part, index) =>
            index ? [el('span', { class: 'netpill__sep' }, '·'), part] : [part]));
    };

    const load = async () => {
        const overview = state.overview || {};
        const network = (overview.networks || [])
            .find((item) => item.id === overview.active_network) || null;
        // Neither call is worth failing the shell over: an unreachable Ray head
        // or a missing tailscale binary is a status to show, not an error page.
        const [tailnet, ray] = await Promise.all([
            api('/api/tailnet').catch(() => null),
            network ? api('/api/ray').catch(() => null) : Promise.resolve(null),
        ]);
        draw(network, tailnet, ray);
    };

    load();
    on('ray.changed', load);
    // The active network is cached in the shared overview, so re-read it before
    // redrawing or the pill keeps naming the network that was just left.
    on('network.active', async () => {
        await refresh().catch(() => {});
        load();
    });
    // The shell is built once and lives as long as the page, so this interval
    // is never torn down — which is the intent, not an oversight.
    setInterval(load, 20000);
    return pill;
}

/** powerButton stops everything this machine is running for the cluster. */
export function powerButton() {
    return el('button', {
        class: 'btn btn--icon btn--sm topbar__power',
        title: 'Stop Plainshow on this machine',
        'aria-label': 'Stop Plainshow on this machine',
        onclick: confirmShutdown,
    }, el('i', { class: 'bx bx-power-off' }));
}

export function confirmShutdown() {
    const leaveTailnet = el('input', { type: 'checkbox' });
    modal({
        title: 'Stop Plainshow on this machine',
        confirmLabel: 'Stop everything',
        danger: true,
        body: () => el('div', {},
            el('p', { style: 'margin:0 0 14px;font-size:13px;color:#cbd5e1;line-height:1.6' },
                'Nothing of Plainshow keeps running here afterwards. In order: jobs ' +
                'started on this machine are stopped, this machine leaves the Ray ' +
                'cluster, and the node service exits.'),
            el('p', { class: 'muted', style: 'margin:0 0 14px;font-size:12px;line-height:1.6' },
                'If this machine is the Ray head, the other machines are told it has ' +
                'gone rather than being left retrying it. Your projects, history and ' +
                'settings are untouched.'),
            el('label', {
                class: 'switch', style: 'cursor:pointer;align-items:center',
            },
                el('span', { class: 'switch__text' },
                    el('strong', {}, 'Also disconnect the private network'),
                    el('span', {}, 'Runs tailscale down. This machine stays enrolled and ' +
                        'reconnects when the node starts again.')),
                leaveTailnet)),
        onConfirm: async (close) => {
            await api('/api/service/shutdown', {
                method: 'POST', body: { tailnet: leaveTailnet.checked },
            });
            close();
            stoppedScreen();
        },
    });
}

/** stoppedScreen replaces the interface once the node is on its way down.
 *
 * The page it was showing is about to stop answering, so leaving it up would
 * turn every click into an obscure network error. */
function stoppedScreen() {
    toast('Stopping…');
    const app = document.getElementById('app');
    if (!app) return;
    setTimeout(() => {
        mount(app, el('div', {
            class: 'page', style: 'margin:auto;max-width:520px;padding-top:80px',
        },
            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Plainshow is stopped on this machine'),
                el('p', { class: 'muted', style: 'font-size:12.5px;line-height:1.7' },
                    'No jobs, no Ray, and no node service are running here. Start it ' +
                    'again from a terminal:'),
                el('pre', { class: 'snippet' }, 'sudo systemctl start plainshow-cluster'),
                el('p', { class: 'muted', style: 'font-size:12px' },
                    'A node you started yourself instead starts with pscluster serve.'),
                el('button', {
                    class: 'btn', onclick: () => location.reload(),
                }, el('i', { class: 'bx bx-refresh' }), 'Try to reconnect'))));
    }, 900);
}
