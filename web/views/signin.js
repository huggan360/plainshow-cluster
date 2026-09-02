// The sign-in gate: first-run setup, sign in, and recovering from an expired
// session.
//
// Authentication is opt-in. A node with no owner account serves everything and
// binds to loopback, which is what makes the first run possible; this screen is
// how it stops being that.

import { el, mount } from '../lib/ui.js';
import { api } from '../lib/client.js';

/**
 * renderGate paints the sign-in screen into host and resolves once the browser
 * is authenticated. It never resolves if the person does not sign in, which is
 * the point: the workspace is not built behind it.
 */
export function renderGate(host, status) {
    return new Promise((resolve) => {
		let create = !status.enabled;
		const draw = () => mount(host, card(create, status, resolve, () => {
			create = !create;
			draw();
		}));
		draw();
    });
}

function card(firstRun, status, done, switchMode) {
    const username = el('input', {
        class: 'input', autocomplete: 'username', autocapitalize: 'off',
        placeholder: firstRun ? 'Pick a username' : 'Username',
    });
    const displayName = el('input', {
        class: 'input', autocomplete: 'name', placeholder: 'Your name (optional)',
    });
    const password = el('input', {
        class: 'input', type: 'password',
        autocomplete: firstRun ? 'new-password' : 'current-password',
        placeholder: firstRun ? 'At least 10 characters' : 'Password',
    });
	const bootstrap = el('input', {
		class: 'input input--mono', type: 'password', autocomplete: 'off',
		placeholder: 'Only for the first global administrator',
	});
    const error = el('p', { class: 'bad-text', style: 'min-height:18px;margin:0' });
    const button = el('button', {
        class: 'btn btn--primary', style: 'width:100%',
    }, firstRun ? 'Create owner account' : 'Sign in');

    const submit = async () => {
        error.textContent = '';
        button.disabled = true;
        try {
            const body = firstRun
                ? {
                    username: username.value.trim(),
                    display_name: displayName.value.trim(),
                    password: password.value,
					bootstrap_token: bootstrap.value.trim(),
                }
                : { username: username.value.trim(), password: password.value };
            await api(firstRun ? '/api/auth/setup' : '/api/auth/login',
                { method: 'POST', body });
            done();
        } catch (err) {
            error.textContent = err.message;
            button.disabled = false;
            password.focus();
            password.select();
        }
    };

    button.addEventListener('click', submit);
    for (const field of [username, displayName, password]) {
        field.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
    }
    queueMicrotask(() => username.focus());

    return el('main', { class: 'login-page' },
        el('div', { class: 'frame login-card' }, el('div', { class: 'frame__in' },
            el('div', { class: 'brand', style: 'padding:0 0 26px' },
                el('span', { class: 'brand__mark' }),
                el('span', {},
                    el('span', { class: 'brand__word' },
                        el('b', {}, 'plain'), el('span', {}, 'show')),
                    el('span', { class: 'brand__sub' }, 'cluster'))),

            el('p', { class: 'page__eyebrow' },
				status.central ? 'Plainshow account' : (firstRun ? 'First run' : 'Secure workspace')),
            el('h1', { class: 'page__title' },
				firstRun ? (status.central ? 'Create an account' : 'Claim this node') : 'Sign in'),
            el('p', { class: 'page__sub', style: 'margin-bottom:20px' },
				status.central
					? 'One account works across every Plainshow device and network.'
					: firstRun
                    ? 'This node has no owner yet, so anyone who can reach it can use it. ' +
                      'Create an account and it will ask for a password from now on.'
                    : 'This node belongs to someone. Sign in to continue.'),

            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Username'), username),
            firstRun
                ? el('div', { class: 'field' },
                    el('label', { class: 'field__label' }, 'Display name'), displayName)
                : null,
			firstRun && status.central
				? el('div', { class: 'field' },
					el('label', { class: 'field__label' }, 'Bootstrap token (first account only)'), bootstrap)
				: null,
            el('div', { class: 'field' },
                el('label', { class: 'field__label' }, 'Password'), password),
            error,
			el('div', { style: 'margin-top:12px' }, button),
			status.central ? el('button', { class: 'btn', style: 'width:100%;margin-top:8px', onclick: switchMode },
				firstRun ? 'Use an existing account' : 'Create a new account') : null)));
}
