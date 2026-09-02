// Terminal — an interactive shell carried by the ordinary job lifecycle.

import { el, mount } from '../lib/ui.js';
import { api, on, toast } from '../lib/client.js';

const stripANSI = (value) => value.replace(/\x1b\[[0-?]*[ -\/]*[@-~]/g, '');

export async function renderTerminal(host) {
    const overview = await api('/api/overview');
    const machine = el('select', { class: 'input' }, ...overview.machines.map((item) =>
        el('option', { value: item.node_id }, item.name)));
    const shell = el('input', { class: 'input input--mono', value: '/bin/sh' });
    const consoleBox = el('pre', { class: 'console', style: 'min-height:420px;white-space:pre-wrap' });
    const input = el('input', { class: 'input input--mono', placeholder: 'Type a command and press Enter', disabled: true });
    let job = null;

    const start = async () => {
        job = await api('/api/jobs', { method: 'POST', body: {
            kind: 'terminal', title: 'Interactive terminal', command: shell.value.trim() || '/bin/sh',
            machine_id: machine.value,
        } });
        input.disabled = false;
        input.focus();
        toast(`Terminal started on ${job.machine}.`);
    };
    input.addEventListener('keydown', async (event) => {
        if (event.key !== 'Enter' || !job) return;
        const value = input.value;
        input.value = '';
        await api(`/api/jobs/${job.id}/input`, { method: 'POST', body: { input: `${value}\n` } });
    });

    mount(host, el('div', { class: 'page' },
        el('div', { class: 'page__head' }, el('p', { class: 'page__eyebrow' }, 'Interactive job'),
            el('h1', { class: 'page__title' }, 'Terminal'),
            el('p', { class: 'page__sub' }, 'Starts a policy-controlled shell on the selected device. It appears in Jobs and stops through the same lifecycle.')),
        el('div', { class: 'panel' },
            el('div', { style: 'display:grid;grid-template-columns:1fr 1fr auto;gap:10px;margin-bottom:12px' },
                machine, shell, el('button', { class: 'btn btn--primary', onclick: start }, 'Start terminal')),
            consoleBox, input)));

    const offLog = on('job.log', (line) => {
        if (!job || line.job_id !== job.id) return;
        consoleBox.textContent += stripANSI(line.text);
        consoleBox.scrollTop = consoleBox.scrollHeight;
    });
    const offState = on('job.state', (state) => {
        if (job && state.id === job.id && state.state !== 'running') input.disabled = true;
    });
    return () => { offLog(); offState(); };
}
