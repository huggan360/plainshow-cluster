// A small syntax highlighter for the editor.
//
// It covers Python, JavaScript, JSON, YAML, shell and Markdown well enough to
// make code readable, and degrades to plain text for anything else. A real
// language service belongs in a later phase; this is the honest amount of
// highlighting for an editor whose job today is "read and change a file".

const KEYWORDS = {
    py: ['and', 'as', 'assert', 'async', 'await', 'break', 'class', 'continue', 'def',
        'del', 'elif', 'else', 'except', 'False', 'finally', 'for', 'from', 'global',
        'if', 'import', 'in', 'is', 'lambda', 'None', 'nonlocal', 'not', 'or', 'pass',
        'raise', 'return', 'True', 'try', 'while', 'with', 'yield', 'self'],
    js: ['async', 'await', 'break', 'case', 'catch', 'class', 'const', 'continue',
        'default', 'delete', 'do', 'else', 'export', 'extends', 'false', 'finally',
        'for', 'from', 'function', 'if', 'import', 'in', 'instanceof', 'let', 'new',
        'null', 'of', 'return', 'super', 'switch', 'this', 'throw', 'true', 'try',
        'typeof', 'undefined', 'var', 'void', 'while', 'yield'],
    sh: ['if', 'then', 'else', 'elif', 'fi', 'for', 'in', 'do', 'done', 'while',
        'case', 'esac', 'function', 'return', 'export', 'local', 'echo', 'cd'],
};

const COMMENT = { py: '#', sh: '#', yaml: '#', js: '//', json: null, md: null };

/** languageOf maps a filename to a highlighting mode. */
export function languageOf(path = '') {
    const ext = path.includes('.') ? path.split('.').pop().toLowerCase() : '';
    if (['py', 'pyw'].includes(ext)) return 'py';
    if (['js', 'mjs', 'ts', 'tsx', 'jsx'].includes(ext)) return 'js';
    if (['sh', 'bash', 'zsh'].includes(ext)) return 'sh';
    if (['yaml', 'yml'].includes(ext)) return 'yaml';
    if (ext === 'json') return 'json';
    if (['md', 'markdown'].includes(ext)) return 'md';
    return 'text';
}

function escapeHTML(s) {
    return s.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));
}

/**
 * highlight renders source as HTML with token spans.
 *
 * It is a single left-to-right pass rather than a set of chained regex
 * replacements, because chained replacements corrupt each other: a keyword
 * inside a string, or a "#" inside a URL, gets coloured wrongly.
 */
export function highlight(source, lang) {
    if (lang === 'text' || lang === 'md') return escapeHTML(source);

    const keywords = new Set(KEYWORDS[lang] || KEYWORDS.js);
    const lineComment = COMMENT[lang];
    let out = '';
    let i = 0;

    const isWordChar = (c) => /[A-Za-z0-9_$]/.test(c);

    while (i < source.length) {
        const c = source[i];

        // Line comment through to the newline.
        if (lineComment && source.startsWith(lineComment, i)) {
            const end = source.indexOf('\n', i);
            const stop = end === -1 ? source.length : end;
            out += `<span class="tok-com">${escapeHTML(source.slice(i, stop))}</span>`;
            i = stop;
            continue;
        }

        // Strings, including triple-quoted Python and escapes.
        if (c === '"' || c === "'" || c === '`') {
            const triple = source.startsWith(c.repeat(3), i);
            const quote = triple ? c.repeat(3) : c;
            let j = i + quote.length;
            while (j < source.length) {
                if (source[j] === '\\') { j += 2; continue; }
                if (source.startsWith(quote, j)) { j += quote.length; break; }
                if (!triple && source[j] === '\n') break;
                j++;
            }
            out += `<span class="tok-str">${escapeHTML(source.slice(i, j))}</span>`;
            i = j;
            continue;
        }

        // Numbers.
        if (/[0-9]/.test(c) && (i === 0 || !isWordChar(source[i - 1]))) {
            let j = i;
            while (j < source.length && /[0-9a-fA-FxX._]/.test(source[j])) j++;
            out += `<span class="tok-num">${escapeHTML(source.slice(i, j))}</span>`;
            i = j;
            continue;
        }

        // Identifiers: keyword, call, or plain.
        if (isWordChar(c)) {
            let j = i;
            while (j < source.length && isWordChar(source[j])) j++;
            const word = source.slice(i, j);
            if (keywords.has(word)) {
                out += `<span class="tok-key">${escapeHTML(word)}</span>`;
            } else if (source[j] === '(') {
                out += `<span class="tok-fn">${escapeHTML(word)}</span>`;
            } else {
                out += escapeHTML(word);
            }
            i = j;
            continue;
        }

        out += escapeHTML(c);
        i++;
    }
    return out;
}
