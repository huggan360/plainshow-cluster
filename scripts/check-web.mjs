// Verify the interface's module graph before it reaches a browser.
//
// ES modules fail at link time when a named import does not exist in the target
// module — and that failure is a blank page, not a helpful error. Node can
// parse these files but cannot run them (they touch the DOM), so this checks
// statically: every relative import resolves to a file, and every named import
// is actually exported by it.

import { readFileSync, readdirSync, statSync, writeFileSync, unlinkSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join, dirname, resolve, relative } from 'node:path';
import { tmpdir } from 'node:os';

const ROOTS = ['web', 'adminweb'];
const problems = [];

/**
 * unbalanced reports the first bracket that closes something never opened, or
 * what is left open at the end. Strings, template literals and comments are
 * blanked first so only structural brackets are counted.
 */
/** walk lists every .js file under dir. */
function walk(dir) {
    return readdirSync(dir).flatMap((name) => {
        const path = join(dir, name);
        if (statSync(path).isDirectory()) return walk(path);
        return path.endsWith('.js') ? [path] : [];
    });
}

/** exportsOf collects the names a module exports. */
function exportsOf(source) {
    const names = new Set();
    const patterns = [
        /export\s+(?:async\s+)?function\s+([A-Za-z0-9_$]+)/g,
        /export\s+(?:const|let|var|class)\s+([A-Za-z0-9_$]+)/g,
    ];
    for (const re of patterns) {
        for (const m of source.matchAll(re)) names.add(m[1]);
    }
    // export { a, b as c }
    for (const m of source.matchAll(/export\s*\{([^}]*)\}/g)) {
        for (const part of m[1].split(',')) {
            const piece = part.trim();
            if (!piece) continue;
            names.add((piece.split(/\s+as\s+/).pop() || piece).trim());
        }
    }
    return names;
}

const files = ROOTS.flatMap(walk);
const exportCache = new Map();

if (!readFileSync('web/app.css', 'utf8').includes("url('./images/login-bg.webp')")) {
    problems.push('web/app.css does not use the bundled login background');
}
if (!readFileSync('web/app.css', 'utf8').includes("url('./images/sidebar-network-bg.webp')")) {
    problems.push('web/app.css does not use the bundled sidebar network background');
}
try {
    readFileSync('web/images/login-bg.webp');
    readFileSync('web/images/sidebar-network-bg.webp');
} catch {
    problems.push('one or more bundled interface backgrounds are missing');
}

for (const file of files) {
    const source = readFileSync(file, 'utf8');
    try {
        // Checked as a module, not a script.
        //
        // `node --check name.js` parses as CommonJS and is lenient about things
        // a module parse rejects: a file with one closing paren too many passed
        // here on Node 24 and was rejected by Node 26 in CI, so the gate stayed
        // silent until a release failed. Everything under web/ is loaded as a
        // module by the browser, so it is checked as one.
        const asModule = join(tmpdir(), `pscluster-check-${process.pid}.mjs`);
        try {
            writeFileSync(asModule, source);
            execFileSync(process.execPath, ['--check', asModule], { stdio: 'pipe' });
        } finally {
            try { unlinkSync(asModule); } catch { /* nothing to clean up */ }
        }
    } catch (error) {
        problems.push(`${file}: ${String(error.stderr || error.message).trim()}`);
    }

    for (const m of source.matchAll(/import\s*\{([^}]*)\}\s*from\s*'([^']+)'/g)) {
        const [, namesRaw, spec] = m;
        if (!spec.startsWith('.')) continue;

        const target = resolve(dirname(file), spec);
        let targetSource;
        try {
            targetSource = readFileSync(target, 'utf8');
        } catch {
            problems.push(`${file}: imports '${spec}', which does not exist`);
            continue;
        }
        if (!exportCache.has(target)) exportCache.set(target, exportsOf(targetSource));
        const available = exportCache.get(target);

        for (const part of namesRaw.split(',')) {
            const piece = part.trim();
            if (!piece) continue;
            const name = piece.split(/\s+as\s+/)[0].trim();
            if (!available.has(name)) {
                problems.push(
                    `${file}: imports { ${name} } from '${spec}', ` +
                    `but ${relative('.', target)} does not export it`);
            }
        }
    }
}

// Every module the page loads must be embedded in the binary.
for (const root of ROOTS) {
    const embedded = readFileSync(join(root, 'embed.go'), 'utf8');
    const patterns = (embedded.match(/\/\/go:embed (.+)/) || [, ''])[1].split(/\s+/);
    for (const file of files.filter((candidate) => candidate.startsWith(`${root}/`))) {
        const rel = relative(root, file);
        const top = rel.split('/')[0];
        if (!patterns.includes(rel) && !patterns.includes(top)) {
            problems.push(`${file} is not covered by the //go:embed patterns in ${root}/embed.go`);
        }
    }
}

// Authentication must stay on the current central-account contract. A stale
// bootstrap field leaves first-time registration unusable, and the browser
// must resolve the gate after setup so account creation signs straight in.
{
    const signin = readFileSync('web/views/signin.js', 'utf8');
    const admin = readFileSync('adminweb/app.js', 'utf8');
    for (const phrase of ['bootstrap_token', 'Bootstrap token',
        'Use your Pi username and password.']) {
        if (signin.includes(phrase) || admin.includes(phrase)) {
            problems.push(`authentication UI still contains removed text: ${phrase}`);
        }
    }
    for (const phrase of ['Welcome to PlainShow', '/api/auth/setup',
        '/api/auth/login', 'done();']) {
        if (!signin.includes(phrase)) {
            problems.push(`web/views/signin.js is missing its authentication contract: ${phrase}`);
        }
    }
}

// Opening a project constructs both the code pane and the imported Branch-tab
// builder. They must not share a lexical name: JavaScript then treats the DOM
// element as the function and fails only after somebody opens a project.
{
    const projects = readFileSync('web/views/projects.js', 'utf8');
    if (/\b(?:const|let|var)\s+branchPane\b/.test(projects)) {
        problems.push('web/views/projects.js shadows the imported branchPane builder');
    }
    if (!projects.includes("const projects = await api('/api/projects')")) {
        problems.push('web/views/projects.js opens projects from a stale overview snapshot');
    }
    if (projects.includes("throw new Error('No such local project.')")) {
        problems.push('web/views/projects.js still exposes the stale project lookup error');
    }
}

// The network Ray control is deliberately a single, centered state action.
// Status details belong on the Jobs page; adding them here turns the button
// back into an uneven dashboard card and obscures what clicking it does.
{
    const networks = readFileSync('web/views/networks.js', 'utf8');
    for (const phrase of ['ray-toggle__surface', 'ray-toggle__label',
        "localRunning ? 'Ray on' : 'Ray off'"]) {
        if (!networks.includes(phrase)) {
            problems.push(`web/views/networks.js is missing its Ray button contract: ${phrase}`);
        }
    }
    const rayMetric = networks.slice(networks.indexOf('function rayMetric'),
        networks.indexOf('function deviceCard'));
    if (rayMetric.includes('ps-metric-head') || rayMetric.includes('ps-metric-card__icon')) {
        problems.push('the network Ray button contains dashboard-card decoration');
    }
}

// The account service can lag a node version. This machine's card must prefer
// the live system snapshot so a count-only legacy response cannot show one GPU
// alongside an empty inventory and unknown cores/memory.
{
    const settings = readFileSync('web/views/settings.js', 'utf8');
    for (const phrase of ['isThis ? state.system', 'local?.gpus',
        'local?.cpu_cores', 'local?.ram_total_mb']) {
        if (!settings.includes(phrase)) {
            problems.push(`web/views/settings.js is missing local device inventory fallback: ${phrase}`);
        }
    }
}

// Home uses two viewport-filling panels. Networks belong to their own list,
// not the general three-column card grid that made the lower half uneven.
{
    const home = readFileSync('web/views/home.js', 'utf8');
    for (const phrase of ['ps-home-page', 'ps-home-network-list',
        'Notifications', 'Your networks', 'View more', 'wheelLocked',
        'ps-notice--first', 'ps-notice--last', '220']) {
        if (!home.includes(phrase)) {
            problems.push(`web/views/home.js is missing its lower-panel contract: ${phrase}`);
        }
    }
    if (!/ps-home-lower[^]*?activityPanel\(overview, rayJobs\),\s*networksPanel\(overview\)/.test(home)) {
        problems.push('web/views/home.js must render Notifications left of Your networks');
    }
    if (/ps-card-grid[^\n]*networkPreview/.test(home)) {
        problems.push('web/views/home.js still renders networks in the general card grid');
    }
}

// Every icon the interface asks for must have a class in the bundled stylesheet.
//
// The font is upstream's complete file, but only some class-to-codepoint rules
// were kept. A class with no rule renders as nothing — no error, no fallback,
// no gap in the layout — so a button simply stops having an icon and nobody
// notices until they look for it. Thirty-eight of them were missing at once.
{
    const sheet = readFileSync('web/boxicons.css', 'utf8');
    const defined = new Set(
        [...sheet.matchAll(/^\.(bxl?-[a-z0-9-]+):before/gm)].map((match) => match[1]));
    const used = new Map();
    for (const file of files.filter((candidate) => candidate.endsWith('.js'))) {
        const source = readFileSync(file, 'utf8');
        for (const match of source.matchAll(/\bbxl?-[a-z0-9-]+/g)) {
            if (!defined.has(match[0])) used.set(match[0], file);
        }
    }
    for (const [icon, file] of [...used].sort()) {
        problems.push(`${file}: uses ${icon}, which web/boxicons.css does not define`);
    }
}

if (problems.length) {
    console.error('web check failed:\n' + problems.map((p) => '  ' + p).join('\n'));
    process.exit(1);
}
console.log(`web check passed — ${files.length} modules, imports all resolve`);
