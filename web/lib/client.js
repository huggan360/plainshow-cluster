// The node client: HTTP, the event stream, shared state, toasts and dialogs.
//
// This module exists so the shell and the views do not import each other. A
// cycle between them "works" only because every reference happens inside a
// function that runs later; one top-level read of a shared binding turns it
// into a blank page. Everything shared lives here instead, and the graph stays
// one-directional: app -> views -> client.

// ------------------------------------------------------------------ api ----

/** api performs a JSON request and turns a failure into a readable Error. */
export async function api(path, options = {}) {
    const init = { headers: {}, ...options };
    if (init.body !== undefined && typeof init.body !== 'string') {
        init.headers['Content-Type'] = 'application/json';
        init.body = JSON.stringify(init.body);
    }
    let res;
    try {
        res = await fetch(path, init);
    } catch (cause) {
        throw new Error('The node is not responding. Is it still running?', { cause });
    }
    const text = await res.text();
    let data = null;
    if (text) {
        try { data = JSON.parse(text); } catch { data = null; }
    }
    if (!res.ok) {
        const error = new Error((data && data.error) || `Request failed (${res.status}).`);
        error.status = res.status;
        // A 401 anywhere means the session went away underneath us. Tell the
        // shell once, so one expired session does not become a page full of
        // identical failures.
        if (res.status === 401 && !path.startsWith('/api/auth/')) {
            unauthorized.forEach((handler) => handler());
        }
        throw error;
    }
    return data;
}

const unauthorized = new Set();

/** onUnauthorized runs when the node stops recognising this browser. */
export function onUnauthorized(handler) {
    unauthorized.add(handler);
    return () => unauthorized.delete(handler);
}

// ---------------------------------------------------------------- state ----

/** state holds what the whole interface reads. */
export const state = { overview: null, system: null };

/** refresh reloads the overview from the node. */
export async function refresh() {
    state.overview = await api('/api/overview');
    state.system = state.overview.system;
    return state.overview;
}

// ------------------------------------------------------------ event bus ----

const bus = new EventTarget();

/** on subscribes to a topic and returns an unsubscribe function. */
export function on(topic, handler) {
    const wrapped = (e) => handler(e.detail);
    bus.addEventListener(topic, wrapped);
    return () => bus.removeEventListener(topic, wrapped);
}

/** emit publishes a topic locally. Used by the socket, and by tests. */
export function emit(topic, data) {
    bus.dispatchEvent(new CustomEvent(topic, { detail: data }));
}

let socket = null;
let controllerSocket = null;
let attempts = 0;
let live = false;
const connectionListeners = new Set();
const outboxKey = 'plainshow.cluster.outbox.v1';
let outbox = [];
try { outbox = JSON.parse(localStorage.getItem(outboxKey) || '[]'); } catch { outbox = []; }

export function isLive() { return live; }

/** send carries browser-originated collaboration and presence messages. Text
 * edits are retained in localStorage while offline and replayed in order. */
export function send(topic, data, durable = false) {
    const message = { topic, data };
    if (socket && socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify(message));
        if (controllerSocket && controllerSocket.readyState === WebSocket.OPEN && topic.startsWith('collab.')) {
            controllerSocket.send(JSON.stringify(message));
        }
        return true;
    }
    if (durable) {
        outbox.push(message);
        localStorage.setItem(outboxKey, JSON.stringify(outbox));
    }
    return false;
}

/** onConnection observes whether the event stream is up. */
export function onConnection(handler) {
    connectionListeners.add(handler);
    handler(live);
    return () => connectionListeners.delete(handler);
}

function setLive(value) {
    live = value;
    connectionListeners.forEach((fn) => fn(live));
}

/** connect opens the event stream and keeps it open. */
export function connect() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    socket = new WebSocket(`${proto}://${location.host}/ws`);

    socket.onopen = () => {
        if (attempts > 0) toast('Reconnected to the node.');
        attempts = 0;
        setLive(true);
        const pending = outbox;
        outbox = [];
        localStorage.setItem(outboxKey, '[]');
        pending.forEach((message) => socket.send(JSON.stringify(message)));
        connectController();
    };
    socket.onmessage = (event) => {
        let msg;
        try { msg = JSON.parse(event.data); } catch { return; }
        // Telemetry is shared state, so keep it current for every view whether
        // or not anything is listening for the topic right now.
        if (msg.topic === 'system') state.system = msg.data;
        emit(msg.topic, msg.data);
    };
    socket.onclose = () => {
        setLive(false);
        // Back off to a few seconds: quick enough to catch a restarting node,
        // slow enough not to spin while it is down.
        attempts += 1;
        setTimeout(connect, Math.min(1000 * attempts, 5000));
    };
    socket.onerror = () => socket.close();
}

function connectController() {
    const target = state.overview && state.overview.controller;
    if (!target || !target.ws_url || (controllerSocket && controllerSocket.readyState < 2)) return;
    controllerSocket = new WebSocket(target.ws_url);
    controllerSocket.onmessage = (event) => {
        // The sending browser already delivered the operation to its node.
        // Other browsers deliver the controller copy to their own node, which
        // writes it to that clone and publishes the normal local event.
        if (socket && socket.readyState === WebSocket.OPEN) socket.send(event.data);
    };
    controllerSocket.onclose = () => setTimeout(connectController, 3000);
    controllerSocket.onerror = () => controllerSocket.close();
}

// --------------------------------------------------------------- toasts ----

/** toast shows a transient message. Errors linger; confirmations do not. */
export function toast(message, kind = 'ok') {
    const host = document.getElementById('toasts');
    if (!host) return;
    const close = () => node.remove();
    const dismiss = document.createElement('button');
    dismiss.className = 'toast__x';
    dismiss.title = 'Dismiss';
    dismiss.setAttribute('aria-label', 'Dismiss');
    dismiss.textContent = '×';
    dismiss.addEventListener('click', close);

    const node = document.createElement('div');
    node.className = `toast toast--${kind}`;
    const label = document.createElement('span');
    label.textContent = message;
    node.append(label, dismiss);

    host.append(node);
    setTimeout(close, kind === 'err' ? 8000 : 3500);
}

// ---------------------------------------------------------------- modal ----

/** modal opens a dialog. body(close) builds its contents. */
export function modal({ title, body, confirmLabel = 'Confirm', danger = false, onConfirm }) {
    const host = document.getElementById('modal');
    const close = () => {
        host.replaceChildren();
        document.removeEventListener('keydown', onKey);
    };
    const onKey = (e) => { if (e.key === 'Escape') close(); };
    document.addEventListener('keydown', onKey);

    const confirm = document.createElement('button');
    confirm.className = `btn ${danger ? 'btn--danger' : 'btn--primary'}`;
    confirm.textContent = confirmLabel;
    confirm.addEventListener('click', async () => {
        confirm.disabled = true;
        try {
            await onConfirm(close);
        } catch (err) {
            toast(err.message, 'err');
            confirm.disabled = false;
        }
    });

    const cancel = document.createElement('button');
    cancel.className = 'btn';
    cancel.textContent = 'Cancel';
    cancel.addEventListener('click', close);

    const head = document.createElement('div');
    head.className = 'modal__head';
    head.textContent = title;

    const bodyEl = document.createElement('div');
    bodyEl.className = 'modal__body';
    bodyEl.append(body(close));

    const foot = document.createElement('div');
    foot.className = 'modal__foot';
    foot.append(cancel, confirm);

    const box = document.createElement('div');
    box.className = 'modal';
    box.append(head, bodyEl, foot);

    const scrim = document.createElement('div');
    scrim.className = 'scrim';
    scrim.append(box);
    scrim.addEventListener('click', (e) => { if (e.target === scrim) close(); });

    host.replaceChildren(scrim);
    const first = scrim.querySelector('input, textarea, select');
    if (first) first.focus();
    return close;
}

// --------------------------------------------------------------- router ----

/** navigate changes route. */
export function navigate(path) {
    location.hash = `#/${path}`;
}
