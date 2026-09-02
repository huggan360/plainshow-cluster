import { el, mount, bytes, ago } from '../lib/ui.js';
import { api, modal, toast } from '../lib/client.js';

export async function renderDatasets(host) {
	const page = el('div', { class: 'page' }); mount(host, page);
	const draw = async () => {
		const datasets = await api('/api/datasets');
		mount(page,
			el('div', { class: 'page__head', style: 'display:flex;align-items:flex-end;gap:16px' },
				el('div', { style: 'flex:1' }, el('p', { class: 'page__eyebrow' }, 'Data'),
					el('h1', { class: 'page__title' }, 'Datasets'),
					el('p', { class: 'page__sub' }, 'Immutable, versioned data. Identical chunks are stored once and copied directly to workers before a job starts.')),
				el('button', { class: 'btn btn--primary', onclick: () => register(draw) }, '+ Register dataset')),
			datasets.length ? el('div', { class: 'grid grid--3' }, ...datasets.map(card)) :
				el('div', { class: 'panel' }, el('div', { class: 'empty' }, el('span', { class: 'empty__ico' }, '◈'),
					el('span', { class: 'empty__text' }, 'Register a directory from this machine to create the first immutable dataset version.'))));
	};
	await draw(); return null;
}

function card(dataset) {
	return el('div', { class: 'panel' },
		el('div', { class: 'panel__head' }, el('span', { class: 'grow' }, dataset.version),
			el('span', { class: 'chip chip--good' }, `${dataset.placements.length} ready`)),
		el('div', { style: 'font-size:17px;font-weight:650' }, dataset.name),
		el('p', { class: 'mono muted', style: 'font-size:11px' }, `${dataset.file_count} files · ${bytes(dataset.size_bytes)}`),
		el('p', { class: 'mono dim', style: 'font-size:9.5px;overflow:hidden;text-overflow:ellipsis' }, `sha256:${dataset.root_hash}`),
		el('p', { class: 'muted', style: 'font-size:11px;margin-bottom:0' }, `registered ${ago(dataset.created_at)}`));
}

function register(redraw) {
	const name=el('input',{class:'input',placeholder:'cats-dogs'});
	const version=el('input',{class:'input',placeholder:'v1',value:'v1'});
	const source=el('input',{class:'input input--mono',placeholder:'/data/cats-dogs'});
	modal({title:'Register dataset',confirmLabel:'Hash and register',body:()=>el('div',{},
		el('div',{class:'field'},el('label',{class:'field__label'},'Name'),name),
		el('div',{class:'field'},el('label',{class:'field__label'},'Version'),version),
		el('div',{class:'field'},el('label',{class:'field__label'},'Directory on this machine'),source),
		el('p',{class:'muted',style:'font-size:12px;margin:0'},'Files are read once, chunked, checksummed, and kept in the node dataset store.')),
		onConfirm:async(close)=>{await api('/api/datasets',{method:'POST',body:{name:name.value.trim(),version:version.value.trim(),source:source.value.trim()}});close();toast('Dataset version registered.');await redraw();}});
}
