// The top bar's machine-wide power control.

import { el, mount } from './ui.js';
import { api, modal, toast } from './client.js';

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
