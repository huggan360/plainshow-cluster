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
