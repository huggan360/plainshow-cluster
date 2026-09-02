// Small DOM and formatting helpers shared by every view.
//
// The interface is hand-written ES modules with no build step: a node ships one
// binary and serves exactly what is in this directory, which keeps the install
// story honest and the debugging obvious.

/** el builds a DOM node. Attributes starting with "on" become listeners. */
export function el(tag, attrs = {}, ...children) {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(attrs || {})) {
        if (value === null || value === undefined || value === false) continue;
        if (key === 'class') node.className = value;
        else if (key === 'html') node.innerHTML = value;
        else if (key.startsWith('on') && typeof value === 'function') {
            node.addEventListener(key.slice(2).toLowerCase(), value);
        } else if (key === 'dataset') {
            Object.assign(node.dataset, value);
        } else {
            node.setAttribute(key, value === true ? '' : String(value));
        }
    }
    for (const child of children.flat(Infinity)) {
        if (child === null || child === undefined || child === false) continue;
        node.append(child instanceof Node ? child : document.createTextNode(String(child)));
    }
    return node;
}

/** clear removes every child of a node. */
export function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
    return node;
}

/** mount replaces a node's contents with the given children. */
export function mount(node, ...children) {
    clear(node);
    for (const child of children.flat(Infinity)) {
        if (child) node.append(child);
    }
    return node;
}

/** bytes renders a byte count the way a person reads it. */
export function bytes(n) {
    if (!n && n !== 0) return '—';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    let v = n;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

/** megabytes renders an MB count as MB or GB. */
export function megabytes(mb) {
    if (!mb) return '—';
    return mb >= 1024 ? `${(mb / 1024).toFixed(mb >= 10240 ? 0 : 1)} GB` : `${mb} MB`;
}

/** ago renders an ISO timestamp as a short relative time. */
export function ago(iso) {
    if (!iso) return '—';
    const then = Date.parse(iso);
    if (Number.isNaN(then)) return '—';
    const secs = Math.max(0, Math.round((Date.now() - then) / 1000));
    if (secs < 45) return 'just now';
    if (secs < 3600) return `${Math.round(secs / 60)} min ago`;
    if (secs < 86400) return `${Math.round(secs / 3600)} h ago`;
    return `${Math.round(secs / 86400)} d ago`;
}

/** duration renders a span between two timestamps. */
export function duration(startIso, endIso) {
    if (!startIso) return '—';
    const start = Date.parse(startIso);
    const end = endIso ? Date.parse(endIso) : Date.now();
    if (Number.isNaN(start) || Number.isNaN(end)) return '—';
    const secs = Math.max(0, Math.round((end - start) / 1000));
    if (secs < 60) return `${secs}s`;
    if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`;
    return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`;
}

/** uptime renders a second count as days and hours. */
export function uptime(secs) {
    if (!secs) return '—';
    const days = Math.floor(secs / 86400);
    const hours = Math.floor((secs % 86400) / 3600);
    return days > 0 ? `${days}d ${hours}h` : `${hours}h ${Math.floor((secs % 3600) / 60)}m`;
}

/** stateChip renders a job state as a coloured chip. */
export function stateChip(state) {
    const tone = {
        running: 'chip--cyan', queued: 'chip--warn', succeeded: 'chip--good',
        failed: 'chip--bad', stopped: '',
    }[state] || '';
    return el('span', { class: `chip ${tone}` }, state);
}

/** stateDot renders a job state as a status dot. */
export function stateDot(state) {
    const tone = {
        running: 'dot--run', queued: 'dot--run', succeeded: 'dot--on',
        failed: 'dot--bad', stopped: 'dot--off',
    }[state] || 'dot--off';
    return el('span', { class: `dot ${tone}` });
}

/** meter renders a labelled proportional bar. */
export function meter(label, value, max, text, tone = '#67e8f9') {
    const pct = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0;
    return el('div', { class: 'bar' },
        el('div', {
            class: 'bar__fill',
            style: `width:${pct}%;background:linear-gradient(90deg,${tone}33,${tone}55);` +
                   `box-shadow:0 0 14px ${tone}22`,
        }),
        el('div', { class: 'bar__text' },
            el('span', { style: 'color:#94a3b8' }, label),
            el('strong', { style: 'color:#e2e8f0' }, text)));
}

/** initials builds a two-letter badge from a name. */
export function initials(name) {
    return String(name || '?').split(/[\s\-_.]+/).filter(Boolean)
        .map((p) => p[0]).join('').slice(0, 2).toUpperCase() || '?';
}

/** icon returns the character used for a file or folder in the tree. */
export function fileIcon(name, isDir) {
    if (isDir) return '▸';
    const ext = name.includes('.') ? name.split('.').pop().toLowerCase() : '';
    return { py: '·', ipynb: '·', md: '·', json: '·', yaml: '·', yml: '·' }[ext] || '·';
}
