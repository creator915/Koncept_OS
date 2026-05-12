// Tile type constants (must match InitWorld)
var TILE_AIR = 0, TILE_DIRT = 1, TILE_STONE = 2, TILE_GRASS = 3, TILE_SAND = 4, TILE_CLAY = 5;
var TILE_COPPER = 6, TILE_IRON = 7, TILE_SILVER = 8, TILE_GOLD = 9, TILE_WOOD = 10, TILE_LEAVES = 11;
var TILE_PLANKS = 12, TILE_TORCH = 13, TILE_WORKBENCH = 14, TILE_FURNACE = 15, TILE_ANVIL = 16, TILE_CHEST = 17, TILE_PLATFORM = 18;

// Item definitions
var ITEMS = {
  0:{name:'Copper Coin',maxStack:999}, 1:{name:'Dirt Block',maxStack:999,placeTile:1},
  2:{name:'Stone Block',maxStack:999,placeTile:2}, 3:{name:'Sand Block',maxStack:999,placeTile:4},
  4:{name:'Clay Block',maxStack:999,placeTile:5}, 5:{name:'Wood',maxStack:999,placeTile:10},
  6:{name:'Copper Ore',maxStack:999}, 7:{name:'Iron Ore',maxStack:999},
  8:{name:'Silver Ore',maxStack:999}, 9:{name:'Gold Ore',maxStack:999},
  10:{name:'Copper Bar',maxStack:99}, 11:{name:'Iron Bar',maxStack:99},
  12:{name:'Silver Bar',maxStack:99}, 13:{name:'Gold Bar',maxStack:99},
  14:{name:'Wood Planks',maxStack:999,placeTile:12}, 15:{name:'Torch',maxStack:99,placeTile:13},
  16:{name:'Work Bench',maxStack:99,placeTile:14}, 17:{name:'Furnace',maxStack:99,placeTile:15},
  18:{name:'Anvil',maxStack:99,placeTile:16}, 19:{name:'Wood Platform',maxStack:999,placeTile:18},
  20:{name:'Copper Pickaxe',maxStack:1,damage:4,useTime:22,toolPower:35,type:'pickaxe'},
  21:{name:'Copper Axe',maxStack:1,damage:4,useTime:22,toolPower:35,type:'axe'},
  22:{name:'Copper Sword',maxStack:1,damage:8,useTime:22,type:'sword'},
  23:{name:'Iron Pickaxe',maxStack:1,damage:5,useTime:20,toolPower:50,type:'pickaxe'},
  24:{name:'Iron Axe',maxStack:1,damage:5,useTime:20,toolPower:50,type:'axe'},
  25:{name:'Iron Sword',maxStack:1,damage:12,useTime:20,type:'sword'},
  26:{name:'Silver Pickaxe',maxStack:1,damage:6,useTime:18,toolPower:55,type:'pickaxe'},
  27:{name:'Silver Sword',maxStack:1,damage:16,useTime:18,type:'sword'},
  28:{name:'Gold Pickaxe',maxStack:1,damage:7,useTime:16,toolPower:60,type:'pickaxe'},
  29:{name:'Gold Sword',maxStack:1,damage:22,useTime:16,type:'sword'},
  30:{name:'Wood Bow',maxStack:1,damage:6,useTime:28,type:'bow'},
  31:{name:'Wooden Arrow',maxStack:999,damage:4,type:'ammo'},
  32:{name:'Gel',maxStack:99}, 33:{name:'Lens',maxStack:99},
  34:{name:'Wood Helmet',maxStack:1,defense:1,type:'helmet'},
  35:{name:'Wood Chestplate',maxStack:1,defense:1,type:'chestplate'},
  36:{name:'Wood Boots',maxStack:1,defense:0,type:'boots'},
  37:{name:'Copper Helmet',maxStack:1,defense:2,type:'helmet'},
  38:{name:'Copper Chestplate',maxStack:1,defense:3,type:'chestplate'},
  39:{name:'Copper Boots',maxStack:1,defense:1,type:'boots'},
  40:{name:'Iron Helmet',maxStack:1,defense:4,type:'helmet'},
  41:{name:'Iron Chestplate',maxStack:1,defense:5,type:'chestplate'},
  42:{name:'Iron Boots',maxStack:1,defense:3,type:'boots'},
  43:{name:'Lesser Healing Potion',maxStack:30,type:'potion',healAmount:50},
  44:{name:'Chest',maxStack:99,placeTile:17},
};

var TILE_DROPS = {};
TILE_DROPS[1]=[1]; TILE_DROPS[2]=[2]; TILE_DROPS[3]=[1]; TILE_DROPS[4]=[3];
TILE_DROPS[5]=[4]; TILE_DROPS[6]=[6]; TILE_DROPS[7]=[7]; TILE_DROPS[8]=[8];
TILE_DROPS[9]=[9]; TILE_DROPS[10]=[5]; TILE_DROPS[11]=[5]; TILE_DROPS[12]=[14];
TILE_DROPS[13]=[15]; TILE_DROPS[14]=[16]; TILE_DROPS[15]=[17]; TILE_DROPS[16]=[18];
TILE_DROPS[17]=[44]; TILE_DROPS[18]=[19];

var TILE_TOOL_POWER = {};
TILE_TOOL_POWER[1]=0; TILE_TOOL_POWER[3]=0; TILE_TOOL_POWER[4]=0; TILE_TOOL_POWER[5]=10;
TILE_TOOL_POWER[2]=35; TILE_TOOL_POWER[6]=35; TILE_TOOL_POWER[7]=50; TILE_TOOL_POWER[8]=55;
TILE_TOOL_POWER[9]=60; TILE_TOOL_POWER[10]=10; TILE_TOOL_POWER[11]=0; TILE_TOOL_POWER[12]=10;
TILE_TOOL_POWER[13]=0; TILE_TOOL_POWER[14]=10; TILE_TOOL_POWER[15]=35; TILE_TOOL_POWER[16]=35;
TILE_TOOL_POWER[17]=10; TILE_TOOL_POWER[18]=0;

var ENEMY_DEFS = {
  0:{name:'Green Slime',hp:14,damage:6,defense:2,width:14,height:12,ai:'slime',color:'#4caf50'},
  1:{name:'Zombie',hp:45,damage:14,defense:4,width:14,height:24,ai:'walker',color:'#8d6e63'},
  2:{name:'Flying Eye',hp:60,damage:18,defense:6,width:16,height:16,ai:'flyer',color:'#b71c1c'},
  3:{name:'Blue Slime',hp:25,damage:8,defense:3,width:14,height:12,ai:'slime',color:'#2196f3'},
  4:{name:'Skeleton',hp:60,damage:20,defense:8,width:14,height:26,ai:'walker',color:'#e0e0e0'},
  5:{name:'Cave Bat',hp:16,damage:12,defense:2,width:12,height:10,ai:'flyer',color:'#5d4037'},
};

var GRAVITY = 0.35, MAX_FALL_SPEED = 12, JUMP_VEL = -8.5, MOVE_ACCEL = 0.4;
var MAX_WALK_SPEED = 4.5, MAX_AIR_SPEED = 3.5, FRICTION = 0.25, TILE_SZ = 16;

const tileSolid = (world, tx, ty) => {
  if (tx < 0 || tx >= world.width || ty < 0 || ty >= world.height) return true;
  const t = world.tiles[tx][ty];
  return t && t.isActive && t.type !== TILE_PLATFORM;
};

const tilePlatform = (world, tx, ty) => {
  if (tx < 0 || tx >= world.width || ty < 0 || ty >= world.height) return false;
  const t = world.tiles[tx][ty];
  return t && t.isActive && t.type === TILE_PLATFORM;
};

const resolvePhysics = (entity, world, isPlayer, input) => {
  const w2 = entity.width/2, h2 = entity.height/2;
  if (!entity.isOnGround) { entity.vy += GRAVITY; if (entity.vy > MAX_FALL_SPEED) entity.vy = MAX_FALL_SPEED; }
  if (isPlayer && input) {
    const maxSpeed = entity.isOnGround ? MAX_WALK_SPEED : MAX_AIR_SPEED;
    if (input.keys['a'] || input.keys['arrowleft']) { entity.vx -= MOVE_ACCEL; if (entity.vx < -maxSpeed) entity.vx = -maxSpeed; entity.facing = -1; }
    if (input.keys['d'] || input.keys['arrowright']) { entity.vx += MOVE_ACCEL; if (entity.vx > maxSpeed) entity.vx = maxSpeed; entity.facing = 1; }
    if ((input.keys[' ']||input.keys['space']||input.keys['w']||input.keys['arrowup']) && entity.isOnGround && (entity.jumpCooldown||0) <= 0) {
      entity.vy = JUMP_VEL; entity.isOnGround = false; entity.jumpCooldown = 8; entity.jumpHeld = 10;
    }
    if ((entity.jumpHeld||0) > 0 && (input.keys[' ']||input.keys['space']||input.keys['w']||input.keys['arrowup'])) { entity.vy -= 0.3; entity.jumpHeld--; }
    if ((input.keys['s']||input.keys['arrowdown']) && entity.isOnGround) {
      const tx=Math.floor(entity.x/TILE_SZ), ty=Math.floor((entity.y+h2)/TILE_SZ);
      if (tilePlatform(world,tx,ty)) { entity.y += 4; entity.isOnGround = false; }
    }
  }
  if (entity.jumpCooldown > 0) entity.jumpCooldown--;
  if (entity.isOnGround && (!isPlayer || !input || (!input.keys['a']&&!input.keys['d']&&!input.keys['arrowleft']&&!input.keys['arrowright']))) {
    if (entity.vx>0) entity.vx=Math.max(0,entity.vx-FRICTION); else if (entity.vx<0) entity.vx=Math.min(0,entity.vx+FRICTION);
  }
  entity.x += entity.vx;
  {
    const l=Math.floor((entity.x-w2)/TILE_SZ), r=Math.floor((entity.x+w2-1)/TILE_SZ);
    const t=Math.floor((entity.y-h2+2)/TILE_SZ), b=Math.floor((entity.y+h2-3)/TILE_SZ);
    for (let ty=t;ty<=b;ty++) for (let tx=l;tx<=r;tx++) {
      if (tileSolid(world,tx,ty)) {
        if (entity.vx>0) entity.x=tx*TILE_SZ-w2-0.01; else if (entity.vx<0) entity.x=tx*TILE_SZ+TILE_SZ+w2+0.01;
        entity.vx=0;
      }
    }
  }
  entity.isOnGround = false;
  entity.y += entity.vy;
  {
    const l=Math.floor((entity.x-w2+2)/TILE_SZ), r=Math.floor((entity.x+w2-3)/TILE_SZ);
    const t=Math.floor((entity.y-h2)/TILE_SZ), b=Math.floor((entity.y+h2-1)/TILE_SZ);
    for (let tx=l;tx<=r;tx++) for (let ty=t;ty<=b;ty++) {
      if (tileSolid(world,tx,ty)) {
        if (entity.vy>0) { entity.y=ty*TILE_SZ-h2-0.01; entity.isOnGround=true; }
        else if (entity.vy<0) { entity.y=ty*TILE_SZ+TILE_SZ+h2+0.01; }
        entity.vy=0;
      }
    }
    if (!entity.isOnGround) {
      const ty=Math.floor((entity.y+h2-1)/TILE_SZ);
      for (let tx=l;tx<=r;tx++) {
        if (tilePlatform(world,tx,ty) && entity.vy>=0) {
          const prevBot = (entity.y-entity.vy)+h2;
          if (prevBot <= ty*TILE_SZ+2) { entity.y=ty*TILE_SZ-h2-0.01; entity.vy=0; entity.isOnGround=true; }
        }
      }
    }
  }
  if (entity.x-w2<0){entity.x=w2;entity.vx=0;}
  if (entity.x+w2>world.width*TILE_SZ){entity.x=world.width*TILE_SZ-w2;entity.vx=0;}
  if (entity.y-h2<0){entity.y=h2;entity.vy=0;}
  if (entity.y+h2>world.height*TILE_SZ){entity.y=world.height*TILE_SZ-h2;entity.vy=0;entity.isOnGround=true;if(isPlayer)entity.hp=0;}
};

const addToInventory = (inventory, itemId, count) => {
  for (let i=0;i<inventory.length&&count>0;i++) {
    const s=inventory[i]; if(s&&s.itemId===itemId){const mx=(ITEMS[itemId]&&ITEMS[itemId].maxStack)||999; const a=Math.min(count,mx-s.count); s.count+=a; count-=a;}
  }
  for (let i=0;i<inventory.length&&count>0;i++) {
    if(!inventory[i]||inventory[i].count===0){const mx=(ITEMS[itemId]&&ITEMS[itemId].maxStack)||999; const a=Math.min(count,mx); inventory[i]={itemId,count:a}; count-=a;}
  }
  return count<=0;
};

const breakTile = (world, tx, ty, toolPower, particles) => {
  if (tx<0||tx>=world.width||ty<0||ty>=world.height) return null;
  const tile=world.tiles[tx][ty]; if(!tile||!tile.isActive) return null;
  const req=TILE_TOOL_POWER[tile.type]||0; if(toolPower<req) return null;
  tile.hp -= 10+toolPower*0.5;
  if (tile.hp<=0) {
    for (let i=0;i<6;i++) particles.push({x:tx*TILE_SZ+TILE_SZ/2+(Math.random()-0.5)*8,y:ty*TILE_SZ+TILE_SZ/2+(Math.random()-0.5)*8,vx:(Math.random()-0.5)*3,vy:-(Math.random()*4+1),life:20+Math.random()*20,maxLife:40,color:'#999',size:2+Math.random()*3,type:0});
    const drops=TILE_DROPS[tile.type]; const itemId=drops?drops[0]:null;
    tile.type=TILE_AIR; tile.isActive=false; tile.hp=0; tile.wall=tile.wall||0;
    return itemId;
  }
  return undefined;
};

const placeTile = (world, tx, ty, tileType, player) => {
  if (tx<0||tx>=world.width||ty<0||ty>=world.height||world.tiles[tx][ty].isActive) return false;
  const px=Math.floor(player.x/TILE_SZ),py=Math.floor(player.y/TILE_SZ);
  if (tx>=px-1&&tx<=px+1&&ty>=py-2&&ty<=py+1) return false;
  world.tiles[tx][ty].type=tileType; world.tiles[tx][ty].isActive=true; world.tiles[tx][ty].hp=100;
  return true;
};

const spawnParticle = (particles,x,y,vx,vy,color,size,life) => {
  particles.push({x,y,vx,vy,color,size,life,maxLife:life,type:0});
};

const updateEnemyAI = (enemy, player, world, particles) => {
  const def=ENEMY_DEFS[enemy.type]; if(!def) return;
  const dx=player.x-enemy.x, dy=player.y-enemy.y, dist=Math.sqrt(dx*dx+dy*dy);
  enemy.facing=dx>0?1:-1;
  if(def.ai==='slime'){
    if(enemy.isOnGround&&dist<300&&!enemy.ai.jumpTimer) enemy.ai.jumpTimer=40+Math.random()*40;
    if(enemy.ai.jumpTimer){enemy.ai.jumpTimer--; if(enemy.ai.jumpTimer===35&&enemy.isOnGround){enemy.vy=-6-Math.random()*3; enemy.vx=(dx>0?1:-1)*(2+Math.random()*2); enemy.isOnGround=false;}}
    if(!enemy.isOnGround) enemy.vx*=0.98; else enemy.vx*=0.85;
  }else if(def.ai==='walker'){
    if(dist<400){const s=1.8; if(Math.abs(dx)>20){enemy.vx+=(dx>0?1:-1)*0.1; enemy.vx=Math.max(-s,Math.min(s,enemy.vx));}}
    else {enemy.vx*=0.9; if(Math.abs(enemy.vx)<0.1)enemy.vx=(Math.random()-0.5)*1;}
    if(enemy.isOnGround&&enemy.vx!==0){
      const atx=Math.floor((enemy.x+(enemy.vx>0?enemy.width/2+4:-enemy.width/2-4))/TILE_SZ), aty=Math.floor(enemy.y/TILE_SZ);
      if((tileSolid(world,atx,aty)||tileSolid(world,atx,aty-1))&&(enemy.ai.jumpCooldown||0)<=0){enemy.vy=-7; enemy.isOnGround=false; enemy.ai.jumpCooldown=15;}
    }
    if(enemy.ai.jumpCooldown>0)enemy.ai.jumpCooldown--;
  }else if(def.ai==='flyer'){
    const tx=player.x,ty=player.y-40,s=2.5;
    if(dist<500){enemy.vx+=(tx-enemy.x)*0.005; enemy.vy+=(ty-enemy.y)*0.005;}
    else {enemy.vx+=Math.sin(enemy.ai.phase||0)*0.5; enemy.vy+=Math.cos(enemy.ai.phase||0)*0.5;}
    enemy.vx=Math.max(-s,Math.min(s,enemy.vx)); enemy.vy=Math.max(-s,Math.min(s,enemy.vy));
    enemy.ai.phase=(enemy.ai.phase||0)+0.02;
  }
};

function updateGame(state) {
  const {world, player, enemies, particles, time, input} = state;
  time.ticks++; if(time.ticks>=time.dayLength){time.ticks=0;time.dayNumber++;time.isDawn=true;}
  time.isDaytime=time.ticks<time.dayLength*0.55; time.isDusk=time.ticks>=time.dayLength*0.53&&time.ticks<time.dayLength*0.55;
  time.isDawn=time.ticks<time.dayLength*0.02; time.isBloodMoon=(time.dayNumber>0&&time.dayNumber%7===0&&!time.isDaytime&&Math.random()<0.005);
  if(player.attackCooldown>0)player.attackCooldown--; if(player.immunityTimer>0)player.immunityTimer--;
  resolvePhysics(player, world, true, input);

  // Block interaction
  if (input.mouseLeft && player.attackCooldown<=0) {
    const ss=player.inventory[player.hotbarSlot], si=ss&&ss.count>0?ITEMS[ss.itemId]:null;
    const tx=input.mouseTileX, ty=input.mouseTileY;
    const dist=Math.sqrt((tx*TILE_SZ+TILE_SZ/2-player.x)**2+(ty*TILE_SZ+TILE_SZ/2-player.y)**2);
    if (si&&(si.type==='pickaxe'||si.type==='axe')&&dist<TILE_SZ*5) {
      const d=breakTile(world,tx,ty,si.toolPower||0,particles);
      if(d!==null){if(d!==undefined)addToInventory(player.inventory,d,1); player.attackCooldown=si.useTime||16;}
    } else if (si&&si.type==='sword') {
      for (const en of enemies) {
        if(en.hp<=0)continue; const edx=en.x-player.x,edy=en.y-player.y;
        if(Math.sqrt(edx*edx+edy*edy)<35&&(player.facing>0?edx>-10:edx<10)){
          en.hp-=Math.max(1,si.damage-en.defense*0.5); en.immunityTimer=10; en.vx+=player.facing*4; en.vy-=2;
          for(let i=0;i<4;i++)spawnParticle(particles,en.x,en.y,(Math.random()-0.5)*3,(Math.random()-0.5)*3,'#ff0',3,15);
        }
      }
      player.attackCooldown=si.useTime||20;
    } else if (dist<TILE_SZ*4) {
      const d=breakTile(world,tx,ty,5,particles);
      if(d!==undefined&&d!==null){addToInventory(player.inventory,d,1);}
      player.attackCooldown=28;
    }
  }
  // Place block
  if (input.mouseRight && player.attackCooldown<=0) {
    const ss=player.inventory[player.hotbarSlot], si=ss&&ss.count>0?ITEMS[ss.itemId]:null;
    if(si&&si.placeTile!==undefined){
      const tx=input.mouseTileX,ty=input.mouseTileY;
      if(Math.sqrt((tx*TILE_SZ+TILE_SZ/2-player.x)**2+(ty*TILE_SZ+TILE_SZ/2-player.y)**2)<TILE_SZ*5){
        if(placeTile(world,tx,ty,si.placeTile,player)){ss.count--; if(ss.count<=0)player.inventory[player.hotbarSlot]=null; player.attackCooldown=12;}
      }
    }
  }
  // Hotbar
  if(input.mouseWheel!==0)player.hotbarSlot=((player.hotbarSlot-Math.sign(input.mouseWheel))+10)%10;
  for(let i=0;i<10;i++){if(input.keys[String(i+1)])player.hotbarSlot=i;}
  if(input.keys['0'])player.hotbarSlot=9;
  // Heal
  if(input.keys['h']&&player.attackCooldown<=0){
    for(let i=0;i<player.inventory.length;i++){const s=player.inventory[i];if(s&&s.count>0){const it=ITEMS[s.itemId];if(it&&it.type==='potion'&&it.healAmount){player.hp=Math.min(player.hpMax,player.hp+it.healAmount);s.count--;if(s.count<=0)player.inventory[i]=null;player.attackCooldown=60;break;}}}
  }
  // HP regen
  if(player.hp<player.hpMax&&player.immunityTimer<=0)player.hp=Math.min(player.hpMax,player.hp+player.hpMax/600);
  // Enemies
  for(const en of enemies){
    if(en.hp<=0)continue; if(en.immunityTimer>0)en.immunityTimer--;
    if(!en.ai)en.ai={}; updateEnemyAI(en,player,world,particles);
    resolvePhysics(en,world,false,null);
    const edx=player.x-en.x,edy=player.y-en.y,ed=Math.sqrt(edx*edx+edy*edy);
    if(ed<(player.width+en.width)/2+2&&player.immunityTimer<=0){
      const dmg=Math.max(1,(ENEMY_DEFS[en.type]?ENEMY_DEFS[en.type].damage:5)-player.defense*0.5);
      player.hp-=Math.floor(dmg); player.immunityTimer=30; player.vx+=(edx>0?-1:1)*5; player.vy-=3;
    }
  }
  // Remove dead enemies
  for(let i=enemies.length-1;i>=0;i--){
    if(enemies[i].hp<=0){
      const def=ENEMY_DEFS[enemies[i].type]; player.coins+=5+Math.floor(Math.random()*20);
      if(def&&(enemies[i].type===0||enemies[i].type===3)&&Math.random()<0.7)addToInventory(player.inventory,32,1+Math.floor(Math.random()*2));
      if(def&&enemies[i].type===2&&Math.random()<0.5)addToInventory(player.inventory,33,1);
      for(let j=0;j<8;j++)spawnParticle(particles,enemies[i].x,enemies[i].y,(Math.random()-0.5)*4,-(Math.random()*4+1),def?def.color:'#ccc',3,20+Math.random()*15);
      enemies.splice(i,1);
    }
  }
  // Particles
  for(let i=particles.length-1;i>=0;i--){const p=particles[i];p.x+=p.vx;p.y+=p.vy;p.vy+=0.15;p.life--;if(p.life<=0)particles.splice(i,1);}
  // Death
  if(player.hp<=0){player.hp=player.hpMax;player.coins=Math.floor(player.coins*0.5);player.x=player.spawnX||world.spawnX*TILE_SZ+TILE_SZ/2;player.y=player.spawnY||world.spawnY*TILE_SZ+TILE_SZ/2;player.vx=0;player.vy=0;player.immunityTimer=120;}
  return {world,player,enemies,particles,time};
}
