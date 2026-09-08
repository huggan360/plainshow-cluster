// Your account: what you are called, and your password.
//
// The username is not here. It is what project membership, GitHub sync and
// invitations are keyed by, so renaming it would quietly detach somebody from
// their own work — a display name is the part that is safe to change.

import { el, mount } from '../lib/ui.js';
import { api, toast, state, refresh } from '../lib/client.js';

export async function renderProfile(host) {
    const page = el('div', { class: 'page profile' });
    mount(host, page);

    const account = (state.overview && state.overview.account) || {};
    const name = el('input', {
        class: 'input', value: account.display_name || '', placeholder: 'Your name',
    });
    const current = el('input', { class: 'input', type: 'password', autocomplete: 'off' });
    const next = el('input', { class: 'input', type: 'password', autocomplete: 'off' });
    const again = el('input', { class: 'input', type: 'password', autocomplete: 'off' });

    const saveName = async () => {
        const value = name.value.trim();
        if (!value) { toast('Give a name.', 'err'); return; }
        try {
            await api('/api/profile', { method: 'PUT', body: { display_name: value } });
            await refresh();
            toast('Name saved.');
        } catch (err) { toast(err.message, 'err'); }
    };

    const savePassword = async () => {
        if (next.value !== again.value) {
            toast('The two new passwords do not match.', 'err');
            return;
        }
        if (next.value.length < 10) {
            toast('A password must be at least 10 characters.', 'err');
            return;
        }
        try {
            await api('/api/profile/password', {
                method: 'PUT',
                body: { current_password: current.value, new_password: next.value },
            });
            current.value = next.value = again.value = '';
            toast('Password changed.');
        } catch (err) { toast(err.message, 'err'); }
    };

    mount(page,
        el('div', { class: 'page__head' },
            el('p', { class: 'page__eyebrow' }, 'Account'),
            el('h1', { class: 'page__title' }, account.display_name || account.username || 'You')),

        el('div', { class: 'grid grid--2' },
            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Who you are'),
                el('label', { class: 'field' },
                    el('span', { class: 'field__label' }, 'Username'),
                    el('input', {
                        class: 'input input--mono', value: account.username || '', disabled: true,
                    })),
                el('p', { class: 'muted', style: 'margin:0 0 14px;font-size:12px;line-height:1.6' },
                    'Your username cannot change: projects, invitations and GitHub ' +
                    'access are all keyed by it.'),
                el('label', { class: 'field' },
                    el('span', { class: 'field__label' }, 'Display name'), name),
                el('button', { class: 'btn btn--primary', onclick: saveName },
                    el('i', { class: 'bx bx-save' }), 'Save name')),

            el('div', { class: 'panel' },
                el('div', { class: 'panel__head' }, 'Password'),
                el('label', { class: 'field' },
                    el('span', { class: 'field__label' }, 'Current password'), current),
                el('label', { class: 'field' },
                    el('span', { class: 'field__label' }, 'New password'), next),
                el('label', { class: 'field' },
                    el('span', { class: 'field__label' }, 'New password again'), again),
                el('button', { class: 'btn btn--primary', onclick: savePassword },
                    el('i', { class: 'bx bx-lock-alt' }), 'Change password'),
                el('p', { class: 'muted', style: 'margin:14px 0 0;font-size:12px;line-height:1.6' },
                    'Your other machines stay signed in. Changing a password on a ' +
                    'laptop should not disown a run happening in another room.'))));

    return null;
}
