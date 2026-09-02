// Networks — independent groups this installation belongs to.

import { el, mount, initials } from '../lib/ui.js';
import { api, modal, toast } from '../lib/client.js';

export async function renderNetworks(host) {
    const page = el('div', { class: 'page' });
    mount(host, page);

    const draw = async () => {
        const data = await api('/api/networks');
        mount(page,
            el('div', { class: 'page__head' },
                el('p', { class: 'page__eyebrow' }, 'Memberships'),
                el('h1', { class: 'page__title' }, 'Networks'),
                el('p', { class: 'page__sub' },
                    'One installation can belong to many separate networks. Projects, people, ' +
                    'machines and permissions stay inside the selected network.'),
				el('div', { style: 'display:flex;gap:8px' },
					el('button', { class: 'btn', onclick: () => joinNetwork() }, 'Join by code'),
					el('button', { class: 'btn btn--primary', onclick: () => createNetwork() },
						'+ Create network'))),
			el('div', { class: 'grid grid--2' }, ...data.networks.map((network) =>
				networkCard(network, network.id === data.active))));
    };
    await draw();
    return null;
}

function networkCard(network, active) {
    const details = el('div', {});
    const loadDetails = async () => {
        const [nodes, members] = await Promise.all([
            api(`/api/networks/${encodeURIComponent(network.id)}/nodes`),
            api(`/api/networks/${encodeURIComponent(network.id)}/members`),
        ]);
		const controllers = await api(`/api/networks/${encodeURIComponent(network.id)}/controllers`);
        mount(details,
            el('div', { class: 'panel__head', style: 'margin-top:16px' }, 'Machines'),
            el('div', { class: 'rows' }, ...nodes.map((node) =>
                el('div', { class: 'row', style: 'cursor:default' },
                    el('span', { class: `dot ${node.is_self ? 'dot--on' : 'dot--off'}` }),
                    el('span', { class: 'row__main' },
                        el('span', { class: 'row__title' }, node.name),
                        el('span', { class: 'row__meta' }, node.roles.join(' · '))),
                    node.is_self ? el('span', { class: 'chip chip--cyan' }, 'this machine') : null))),
            el('div', { class: 'panel__head', style: 'margin-top:16px' }, 'Accounts'),
            el('div', { class: 'rows' }, ...members.map((member) =>
                el('div', { class: 'row', style: 'cursor:default' },
                    el('span', { class: 'avatar' }, initials(member.account.username)),
                    el('span', { class: 'row__main' },
                        el('span', { class: 'row__title' }, member.account.display_name),
                        el('span', { class: 'row__meta' }, `@${member.account.username}`)),
                    el('span', { class: 'chip' }, member.role)))));
		if (controllers.length) details.append(el('div', { class: 'panel__head', style: 'margin-top:16px' },
			`Controller: ${controllers[0].name}`));
    };
    loadDetails().catch((err) => mount(details,
        el('p', { class: 'muted', style: 'font-size:12px' }, err.message)));

    return el('div', { class: active ? 'frame' : 'panel' },
        active ? el('div', { class: 'frame__in' }, cardBody()) : cardBody());

    function cardBody() {
        return el('div', {},
            el('div', { class: 'panel__head' },
                el('span', { class: 'grow' }, network.name),
                active ? el('span', { class: 'chip chip--good' }, 'active') : null),
            el('p', { class: 'mono dim', style: 'font-size:10px;margin:0' }, network.id),
            el('div', { style: 'display:flex;gap:8px;margin-top:12px' },
                el('span', { class: 'chip' }, network.role),
                !active ? el('button', {
                    class: 'btn btn--sm', onclick: async () => {
                        await api(`/api/networks/${encodeURIComponent(network.id)}/active`, { method: 'PUT' });
                        toast(`${network.name} is now active.`);
                        location.reload();
                    },
                }, 'Switch to') : null,
				el('button', { class: 'btn btn--sm', onclick: () => createInvite(network) },
					'Invite machine'),
				el('button', { class: 'btn btn--sm', onclick: () => createControllerInvite(network) },
					'Attach controller')),
            details);
    }
}

async function createControllerInvite(network) {
	const invite = await api(`/api/networks/${encodeURIComponent(network.id)}/controller-invites`,
		{ method: 'POST', body: {} });
	const code = el('textarea', { class: 'textarea input--mono', rows: '8', readonly: true }, invite.code);
	modal({ title: 'Controller enrollment code', confirmLabel: 'Copy code',
		body: () => el('div', {}, code, el('p', { class: 'muted' },
			'Run pscluster-controller attach CODE --advertise HTTPS_URL on the controller server.')),
		onConfirm: async (close) => { await navigator.clipboard.writeText(invite.code); close(); toast('Code copied.'); },
	});
}

function joinNetwork() {
	const code = el('textarea', { class: 'textarea input--mono', rows: '6',
		placeholder: 'psc1_…', spellcheck: 'false' });
	const endpoint = el('input', { class: 'input input--mono',
		placeholder: 'https://this-machine.example:10000 (optional)' });
	modal({ title: 'Join a network', confirmLabel: 'Join',
		body: () => el('div', {},
			el('div', { class: 'field' }, el('label', { class: 'field__label' }, 'Join code'), code),
			el('div', { class: 'field' }, el('label', { class: 'field__label' },
				'This machine’s reachable address'), endpoint),
			el('p', { class: 'muted', style: 'font-size:12px;margin:0' },
				'Leave the address empty for hostname-based LAN discovery. For the internet, use a VPN or public HTTPS address.')),
		onConfirm: async (close) => {
			const body = { code: code.value.trim() };
			if (endpoint.value.trim()) body.endpoint = endpoint.value.trim();
			const joined = await api('/api/networks/join', { method: 'POST', body });
			close(); toast(`Joined ${joined.network.name}.`); location.reload();
		},
	});
}

function createInvite(network) {
	const endpoint = el('input', { class: 'input input--mono',
		placeholder: 'https://host-or-vpn-address:10000' });
	const authKey = el('input', { class: 'input input--mono', type: 'password',
		placeholder: 'tskey-auth-… (optional)' });
	const loginServer = el('input', { class: 'input input--mono',
		placeholder: 'Headscale URL (optional)' });
	modal({ title: `Invite to ${network.name}`, confirmLabel: 'Create code',
		body: () => el('div', {},
			el('div', { class: 'field' }, el('label', { class: 'field__label' },
				'Reachable address (optional)'), endpoint),
			el('div', { class: 'field' }, el('label', { class: 'field__label' },
				'Tailscale reusable auth key (optional)'), authKey),
			el('div', { class: 'field' }, el('label', { class: 'field__label' },
				'Headscale login server (optional)'), loginServer),
			el('p', { class: 'muted', style: 'font-size:12px;margin:0' },
				'The code is single-use and expires after 15 minutes. It pins this machine’s TLS identity.')),
		onConfirm: async (close) => {
			const body = { minutes: 15, max_uses: 1 };
			if (endpoint.value.trim()) body.endpoint = endpoint.value.trim();
			if (authKey.value.trim()) body.tailnet_auth_key = authKey.value.trim();
			if (loginServer.value.trim()) body.tailnet_login_server = loginServer.value.trim();
			const invite = await api(`/api/networks/${encodeURIComponent(network.id)}/invites`,
				{ method: 'POST', body });
			close(); showInvite(invite);
		},
	});
}

function showInvite(invite) {
	const code = el('textarea', { class: 'textarea input--mono', rows: '8', readonly: true }, invite.code);
	modal({ title: 'Machine join code', confirmLabel: 'Copy code',
		body: () => el('div', {},
			el('div', { class: 'field' }, el('label', { class: 'field__label' }, 'Single-use code'), code),
			el('p', { class: 'muted', style: 'font-size:12px;margin:0' },
				`Expires ${new Date(invite.expires_at).toLocaleString()}. Share it through a private channel.`)),
		onConfirm: async (close) => { await navigator.clipboard.writeText(invite.code); close(); toast('Join code copied.'); },
	});
}

function createNetwork() {
    const name = el('input', { class: 'input', placeholder: 'Research lab' });
    modal({
        title: 'Create a network', confirmLabel: 'Create',
        body: () => el('div', {},
            el('div', { class: 'field' }, el('label', { class: 'field__label' }, 'Name'), name),
            el('p', { class: 'muted', style: 'font-size:12px;margin:12px 0 0' },
                'You become the owner. This machine becomes the first device in the network.')),
        onConfirm: async (close) => {
            await api('/api/networks', { method: 'POST', body: { name: name.value } });
            close(); toast('Network created.'); location.reload();
        },
    });
}
