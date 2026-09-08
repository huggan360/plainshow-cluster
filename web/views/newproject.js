// Bringing a repository into Plainshow — the console's create page.
//
// A page rather than a dialog, because choosing a repository is browsing, and
// browsing inside a modal is miserable. The shape is the console's: your
// repositories to pick from, the name and where its git lives underneath, and
// one link out for the case where there is no repository yet.

import { el, mount } from '../lib/ui.js';
import { api, toast, navigate, refresh, state } from '../lib/client.js';

export async function renderNewProject(host) {
    const page = el('div', { class: 'page np' });
    mount(host, page);

    const form = {
        name: '', repository: '', source: 'repository',
        gitMode: 'local', visibility: 'private',
    };
    let repositories = [];
    let search = '';
    let github = { connected: false, error: '' };
    let creating = false;

    const status = await api('/api/github').catch(() => ({ connected: false }));
    github = { connected: Boolean(status.connected), error: status.error || '' };
    if (github.connected) {
        repositories = await api('/api/github/repositories').catch((err) => {
            github.error = err.message;
            return [];
        });
    }

    const slug = (value) => value.toLowerCase().replace(/[^a-z0-9-_]/g, '-')
        .replace(/^-+|-+$/g, '');

    const create = async () => {
        if (creating) return;
        creating = true;
        draw();
        try {
            let project;
            if (form.source === 'repository') {
                project = await api('/api/github/clone', {
                    method: 'POST',
                    body: { repository: form.repository, name: form.name },
                });
            } else {
                project = await api('/api/projects', {
                    method: 'POST', body: { name: form.name },
                });
                if (form.gitMode === 'github') {
                    // Created first, connected second: a project that exists
                    // with no remote is recoverable, a remote with no project
                    // is litter on somebody's GitHub account.
                    await api(`/api/projects/${encodeURIComponent(project.name)}/repository`, {
                        method: 'POST',
                        // create:true is what makes GitHub mint the repository;
                        // without it this asks to link one that does not exist.
                        body: {
                            create: true, repository: form.name,
                            private: form.visibility === 'private',
                        },
                    }).catch((err) => toast(`Project created, but GitHub refused: ${err.message}`, 'err'));
                }
            }
            toast(`Created ${project.name}.`);
            await refresh();
            navigate(`projects/${encodeURIComponent(project.id || project.name)}`);
        } catch (err) {
            toast(err.message, 'err');
            creating = false;
            draw();
        }
    };

    const draw = () => {
        const local = form.source === 'empty';
        const visible = repositories.filter((repo) =>
            `${repo.full_name} ${repo.description || ''}`.toLowerCase().includes(search.toLowerCase()));
        const ready = Boolean(form.name) && (local || Boolean(form.repository));

        mount(page,
            el('a', { class: 'np__back', href: '#/projects' },
                el('i', { class: 'bx bx-left-arrow-alt' }), 'Back to projects'),
            el('h1', { class: 'np__title' }, 'Bring a repository into Plainshow.'),

            el('section', { class: `panel np__repos ${local ? 'np--dim' : ''}` },
                el('div', { class: 'np__head' },
                    el('div', {},
                        el('p', { class: 'np__label' }, 'Your GitHub repositories'),
                        el('p', { class: 'np__hint' }, github.connected
                            ? `${repositories.length} repositories available`
                            : 'GitHub is not connected')),
                    github.connected
                        ? el('label', { class: 'ps-search', style: 'max-width:280px' },
                            el('i', { class: 'bx bx-search' }),
                            el('input', {
                                class: 'input', placeholder: 'Find a repository…', value: search,
                                oninput: (event) => { search = event.target.value; drawGrid(); },
                            }))
                        : el('a', { class: 'btn btn--primary', href: '#/github' },
                            el('i', { class: 'bx bxl-github' }), 'Connect GitHub')),
                grid),

            el('section', { class: 'panel np__name' },
                el('div', { class: 'np__namegrid' },
                    el('div', {},
                        el('label', { class: 'field' },
                            el('span', { class: 'field__label' }, 'Plainshow project name'),
                            el('input', {
                                class: 'input', placeholder: 'my-project', value: form.name,
                                oninput: (event) => {
                                    form.name = slug(event.target.value);
                                    event.target.value = form.name;
                                    updateReady();
                                },
                            })),
                        el('p', { class: 'np__where' },
                            local
                                ? el('span', {},
                                    el('i', { class: `bx ${form.gitMode === 'github' ? 'bxl-github' : 'bx-git-branch'}` }),
                                    form.gitMode === 'github'
                                        ? ` ${form.visibility} GitHub · ${form.name || 'name it above'}`
                                        : ' Local Git repository')
                                : form.repository
                                    ? el('span', {}, el('i', { class: 'bx bxl-github' }), ` ${form.repository}`)
                                    : 'Select a repository above'),
                        local
                            ? el('div', { class: 'np__mode' },
                                modeButton('bx-git-branch', 'Local Git', form.gitMode === 'local',
                                    false, () => { form.gitMode = 'local'; draw(); }),
                                modeButton('bxl-github', 'GitHub', form.gitMode === 'github',
                                    !github.connected, () => { form.gitMode = 'github'; draw(); }))
                            : null,
                        local && form.gitMode === 'github'
                            ? el('div', { class: 'np__mode', style: 'margin-top:8px' },
                                modeButton('bx-lock-alt', 'Private', form.visibility === 'private',
                                    false, () => { form.visibility = 'private'; draw(); }),
                                modeButton('bx-globe', 'Public', form.visibility === 'public',
                                    false, () => { form.visibility = 'public'; draw(); }))
                            : null),
                    el('button', {
                        class: 'btn btn--primary np__create', disabled: !ready || creating,
                        onclick: create,
                    }, el('i', { class: 'bx bx-layer-plus' }),
                        creating ? 'Creating…' : 'Create project'))),

            el('button', {
                class: 'np__toggle',
                onclick: () => {
                    form.source = local ? 'repository' : 'empty';
                    form.repository = '';
                    draw();
                },
            }, el('i', { class: `bx ${local ? 'bxl-github' : 'bx-unlink'}` }),
                local ? 'Clone a GitHub repository instead' : 'Create without connecting to GitHub'));

        drawGrid();
    };

    // The grid and the Create button are redrawn on their own so typing in the
    // search box or the name field does not rebuild the page under the cursor
    // and lose the caret.
    const grid = el('div', { class: 'np__grid' });
    const drawGrid = () => {
        if (!github.connected) {
            mount(grid, el('p', { class: 'np__empty' },
                github.error || 'Connect GitHub to browse and clone your repositories.'));
            return;
        }
        const visible = repositories.filter((repo) =>
            `${repo.full_name} ${repo.description || ''}`.toLowerCase().includes(search.toLowerCase()));
        mount(grid, ...(visible.length
            ? visible.map(repoCard)
            : [el('p', { class: 'np__empty' }, 'No repositories match your search.')]));
    };

    const repoCard = (repo) => el('button', {
        class: `np__repo ${form.repository === repo.full_name && form.source === 'repository' ? 'np__repo--on' : ''}`,
        onclick: () => {
            form.source = 'repository';
            form.repository = repo.full_name;
            // The repository's own name is almost always the right project
            // name, and it is the folder name on disk, so offer it rather than
            // making somebody retype it.
            if (!form.name) form.name = slug(repo.name);
            draw();
        },
    },
        el('div', { class: 'np__repo-top' },
            el('i', { class: 'bx bxl-github' }),
            el('span', { class: 'np__repo-name' }, repo.full_name),
            el('span', { class: 'np__repo-vis' }, repo.private ? 'Private' : 'Public')),
        el('p', { class: 'np__repo-desc' }, repo.description || 'No description'),
        el('p', { class: 'np__repo-meta' },
            repo.pushed_at ? `updated ${new Date(repo.pushed_at).toLocaleDateString()}` : ''));

    const updateReady = () => {
        const button = page.querySelector('.np__create');
        if (button) {
            button.disabled = !(form.name && (form.source === 'empty' || form.repository)) || creating;
        }
        const where = page.querySelector('.np__where');
        if (where && form.source === 'empty' && form.gitMode === 'github') {
            where.textContent = ` ${form.visibility} GitHub · ${form.name || 'name it above'}`;
        }
    };

    function modeButton(icon, label, on, disabled, onclick) {
        return el('button', {
            class: `np__modebtn ${on ? 'np__modebtn--on' : ''}`, disabled, onclick,
        }, el('i', { class: `bx ${icon}` }), label);
    }

    draw();
    return null;
}
