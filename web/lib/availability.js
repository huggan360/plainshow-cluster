// Whether this machine is available to the cluster.
//
// The corner used to report whether the browser could reach the node, which is
// almost never the question somebody has. This answers the one they do have:
// may other people's work run on my computer right now.
//
// It is not saved. A machine is available when it starts, because that is what
// somebody who installed a cluster expects, and going offline is a decision
// about this afternoon rather than a setting to rediscover in March. Settings
// holds the durable version of the same idea.

import { el, mount } from './ui.js';
import { api, on, toast } from './client.js';

export function availabilitySwitch() {
    const box = el('div', { class: 'presence' });

    const draw = (state) => {
        const available = Boolean(state.available);
        const blocked = !state.accept_work;

        const toggle = el('button', {
            class: `presence__switch ${available && !blocked ? 'presence__switch--on' : ''}`,
            role: 'switch', 'aria-checked': String(available && !blocked),
            disabled: blocked,
            title: blocked
                ? 'This machine is set to refuse work in Settings'
                : available
                    ? 'Available — other people may run work here'
                    : 'Offline — nothing new will run here',
            onclick: async () => {
                try {
                    const next = await api('/api/presence', {
                        method: 'PUT', body: { available: !available },
                    });
                    draw(next);
                    toast(next.available
                        ? 'This machine is available to the cluster.'
                        : 'This machine is offline. Nothing new will run here.');
                } catch (err) { toast(err.message, 'err'); }
            },
        });

        // No label. The switch position says it, and the tooltip says what it
        // means — a word here would sit next to the connection dot and read as
        // if it described that instead.
        mount(box, toggle);
    };

    api('/api/presence').then(draw).catch(() => mount(box));
    on('presence.changed', draw);
    on('settings.changed', () => api('/api/presence').then(draw).catch(() => {}));
    return box;
}
