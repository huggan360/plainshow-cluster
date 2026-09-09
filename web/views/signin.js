// The sign-in gate mirrors plainshow.se while keeping account creation beside
// sign-in for a fresh machine. Central registration returns a session, so a
// successful account creation enters the workspace immediately.

import { el, mount, plainshowLogo } from '../lib/ui.js';
import { api, toast } from '../lib/client.js';

/**
 * renderGate paints the sign-in screen into host and resolves once the browser
 * is authenticated. A central node always opens on sign-in; creating another
 * account is an explicit choice, not something inferred from local node state.
 */
export function renderGate(host, status) {
    return new Promise((resolve) => {
        let create = !status.central && !status.enabled;
        const draw = () => mount(host, card(create, status, resolve, () => {
            create = !create;
            draw();
        }));
        draw();
    });
}

function authField(label, icon, input) {
    return el('label', { class: 'login-field' },
        el('span', { class: 'login-field__label' }, label),
        el('span', { class: 'login-input-wrap' },
            el('i', { class: `bx ${icon} login-input__icon`, 'aria-hidden': 'true' }),
            input));
}

function card(create, status, done, switchMode) {
    const username = el('input', {
        class: 'login-input', autocomplete: 'username', autocapitalize: 'off',
        spellcheck: 'false', placeholder: 'username', required: true,
    });
    const displayName = el('input', {
        class: 'login-input', autocomplete: 'name', placeholder: 'Your name',
    });
    const password = el('input', {
        class: 'login-input', type: 'password', required: true,
        minlength: create ? 10 : null,
        autocomplete: create ? 'new-password' : 'current-password',
        placeholder: create ? 'At least 10 characters' : '••••••••••••',
    });
    const error = el('p', {
        class: 'login-error', role: 'alert', 'aria-live': 'polite',
    });
    const buttonLabel = el('span', {}, create ? 'Create account' : 'Continue');
    const button = el('button', {
        class: 'login-submit', type: 'submit',
    }, buttonLabel, el('i', {
        class: `bx ${create ? 'bx-user-plus' : 'bx-right-arrow-alt'}`,
        'aria-hidden': 'true',
    }));

    const submit = async (event) => {
        event?.preventDefault();
        if (button.disabled) return;
        error.textContent = '';
        button.disabled = true;
        buttonLabel.textContent = create ? 'Creating account…' : 'Signing in…';
        try {
            const body = create
                ? {
                    username: username.value.trim(),
                    display_name: displayName.value.trim(),
                    password: password.value,
                }
                : { username: username.value.trim(), password: password.value };
            const result = await api(create ? '/api/auth/setup' : '/api/auth/login',
                { method: 'POST', body });
            if (result && result.networks_adopted > 0) {
                toast(`Found ${result.networks_adopted} network${
                    result.networks_adopted === 1 ? '' : 's'} on your account.`);
            }
            done();
        } catch (cause) {
            error.textContent = cause.message;
            button.disabled = false;
            buttonLabel.textContent = create ? 'Create account' : 'Continue';
            password.focus();
            password.select();
        }
    };

    const form = el('form', { class: 'login-form', onsubmit: submit },
        authField('Username', 'bx-user', username),
        create ? authField('Display name', 'bx-user', displayName) : null,
        authField('Password', 'bx-lock-alt', password),
        error,
        button);

    queueMicrotask(() => username.focus());

    return el('main', { class: 'login-page' },
        el('section', { class: 'login-shell' },
            el('header', { class: 'login-heading' },
                plainshowLogo(),
                el('h1', {}, 'Welcome to PlainShow')),
            el('div', { class: 'login-panel' },
                el('h2', {}, create ? 'Create account' : 'Sign in'),
                form,
                status.central ? el('p', { class: 'login-switch' },
                    create ? 'Already have an account? ' : 'New to PlainShow? ',
                    el('button', { type: 'button', onclick: switchMode },
                        create ? 'Sign in' : 'Create an account')) : null)));
}
