// The demonstration must be the real interface, not a copy of it.
//
// Its whole value is that it cannot drift: if a view is added to the product
// and missing here, the demo silently stops showing part of the app and nobody
// notices until somebody is shown the wrong thing. So this checks that every
// module the product ships is present, and that the only difference is the
// stubbed node.

import { readdirSync, existsSync, readFileSync } from 'node:fs';
import { join, relative } from 'node:path';

const DEMO = process.env.DEMO_DIR || 'dist/demo';
// index.html stays at the root: it is the one file the CDN does not cache, and
// it is what points at the newest build.
const INDEX = process.env.DEMO_INDEX || 'dist/demo/index.html';

const problems = [];

function walk(dir) {
    const out = [];
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) out.push(...walk(path));
        else out.push(path);
    }
    return out;
}

const shipped = walk('web')
    .filter((file) => !file.endsWith('embed.go') && !file.endsWith('web/index.html'))
    .map((file) => relative('web', file));

for (const file of shipped) {
    if (!existsSync(join(DEMO, file))) {
        problems.push(`dist/demo is missing ${file}, which the product ships`);
    }
}

// The mark is served by a Go route in the product rather than a file in web/,
// so the demo carries its own copy and would otherwise lose its favicon
// silently.
if (!existsSync(`${DEMO}/brand/plainshow-icon.webp`)) {
    problems.push('dist/demo is missing the brand mark that the product serves from a route');
}

// The stub has to load before the interface, or the first request reaches a
// node that is not there and the page renders an error instead of the app.
const page = readFileSync(INDEX, 'utf8');
if (page.indexOf('demo-api.js') > page.indexOf('app.js"')) {
    problems.push('demo/index.html loads app.js before the stub, so the first request escapes');
}
if (!page.includes('demo-api.js')) {
    problems.push('demo/index.html does not load the stub at all');
}

if (problems.length) {
    console.error('demo check failed:\n' + problems.map((p) => '  ' + p).join('\n'));
    process.exit(1);
}
console.log(`demo check passed — ${shipped.length} files, the interface is the real one`);
