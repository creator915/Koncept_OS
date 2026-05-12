// Tile type refs (must match)
var TILE_AIR=0,TILE_DIRT=1,TILE_STONE=2,TILE_GRASS=3,TILE_SAND=4,TILE_CLAY=5;
var TILE_COPPER=6,TILE_IRON=7,TILE_SILVER=8,TILE_GOLD=9,TILE_WOOD=10,TILE_LEAVES=11;
var TILE_PLANKS=12,TILE_TORCH=13,TILE_WORKBENCH=14,TILE_FURNACE=15,TILE_ANVIL=16,TILE_CHEST=17,TILE_PLATFORM=18;

var TILE_COLORS = {};
TILE_COLORS[1]='#8B5E3C';TILE_COLORS[2]='#808080';TILE_COLORS[3]='#4CAF50';TILE_COLORS[4]='#F4D03F';
TILE_COLORS[5]='#C0392B';TILE_COLORS[6]='#E67E22';TILE_COLORS[7]='#95A5A6';TILE_COLORS[8]='#BDC3C7';
TILE_COLORS[9]='#F1C40F';TILE_COLORS[10]='#A0522D';TILE_COLORS[11]='#2E7D32';TILE_COLORS[12]='#C49A6C';
TILE_COLORS[13]='#FFEB3B';TILE_COLORS[14]='#BCAAA4';TILE_COLORS[15]='#616161';TILE_COLORS[16]='#757575';
TILE_COLORS[17]='#8D6E63';TILE_COLORS[18]='#A1887F';

var ENEMY_DEFS = {
  0:{name:'Green Slime',hp:14,damage:6,defense:2,width:14,height:12,ai:'slime',color:'#4caf50'},
  1:{name:'Zombie',hp:45,damage:14,defense:4,width:14,height:24,ai:'walker',color:'#8d6e63'},
  2:{name:'Flying Eye',hp:60,damage:18,defense:6,width:16,height:16,ai:'flyer',color:'#b71c1c'},
  3:{name:'Blue Slime',hp:25,damage:8,defense:3,width:14,height:12,ai:'slime',color:'#2196f3'},
  4:{name:'Skeleton',hp:60,damage:20,defense:8,width:14,height:26,ai:'walker',color:'#e0e0e0'},
  5:{name:'Cave Bat',hp:16,damage:12,defense:2,width:12,height:10,ai:'flyer',color:'#5d4037'},
};

var ITEMS = {
  0:{name:'Copper Coin',maxStack:999},1:{name:'Dirt Block',maxStack:999},2:{name:'Stone Block',maxStack:999},
  3:{name:'Sand Block',maxStack:999},4:{name:'Clay Block',maxStack:999},5:{name:'Wood',maxStack:999},
  6:{name:'Copper Ore',maxStack:999},7:{name:'Iron Ore',maxStack:999},8:{name:'Silver Ore',maxStack:999},
  9:{name:'Gold Ore',maxStack:999},10:{name:'Copper Bar',maxStack:99},11:{name:'Iron Bar',maxStack:99},
  12:{name:'Silver Bar',maxStack:99},13:{name:'Gold Bar',maxStack:99},14:{name:'Wood Planks',maxStack:999},
  15:{name:'Torch',maxStack:99},16:{name:'Work Bench',maxStack:99},17:{name:'Furnace',maxStack:99},
  18:{name:'Anvil',maxStack:99},19:{name:'Wood Platform',maxStack:999},
  20:{name:'Copper Pickaxe',maxStack:1,type:'pickaxe'},21:{name:'Copper Axe',maxStack:1,type:'axe'},
  22:{name:'Copper Sword',maxStack:1,type:'sword'},23:{name:'Iron Pickaxe',maxStack:1,type:'pickaxe'},
  24:{name:'Iron Axe',maxStack:1,type:'axe'},25:{name:'Iron Sword',maxStack:1,type:'sword'},
  26:{name:'Silver Pickaxe',maxStack:1,type:'pickaxe'},27:{name:'Silver Sword',maxStack:1,type:'sword'},
  28:{name:'Gold Pickaxe',maxStack:1,type:'pickaxe'},29:{name:'Gold Sword',maxStack:1,type:'sword'},
  30:{name:'Wood Bow',maxStack:1,type:'bow'},31:{name:'Wooden Arrow',maxStack:999,type:'ammo'},
  32:{name:'Gel',maxStack:99},33:{name:'Lens',maxStack:99},
  34:{name:'Wood Helmet',maxStack:1,type:'helmet'},35:{name:'Wood Chestplate',maxStack:1,type:'chestplate'},
  36:{name:'Wood Boots',maxStack:1,type:'boots'},37:{name:'Copper Helmet',maxStack:1,type:'helmet'},
  38:{name:'Copper Chestplate',maxStack:1,type:'chestplate'},39:{name:'Copper Boots',maxStack:1,type:'boots'},
  40:{name:'Iron Helmet',maxStack:1,type:'helmet'},41:{name:'Iron Chestplate',maxStack:1,type:'chestplate'},
  42:{name:'Iron Boots',maxStack:1,type:'boots'},43:{name:'Lesser Healing Potion',maxStack:30,type:'potion'},
};

var SKY_DAY='#87CEEB', SKY_NIGHT='#1a1a2e', SKY_DAWN='#FF7F50', SKY_DUSK='#FF6347';

const interpolateColor = (c1, c2, t) => {
  t=Math.max(0,Math.min(1,t));
  const r1=parseInt(c1.slice(1,3),16),g1=parseInt(c1.slice(3,5),16),b1=parseInt(c1.slice(5,7),16);
  const r2=parseInt(c2.slice(1,3),16),g2=parseInt(c2.slice(3,5),16),b2=parseInt(c2.slice(5,7),16);
  return '#'+[Math.round(r1+(r2-r1)*t),Math.round(g1+(g2-g1)*t),Math.round(b1+(b2-b1)*t)].map(v=>v.toString(16).padStart(2,'0')).join('');
};

const getSkyColor = (time) => {
  const t=time.ticks/time.dayLength;
  if(time.isDawn)return interpolateColor(SKY_NIGHT,SKY_DAWN,(t%0.02)/0.02);
  if(time.isDusk)return interpolateColor(SKY_DUSK,SKY_NIGHT,((t-0.53)%0.02)/0.02);
  if(time.isDaytime){if(t<0.1)return interpolateColor(SKY_DAWN,SKY_DAY,t/0.1);if(t>0.45)return interpolateColor(SKY_DAY,SKY_DUSK,(t-0.45)/0.08);return SKY_DAY;}
  return SKY_NIGHT;
};

function renderFrame(rs) {
  const {ctx,world,player,enemies,particles,time,input,camera,canvasWidth,canvasHeight}=rs;
  const TS=16;
  ctx.fillStyle=getSkyColor(time); ctx.fillRect(0,0,canvasWidth,canvasHeight);
  // Stars
  if(!time.isDaytime){ctx.fillStyle='#fff';const sox=camera.x*0.1;for(let i=0;i<80;i++){const sx=((i*137+50)%canvasWidth+sox)%canvasWidth,sy=(i*73+20)%(canvasHeight*0.6);ctx.globalAlpha=0.3+0.7*Math.abs(Math.sin(time.ticks*0.01+i));ctx.fillRect(sx,sy,1.5,1.5);}ctx.globalAlpha=1;}
  // Sun/Moon
  const df=time.ticks/time.dayLength;
  if(time.isDaytime){const sx=canvasWidth*0.5+Math.cos(df*Math.PI*2)*canvasWidth*0.4,sy=canvasHeight*0.5-Math.sin(df*Math.PI*2)*canvasHeight*0.35;ctx.fillStyle='#FFD700';ctx.beginPath();ctx.arc(sx,sy,18,0,Math.PI*2);ctx.fill();}
  else{const sx=canvasWidth*0.5+Math.cos(df*Math.PI*2)*canvasWidth*0.4,sy=canvasHeight*0.5-Math.sin(df*Math.PI*2)*canvasHeight*0.35;ctx.fillStyle='#F5F5DC';ctx.beginPath();ctx.arc(sx,sy,14,0,Math.PI*2);ctx.fill();}
  // Tiles
  const sTX=Math.max(0,Math.floor(camera.x/TS)-1),eTX=Math.min(world.width-1,Math.floor((camera.x+canvasWidth)/TS)+1);
  const sTY=Math.max(0,Math.floor(camera.y/TS)-1),eTY=Math.min(world.height-1,Math.floor((camera.y+canvasHeight)/TS)+1);
  for(let tx=sTX;tx<=eTX;tx++)for(let ty=sTY;ty<=eTY;ty++){
    const tile=world.tiles[tx][ty];if(!tile||!tile.isActive)continue;
    const sx=tx*TS-camera.x,sy=ty*TS-camera.y;if(sx+TS<0||sx>canvasWidth||sy+TS<0||sy>canvasHeight)continue;
    ctx.fillStyle=TILE_COLORS[tile.type]||'#888';ctx.fillRect(sx,sy,TS,TS);
    ctx.strokeStyle='rgba(0,0,0,0.15)';ctx.strokeRect(sx,sy,TS,TS);
    if(tile.type===TILE_GRASS){ctx.fillStyle='#66BB6A';ctx.fillRect(sx,sy,TS,3);}
    else if(tile.type===TILE_TORCH){ctx.fillStyle='#FF9800';ctx.fillRect(sx+6,sy+2,4,6);ctx.fillStyle='#FFEB3B';ctx.fillRect(sx+7,sy+1,2,4);}
    else if(tile.type===TILE_CHEST){ctx.fillStyle='#FFD700';ctx.fillRect(sx+4,sy+4,8,3);}
    if(tile.type===TILE_COPPER||tile.type===TILE_IRON||tile.type===TILE_SILVER||tile.type===TILE_GOLD){if(Math.random()<0.05){ctx.fillStyle='rgba(255,255,255,0.5)';ctx.fillRect(sx+Math.random()*12,sy+Math.random()*12,2,2);}}
  }
  // Enemies
  for(const en of enemies){
    if(en.hp<=0)continue;const sx=en.x-en.width/2-camera.x,sy=en.y-en.height/2-camera.y;
    if(sx+en.width<0||sx>canvasWidth||sy+en.height<0||sy>canvasHeight)continue;
    const def=ENEMY_DEFS[en.type],color=def?def.color:'#f00',hpF=en.hp/en.hpMax;
    ctx.fillStyle=color;
    if(def&&def.ai==='slime'){ctx.beginPath();ctx.ellipse(sx+en.width/2,sy+en.height*0.7,en.width/2,en.height*0.5,0,0,Math.PI*2);ctx.fill();}
    else if(def&&def.ai==='flyer'){ctx.fillRect(sx+2,sy+2,en.width-4,en.height-4);ctx.fillStyle='rgba(255,255,255,0.3)';const wf=Math.sin(time.ticks*0.3)*4;ctx.fillRect(sx-2,sy-2+wf,4,en.height/2);ctx.fillRect(sx+en.width-2,sy-2-wf,4,en.height/2);}
    else{ctx.fillRect(sx,sy,en.width,en.height);ctx.fillRect(sx+2,sy-4,en.width-4,6);}
    ctx.fillStyle='#fff';const ex=en.facing>0?sx+en.width-5:sx+3;ctx.fillRect(ex,sy+3,3,3);ctx.fillStyle='#000';ctx.fillRect(ex+1,sy+4,1,1);
    if(hpF<1){const bw=en.width,bh=3,by=sy-8;ctx.fillStyle='#400';ctx.fillRect(sx,by,bw,bh);ctx.fillStyle=hpF>0.5?'#0f0':hpF>0.25?'#ff0':'#f00';ctx.fillRect(sx,by,bw*hpF,bh);}
  }
  // Player
  {
    const sx=player.x-player.width/2-camera.x,sy=player.y-player.height/2-camera.y;
    if(player.immunityTimer>0&&Math.floor(player.immunityTimer/4)%2===0)ctx.globalAlpha=0.4;
    ctx.fillStyle='#FFD54F';ctx.fillRect(sx+2,sy+8,player.width-4,player.height-12);
    ctx.fillStyle='#FFCC80';ctx.fillRect(sx+3,sy,player.width-6,10);
    ctx.fillStyle='#fff';const ex=player.facing>0?sx+player.width-6:sx+3;ctx.fillRect(ex,sy+2,4,3);ctx.fillStyle='#000';ctx.fillRect(ex+1,sy+3,2,1);
    ctx.fillStyle='#5D4037';ctx.fillRect(sx+3,sy+player.height-6,6,6);ctx.fillRect(sx+player.width-9,sy+player.height-6,6,6);
    const ss=player.inventory[player.hotbarSlot];if(ss&&ss.count>0){const it=ITEMS[ss.itemId];if(it&&it.type==='sword'&&player.attackCooldown>12){ctx.strokeStyle='#ccc';ctx.lineWidth=2;const sa=player.facing>0?-0.5:Math.PI+0.5;ctx.beginPath();ctx.arc(sx+player.width/2,sy+player.height/2,20,sa,sa+1.5);ctx.stroke();}}
    ctx.globalAlpha=1;
  }
  // Particles
  for(const p of particles){const sx=p.x-camera.x,sy=p.y-camera.y;if(sx<-4||sx>canvasWidth+4||sy<-4||sy>canvasHeight+4)continue;ctx.fillStyle=p.color;ctx.globalAlpha=Math.max(0,p.life/p.maxLife);ctx.fillRect(sx-p.size/2,sy-p.size/2,p.size,p.size);}
  ctx.globalAlpha=1;
  // Target indicator
  if(input.mouseLeft){const tx=input.mouseTileX,ty=input.mouseTileY,sx=tx*TS-camera.x,sy=ty*TS-camera.y;if(Math.sqrt((tx*TS+TS/2-player.x)**2+(ty*TS+TS/2-player.y)**2)<TS*5&&tx>=0&&tx<world.width&&ty>=0&&ty<world.height&&world.tiles[tx][ty].isActive){ctx.strokeStyle='#ff0';ctx.lineWidth=2;ctx.strokeRect(sx,sy,TS,TS);}}
  // HUD
  const hx=10,hy=canvasHeight-60;ctx.fillStyle='rgba(0,0,0,0.6)';ctx.fillRect(hx-2,hy-2,204,24);ctx.fillStyle='#600';ctx.fillRect(hx,hy,200,12);
  const hpf=player.hp/player.hpMax;ctx.fillStyle=hpf>0.5?'#0f0':hpf>0.25?'#ff0':'#f00';ctx.fillRect(hx,hy,200*hpf,12);
  ctx.fillStyle='#fff';ctx.font='10px monospace';ctx.fillText('HP: '+Math.ceil(player.hp)+'/'+player.hpMax,hx+4,hy+10);
  ctx.fillText((time.isDaytime?'Day ':'Night ')+time.dayNumber,canvasWidth-120,20);
  // Hotbar
  const bx=canvasWidth/2-160,by=canvasHeight-40;ctx.fillStyle='rgba(0,0,0,0.7)';ctx.fillRect(bx-2,by-2,324,38);
  for(let i=0;i<10;i++){const sx=bx+i*32,slot=player.inventory[i];if(i===player.hotbarSlot){ctx.strokeStyle='#FFD700';ctx.lineWidth=2;ctx.strokeRect(sx,by,32,34);}ctx.strokeStyle='#555';ctx.lineWidth=1;ctx.strokeRect(sx,by,32,34);if(slot&&slot.count>0){const it=ITEMS[slot.itemId];if(it){ctx.fillStyle='#fff';ctx.font='7px monospace';ctx.fillText(it.name.substring(0,8),sx+2,by+12);ctx.fillText(''+slot.count,sx+2,by+28);}}}
  ctx.fillStyle='#F1C40F';ctx.font='12px monospace';ctx.fillText('Coins: '+player.coins,hx,hy+30);
}
