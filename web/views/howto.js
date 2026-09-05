// How to — the shortest path from a fresh install to a job running on every
// machine in a network. Deliberately short: if this page needs to be long, the
// product is wrong.

import { el, mount } from '../lib/ui.js';
import { api } from '../lib/client.js';

export async function renderHowTo(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const ray = await api('/api/ray').catch(() => ({}));
    const head = ray.head || 'the first machine you start';

    mount(page,
        el('div', { class: 'page__head' },
            el('p', { class: 'page__eyebrow' }, 'How to'),
            el('h1', { class: 'page__title' }, 'Running work on your machines'),
            el('p', { class: 'page__sub' },
                'Plainshow connects the machines and keeps your projects in step. ' +
                'Ray runs the work. You use your own editor.')),

        step(1, 'Join a network',
            'Networks → create one, or join with a code. Every machine in a ' +
            'network can reach every other one, on any connection, with nothing ' +
            'to configure on your router.'),

        step(2, 'Add a project',
            'Projects → connect a GitHub repository and attach it to the network. ' +
            'Everyone else in that network sees it and can clone it with one click. ' +
            'It is an ordinary folder on your machine — open it in VS Code, or ' +
            'anything else.',
            'Data goes in the project folder like any other file. Put it in ' +
            'data/ and it stays out of git.'),

        step(3, 'Start Ray on the machines',
            'Home → Start Ray. Do it on each machine in the network. The first ' +
            'one becomes the head and the rest attach to it.',
            head !== 'the first machine you start'
                ? `This network's head is ${head}.`
                : null),

        step(4, 'Write ordinary Ray code',
            'Nothing here is Plainshow-specific. If it works on one Ray cluster ' +
            'it works on this one. Ray pools all online CPU cores and enabled ' +
            'NVIDIA, AMD and Intel GPUs. The project still needs the matching ' +
            'vendor runtime on the machine that receives a GPU task.'),

        code(`import ray

ray.init()                       # finds this network's machines

@ray.remote(num_gpus=1)
def train(shard):
    ...

results = ray.get([train.remote(s) for s in shards])`),

        step(null, 'Mixing GPU brands',
            'A normal num_gpus request can land on any available GPU. When code ' +
            'requires one vendor, add a Ray custom resource such as ' +
            'plainshow_gpu_amd, plainshow_gpu_intel or plainshow_gpu_nvidia.'),

        step(5, 'Run it',
            'From the project folder, in your own terminal:'),

        code('python train.py'),

        step(6, 'Watch it',
            'Jobs shows what Ray is running and which machines are busy.'),

        explainer('Run on the Projects page, or run it yourself?',
            'Both reach the same cluster. Pressing Run submits the job to Ray ' +
            'rather than starting a process on this machine:',
            [
                ['The project folder travels with it',
                 'Ray packages the folder and ships it to every machine that runs ' +
                 'part of the job, so they all see the same files. A very large ' +
                 'data/ folder is shipped too, which is the one thing to watch.'],
                ['It is tracked',
                 'The job gets an id, appears under Jobs for everyone in the ' +
                 'network, streams its logs, and has a Stop button.'],
                ['It outlives the page',
                 'Closing the tab, or the window, does not stop it. A terminal run ' +
                 'dies with the terminal.'],
                ['It uses the node’s Ray runtime',
                 'Not your shell’s environment. Packages your code needs must be ' +
                 'installed where Ray runs.'],
            ],
            'Running python train.py yourself still joins the cluster — ray.init() ' +
            'finds the local Ray either way. You just do not get the id, the log ' +
            'capture or the stop button.'),

        explainer('What happens when you switch network',
            'A machine works in one network at a time, and it runs one Ray ' +
            'process, so switching moves it:',
            [
                ['It leaves the old Ray cluster',
                 'Immediately, not on a timer. Anything this machine was ' +
                 'contributing to that network stops.'],
                ['If it was the head, the others are told',
                 'A head that vanishes without saying so leaves every other ' +
                 'machine retrying an address that will never answer.'],
                ['It joins the new network’s cluster',
                 'Attaching to that network’s head, or becoming it when there is ' +
                 'none yet and this machine accepts work.'],
                ['Nothing is deleted',
                 'Projects, history and settings in the other network are exactly ' +
                 'where you left them. Switch back and it rejoins.'],
            ]),

        el('div', { class: 'panel', style: 'margin-top:18px' },
            el('div', { class: 'panel__head' }, 'If something is missing'),
            el('p', { class: 'muted', style: 'margin:0 0 8px;font-size:13px' },
                'Ray not installed on a machine:'),
            code("pip install 'ray[default]'", true),
            el('p', { class: 'muted', style: 'margin:12px 0 8px;font-size:13px' },
                'A machine cannot see the others: check it has joined the network ' +
                'under Networks. Everything runs over that private network, so ' +
                'nothing works until a machine is on it.')));

    return null;
}

function step(n, title, body, note) {
    return el('div', { class: 'panel', style: 'margin-bottom:12px' },
        el('div', { style: 'display:flex;gap:12px;align-items:flex-start' },
            el('span', {
                class: 'mono',
                style: 'flex:none;width:24px;height:24px;display:flex;align-items:center;' +
                       'justify-content:center;border-radius:8px;border:1px solid var(--line-2);' +
                       'font-size:11px;color:var(--cyan)',
            }, n == null ? '•' : String(n)),
            el('div', { style: 'min-width:0;flex:1' },
                el('div', { style: 'font-size:14.5px;font-weight:600;color:#fff' }, title),
                el('p', { class: 'muted', style: 'margin:5px 0 0;font-size:13px;line-height:1.6' }, body),
                note
                    ? el('p', { class: 'mono', style: 'margin:8px 0 0;font-size:11.5px;color:var(--tx-4)' }, note)
                    : null)));
}

// explainer answers a question people actually ask, rather than describing a
// feature. Kept on this page so the answer is one click from the thing itself.
function explainer(title, lead, points, footnote) {
    return el('div', { class: 'panel', style: 'margin:18px 0 12px' },
        el('div', { class: 'panel__head' }, title),
        el('p', { class: 'muted', style: 'margin:0 0 14px;font-size:13px;line-height:1.6' }, lead),
        el('div', { class: 'rows' }, ...points.map(([heading, body]) =>
            el('div', { class: 'row', style: 'cursor:default;align-items:flex-start' },
                el('i', {
                    class: 'bx bx-check ',
                    style: 'color:var(--cyan);font-size:15px;margin-top:2px;flex:none',
                }),
                el('span', { class: 'row__main' },
                    el('span', { class: 'row__title' }, heading),
                    el('span', {
                        class: 'row__meta',
                        style: 'white-space:normal;line-height:1.6',
                    }, body))))),
        footnote
            ? el('p', { class: 'muted', style: 'margin:14px 0 0;font-size:12px;line-height:1.6' },
                footnote)
            : null);
}

function code(text, tight) {
    return el('pre', {
        style: 'margin:0 0 12px;padding:14px 16px;border:1px solid var(--line);' +
               'border-radius:12px;background:#05070b;overflow-x:auto;' +
               'font-family:var(--mono);font-size:12px;line-height:1.6;color:#a5b4c4' +
               (tight ? ';margin-bottom:0' : ''),
    }, text);
}
