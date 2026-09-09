const DEMO = process.env.DEMO_DIR || 'dist/demo';

// Boot the real interface against the demo stub and fail if it throws.
//
// This exists because "web check passed" did not mean the page worked. The
// module graph resolved, every file parsed, and the app still died on load with
// `activityStrip is not defined` — a call added without its import. Nothing
// that reads files can see that; only running it can.
//
// The DOM here is the smallest thing the shell touches on its way up. It is not
// a browser and does not try to be: it exists to reach the first line that
// throws, which is the failure this is for.

import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';

const LOGIN_DEMO = process.env.DEMO_LOGIN === '1';

const element = (tag) => {
    const node = {
        tagName: tag, children: [], dataset: {}, style: {}, attributes: {},
        classList: {
            _set: new Set(),
            add(...names) { names.forEach((name) => this._set.add(name)); },
            remove(...names) { names.forEach((name) => this._set.delete(name)); },
            toggle(name, on) { on ? this._set.add(name) : this._set.delete(name); },
            contains(name) { return this._set.has(name); },
        },
        append(...kids) { node.children.push(...kids); },
        appendChild(kid) { node.children.push(kid); return kid; },
        // removeChild has to really remove: ui.js clears a node with
        // `while (node.firstChild) node.removeChild(node.firstChild)`, so a
        // no-op here spins forever and the check hangs with no output.
        removeChild(kid) {
            const at = node.children.indexOf(kid);
            if (at >= 0) node.children.splice(at, 1);
            return kid;
        },
        remove() {}, replaceChildren() { node.children = []; },
        setAttribute(key, value) { node.attributes[key] = value; },
        getAttribute(key) { return node.attributes[key]; },
        addEventListener() {}, removeEventListener() {}, focus() {}, select() {},
        querySelector: () => null, querySelectorAll: () => [],
        get firstChild() { return node.children[0] || null; },
        set textContent(value) { node._text = value; },
        get textContent() { return node._text || ''; },
        set innerHTML(value) { node._html = value; },
        set className(value) { node._class = value; },
        get className() { return node._class || ''; },
    };
    return node;
};

const known = {};
const rendered = [];
globalThis.Node = function Node() {};
globalThis.document = {
    createElement: element,
    // Every string the interface renders is recorded. Walking the tree to find
    // one afterwards proved unreliable — this shim is not a DOM and its
    // textContent does not aggregate — and the text is what the check is
    // actually looking for.
    createTextNode: (text) => {
        rendered.push(String(text));
        return { nodeType: 3, textContent: String(text) };
    },
    getElementById: (id) => (known[id] ||= element('div')),
    querySelectorAll: () => [],
    addEventListener() {},
    body: element('body'),
};
// Handlers are kept rather than dropped: the router listens for hashchange,
// and every page after the first is only reachable by firing it.
const handlers = {};
globalThis.window = {
    addEventListener(name, fn) { (handlers[name] ||= []).push(fn); },
    removeEventListener() {},
    location: {
        hash: '', protocol: 'https:', host: 'demo',
        search: LOGIN_DEMO ? '?login=1' : '', reload() {},
    },
};
globalThis.location = globalThis.window.location;
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.Response = class {
    constructor(body, init = {}) { this._body = body; this.status = init.status || 200; }
    async text() { return this._body; }
    async json() { return JSON.parse(this._body); }
    get ok() { return this.status < 400; }
};
// Timers run, but immediately. The stub answers after a short delay to look
// like a network, so a setTimeout that never fires leaves boot() waiting on its
// first request forever — which looks exactly like success and is how the first
// version of this check passed while the page was broken.
//
// Intervals are dropped: they are polls and reconnects, and nothing here needs
// a second round.
const realTimeout = globalThis.setTimeout;
const realClear = globalThis.clearTimeout;
// Left as the real thing. Forcing every delay to zero turns any code that
// reschedules itself into a tight loop that starves the event loop, and the
// process then hangs with no output at all — which is how the second version of
// this check failed. The stub's own delays are a few hundred milliseconds.
globalThis.setTimeout = realTimeout;
globalThis.setInterval = () => 0;
globalThis.clearTimeout = () => {};
globalThis.clearInterval = () => {};

const stub = readFileSync(`${DEMO}/demo-api.js`, 'utf8');
new Function('window', 'document', 'Response', stub)(
    globalThis.window, globalThis.document, globalThis.Response);
globalThis.fetch = globalThis.window.fetch;
globalThis.WebSocket = globalThis.window.WebSocket;

let failure = null;
process.on('unhandledRejection', (error) => { failure ||= error; });

// A retry loop in the interface would otherwise keep this alive forever.
const watchdog = realTimeout(() => {
    console.error('demo boot failed:\n  the interface never finished loading');
    process.exit(1);
}, 15000);

try {
    await import(pathToFileURL(resolve(`${DEMO}/app.js`)).href);
    // boot() is async and reaches the network several times on its way up, so
    // this waits in real time rather than counting microtasks.
    await new Promise((done) => realTimeout(done, 3000));
} catch (error) {
    failure = error;
}
realClear(watchdog);

if (LOGIN_DEMO && !failure) {
    for (const phrase of ['Welcome to PlainShow', 'Sign in',
        'New to PlainShow?', 'Create an account']) {
        if (!rendered.some((text) => text.includes(phrase))) {
            failure = new Error(`the login demo did not render ${JSON.stringify(phrase)}`);
            break;
        }
    }
    for (const phrase of ['Use your Pi username and password.', 'Bootstrap token']) {
        if (rendered.some((text) => text.includes(phrase))) {
            failure = new Error(`the login demo rendered removed text ${JSON.stringify(phrase)}`);
            break;
        }
    }
}

if (!LOGIN_DEMO && !failure) {
    for (const phrase of ['Connected', 'Research lab', '100.64.0.1']) {
        if (!rendered.some((text) => text.includes(phrase))) {
            failure = new Error(`the sidebar connection card did not render ${JSON.stringify(phrase)}`);
            break;
        }
    }
}

if (LOGIN_DEMO) {
    if (failure) {
        console.error('login demo boot failed:\n  ' + (failure.message || failure));
        process.exit(1);
    }
    console.log('login demo boot passed — sign-in and account creation render');
    process.exit(0);
}

// Every page, not just the one it lands on.
//
// renderRoute catches whatever a view throws and renders "Could not load this
// page", so a broken view is not an unhandled rejection — it is a quiet panel
// that this check has to go looking for. Missing that is how a page shipped
// with an undefined function while the check reported success.
const pages = ['home', 'networks', 'projects', 'jobs', 'github',
    'devices', 'howto', 'settings', 'new', 'profile'];
if (!failure) {
    for (const page of pages) {
        globalThis.window.location.hash = `#/${page}`;
        for (const fire of handlers.hashchange || []) fire();
        await new Promise((done) => realTimeout(done, 400));
        if (rendered.some((text) => text.includes('Could not load this page'))) {
            failure = new Error(`the ${page} page failed to render`);
            break;
        }
    }
}

if (failure) {
    console.error('demo boot failed:\n  ' + (failure.message || failure));
    if (failure.stack) {
        console.error(failure.stack.split('\n').slice(1).join('\n'));
    }
    process.exit(1);
}
console.log('demo boot passed — the interface loads and renders');
