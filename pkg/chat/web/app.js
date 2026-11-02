'use strict';
const form=document.querySelector('#chat'),input=document.querySelector('#input'),messages=document.querySelector('#messages'),status=document.querySelector('#status'),send=document.querySelector('#send'),reset=document.querySelector('#reset'),suggestions=document.querySelector('#suggestions');
const spinner=document.querySelector('#spinner');
const vectorPanel=document.querySelector('#vector-panel'),vectorTitle=document.querySelector('#vector-title'),vectorResults=document.querySelector('#vector-results');
const downloadURLs=[];
window.addEventListener('pagehide',()=>downloadURLs.forEach(url=>URL.revokeObjectURL(url)));
let busy=false,completionVersion=0,completionTask=Promise.resolve();
function add(text,role){
 const article=document.createElement('article');article.className='message '+role;
 const label=document.createElement('span');label.className='label';label.textContent=role==='user'?'YOU':role==='error'?'ERROR':role==='warning'?'ADVISORY WARNING':role==='progress'?'VECTOR SEARCH':'JAR';
 article.append(label,document.createTextNode(text));messages.prepend(article);return article;
}
function setBusy(value){busy=value;send.disabled=reset.disabled=input.disabled=value;spinner.hidden=!value;form.setAttribute('aria-busy',String(value));if(value)status.textContent='Searching the index…';}
function showRetrieval(progress){
 const stages={searching:'Searching the index…',retrieved:'Vector search complete. Preparing the answer…',generating:'Generating a sourced answer…',validating:'Checking citations and personal names…'};
 status.textContent=progress.stage==='translating'?`Translating to ${progress.language} · part ${progress.part}/${progress.parts}…`:stages[progress.stage]||progress.stage;
 if(progress.stage==='searching'){vectorPanel.hidden=false;vectorTitle.textContent='LATEST VECTOR SEARCH';vectorResults.textContent='Searching the index…';}
 if(progress.stage!=='retrieved')return;
 const results=progress.results||[];
 vectorPanel.hidden=false;
 vectorTitle.textContent=`LATEST VECTOR SEARCH · ${progress.language} · ${results.length} ${results.length===1?'passage':'passages'}`;
 vectorResults.textContent=results.length?results.map(result=>`Candidate ${result.reference} · Chunk ${result.chunkId} · similarity ${result.similarity.toFixed(4)}\n${result.source}\n${result.text}`).join('\n\n'):'No matching passages.';
 vectorResults.scrollTop=0;
}

function showResult(result){
 if(result.text){
  const article=add(result.text,'assistant');
  if(result.downloadName){const link=document.createElement('a'),url=URL.createObjectURL(new Blob([result.downloadText||result.text],{type:'text/plain;charset=utf-8'}));downloadURLs.push(url);link.href=url;link.download=result.downloadName;link.className='download';link.textContent=result.downloadName.startsWith('reference-')?'Download reference and context (.txt)':'Download conference text (.txt)';article.insertBefore(link,article.childNodes[1]);}
 }
 for(const warning of result.warnings||[])add(warning,'warning');
 status.textContent=`Search: ${result.scope} · Language: ${result.language}`;
 if(result.quit)add('Use /reset to start again. The server remains running.','assistant');
}
async function readEvents(response,onEvent){
 const reader=response.body.getReader(),decoder=new TextDecoder();let buffer='';
 try{
  for(;;){const {done,value}=await reader.read();buffer+=decoder.decode(value,{stream:!done});let end;
   while((end=buffer.indexOf('\n'))>=0){const line=buffer.slice(0,end);buffer=buffer.slice(end+1);if(line.trim())onEvent(JSON.parse(line));}
   if(done)break;
  }
  if(buffer.trim())onEvent(JSON.parse(buffer));
 }finally{await reader.cancel();reader.releaseLock();}
}
async function submit(text){
 if(busy||!text.trim())return;
 completionVersion++;suggestions.replaceChildren();document.querySelector('details').open=false;
 add(text,'user');input.value='';setBusy(true);let receivedResult=false;
 try{
  const response=await fetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json','Accept':'application/x-ndjson'},body:JSON.stringify({message:text})});
  if(!response.ok)throw new Error(await response.text());
  await readEvents(response,event=>{
   if(event.type==='error')throw new Error(event.error);
   if(event.type==='progress'){
    showRetrieval(event.progress);
   }
   if(event.type==='result'){receivedResult=true;showResult(event.result);if(text.trim()==='/reset'||event.result.quit){vectorPanel.hidden=true;vectorResults.textContent='';}}
  });
  if(!receivedResult)throw new Error('The connection closed before the answer completed.');
 }catch(error){add(error.message,'error');status.textContent='Request failed. You can retry.';}
 finally{setBusy(false);input.focus({preventScroll:true});}
}
form.addEventListener('submit',async event=>{event.preventDefault();await completionTask;submit(input.value);});
reset.addEventListener('click',()=>submit('/reset'));
input.addEventListener('input',()=>{completionVersion++;suggestions.replaceChildren();});
async function completeInput(){const line=input.value,version=++completionVersion;try{const response=await fetch('/api/complete?line='+encodeURIComponent(line));if(!response.ok)throw new Error(await response.text());const {candidates}=await response.json();if(version!==completionVersion||input.value!==line)return;suggestions.replaceChildren();if(candidates.length===1){input.value=candidates[0];return;}for(const candidate of candidates){const button=document.createElement('button');button.type='button';button.textContent=candidate;button.addEventListener('click',()=>{input.value=candidate;suggestions.replaceChildren();input.focus();});suggestions.append(button);}}catch(error){status.textContent='Completion unavailable: '+error.message;}}
input.addEventListener('keydown',async event=>{if(event.key==='Enter'&&!event.shiftKey&&!event.isComposing){event.preventDefault();await completionTask;submit(input.value);return;}if(event.key==='Tab'&&input.value.startsWith('/')&&input.selectionStart===input.value.length){event.preventDefault();completionTask=completeInput();}});
fetch('/api/info').then(response=>{if(!response.ok)throw new Error('Cannot load commands');return response.json();}).then(info=>{document.querySelector('#help').textContent=info.help;document.querySelector('details').open=true;}).catch(error=>{document.querySelector('#help').textContent=error.message;});
input.focus();
