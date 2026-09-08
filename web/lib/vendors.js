// Marks for the three graphics vendors.
//
// Boxicons carries 155 brand icons and none of these, so they are drawn here.
// They are deliberately simplified rather than traced: at fourteen pixels a
// faithful logo is mud, and what actually does the work is the colour — green,
// red and blue are recognised before any shape is. The vendor's name is always
// beside the mark, so the icon confirms rather than has to be decoded.

import { el } from './ui.js';

const VENDORS = {
    nvidia: {
        label: 'NVIDIA',
        colour: '#76b900',
        // The eye: an outer lens with the inner curve cut out of it.
        path: 'M12 5.5c-4 0-7.2 2.6-8.4 6.5 1.2 3.9 4.4 6.5 8.4 6.5s7.2-2.6 8.4-6.5'
            + 'C19.2 8.1 16 5.5 12 5.5zm0 2.2c2.7 0 5 1.6 6.1 4.3-1.1 2.7-3.4 4.3-6.1 4.3'
            + 'V13.9c1.2 0 2-.7 2-1.9s-.8-1.9-2-1.9V7.7z',
    },
    amd: {
        label: 'AMD',
        colour: '#ed1c24',
        // The arrow: a corner bracket with a wedge driving out of it.
        path: 'M4 4h11.2l4.8 4.8V20h-3.4v-9.8L12.6 7.4H4V4zm0 5.6h6.6L4 16.2V9.6z',
    },
    intel: {
        label: 'Intel',
        colour: '#0068b5',
        // A lowercase i inside the rounded field the brand uses.
        path: 'M4 4h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2z'
            + 'm7.1 3.1v2.1h1.9V7.1h-1.9zm0 3.4V17h1.9v-6.5h-1.9z',
    },
};

/** vendorMark renders a graphics vendor's mark, or a generic chip. */
export function vendorMark(vendor) {
    const known = VENDORS[String(vendor || '').toLowerCase()];
    if (!known) {
        return el('i', { class: 'bx bx-chip ps-gpu-icon', 'aria-hidden': 'true' });
    }
    // Built through innerHTML because el() creates HTML elements, and an SVG
    // child has to be in the SVG namespace or the browser renders nothing.
    return el('span', {
        class: 'vendor-mark', title: known.label, 'aria-hidden': 'true',
        style: `color:${known.colour}`,
        html: `<svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor">`
            + `<path d="${known.path}"/></svg>`,
    });
}

/** vendorName is what to call a vendor in text. */
export function vendorName(vendor) {
    const known = VENDORS[String(vendor || '').toLowerCase()];
    return known ? known.label : (vendor || 'GPU');
}
