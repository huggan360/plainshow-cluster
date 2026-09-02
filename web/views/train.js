import { el, mount, ago, stateChip } from '../lib/ui.js';
import { api, toast, navigate } from '../lib/client.js';

export async function renderTrain(host) {
	const page=el('div',{class:'page'});mount(host,page);
	const [projects,datasets,runs,machines]=await Promise.all([api('/api/projects'),api('/api/datasets'),api('/api/training'),api('/api/machines')]);
	const project=el('select',{class:'select'},...projects.map(p=>el('option',{value:p.name},p.name)));
	const entry=el('input',{class:'input input--mono',value:'train.py',placeholder:'train.py --epochs 10'});
	const dataset=el('select',{class:'select'},el('option',{value:''},'No dataset'),...datasets.map(d=>el('option',{value:d.id},`${d.name} · ${d.version}`)));
	const bandwidth=el('input',{class:'input input--mono',type:'number',value:'100',min:'0.1'});
	const parameters=el('input',{class:'input input--mono',type:'number',value:'100000000',min:'1'});
	const nodeChecks=new Map();
	const nodeRows=machines.filter(n=>n.roles.includes('worker')).map(n=>{const check=el('input',{type:'checkbox',checked:true});nodeChecks.set(n.node_id||n.id,check);return el('label',{class:'switch'},el('span',{class:'switch__text'},el('strong',{},n.name),el('span',{},`${n.is_self?'this machine':'remote'} · ${n.arch}`)),check)});
	const result=el('div',{});
	const request=()=>({project:project.value,framework:'pytorch',entry:entry.value.trim(),machines:[...nodeChecks].filter(([,c])=>c.checked).map(([id])=>id),processes_per_node:1,dataset:dataset.value,parameters:Number(parameters.value),bandwidth_mbps:Number(bandwidth.value),observed_step_seconds:1});
	const preflight=async()=>{const check=await api('/api/training/preflight',{method:'POST',body:request()});mount(result,el('div',{class:check.ready?'frame frame--good':'panel'},el('div',{class:check.ready?'frame__in':''},el('div',{class:'panel__head'},check.ready?'Ready to launch':'Needs attention'),...(check.issues||[]).map(issue=>el('p',{class:'muted'},issue)),el('p',{class:'mono muted',style:'font-size:11px'},`Estimated communication: ${check.advice.communication_percent.toFixed(0)}% · ${check.advice.verdict}`))));return check.ready};
	const launch=async()=>{if(!await preflight())return;const run=await api('/api/training/run',{method:'POST',body:request()});toast(`Training run ${run.id.slice(0,8)} launched.`);location.reload()};
	mount(page,el('div',{class:'page__head'},el('p',{class:'page__eyebrow'},'Distributed AI'),el('h1',{class:'page__title'},'Train'),el('p',{class:'page__sub'},'Reserve every rank, prepare data, then launch PyTorch together. Plainshow shows when network communication is likely to erase the benefit.')),
		el('div',{class:'grid grid--2'},el('div',{class:'panel'},el('div',{class:'panel__head'},'Run specification'),field('Project',project),field('Entry point',entry),field('Dataset',dataset),el('div',{style:'display:grid;grid-template-columns:1fr 1fr;gap:10px'},field('Model parameters',parameters),field('Link Mbit/s',bandwidth)),el('div',{style:'display:flex;gap:8px'},el('button',{class:'btn',onclick:preflight},'Check first'),el('button',{class:'btn btn--primary',onclick:launch},'Start training')),result),
		el('div',{class:'panel'},el('div',{class:'panel__head'},'Gang reservation'),...nodeRows)),
		el('div',{class:'panel',style:'margin-top:14px'},el('div',{class:'panel__head'},'Training history'),...(runs.length?runs.map(run=>el('div',{class:'row'},el('span',{class:'row__main'},el('span',{class:'row__title'},`${run.framework} · ${run.ranks.length} ranks`),el('span',{class:'row__meta'},`${ago(run.created_at)} · ${run.id.slice(0,8)}`)),stateChip(run.state))):[el('div',{class:'empty'},el('span',{class:'empty__text'},'No distributed runs yet.'))])));
	return null;
}
function field(label,input){return el('div',{class:'field'},el('label',{class:'field__label'},label),input)}
