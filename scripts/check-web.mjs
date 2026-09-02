// Verify the interface's module graph before it reaches a browser.
//
// ES modules fail at link time when a named import does not exist in the target
// module — and that failure is a blank page, not a helpful error. Node can
// parse these files but cannot run them (they touch the DOM), so this checks
// statically: every relative import resolves to a file, and every named import
// is actually exported by it.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join, dirname, resolve, relative } from 'node:path';

const ROOTS = ['web', 'adminweb'];
const problems = [];

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
        execFileSync(process.execPath, ['--check', file], { stdio: 'pipe' });
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

if (problems.length) {
    console.error('web check failed:\n' + problems.map((p) => '  ' + p).join('\n'));
    process.exit(1);
}
console.log(`web check passed — ${files.length} modules, imports all resolve`);
