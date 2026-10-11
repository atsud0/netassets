const http = require('node:http'); const fs = require('node:fs'); const path = require('node:path');
const root='/site';
http.createServer((req,res)=>{
 let name;try{name=decodeURIComponent(new URL(req.url,'http://localhost').pathname)}catch{res.writeHead(400);return res.end()}
 const file=path.resolve(root,'.'+(name==='/'?'/index.html':name));
 if(!file.startsWith(root+'/')){res.writeHead(403);return res.end()}
 fs.readFile(file,(error,body)=>{if(error){res.writeHead(404);return res.end('Not found')}res.writeHead(200,{'Content-Type':({'.html':'text/html; charset=utf-8','.png':'image/png','.json':'application/json','.txt':'text/plain; charset=utf-8','.md':'text/plain; charset=utf-8'})[path.extname(file)]||'application/octet-stream','Cache-Control':'no-store'});res.end(body)})
}).listen(8080,'0.0.0.0',()=>console.log('NetAssets UI proposal server ready'));
