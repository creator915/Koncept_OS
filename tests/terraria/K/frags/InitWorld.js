// TILE TYPES (const)
const TILE_AIR$ = 0;
const TILE_DIRT$ = 1;
const TILE_STONE$ = 2;
const TILE_GRASS$ = 3;
const TILE_SAND$ = 4;
const TILE_CLAY$ = 5;
const TILE_COPPER$ = 6;
const TILE_IRON$ = 7;
const TILE_SILVER$ = 8;
const TILE_GOLD$ = 9;
const TILE_WOOD$ = 10;
const TILE_LEAVES$ = 11;
const TILE_PLANKS$ = 12;
const TILE_TORCH$ = 13;
const TILE_WORKBENCH$ = 14;
const TILE_FURNACE$ = 15;
const TILE_ANVIL$ = 16;
const TILE_CHEST$ = 17;
const TILE_PLATFORM$ = 18;

const hash = (x, y, seed) => {
  let h = seed + x * 374761393 + y * 668265263;
  h = (h ^ (h >> 13)) * 1274126177;
  return (h ^ (h >> 16)) & 0x7fffffff;
};

const noise = (x, y, seed) => (hash(x, y, seed) % 1000) / 1000;

const smoothNoise = (x, y, seed) => {
  const ix = Math.floor(x), iy = Math.floor(y);
  const fx = x - ix, fy = y - iy;
  const sx = fx * fx * (3 - 2 * fx);
  const sy = fy * fy * (3 - 2 * fy);
  return noise(ix,iy,seed)*(1-sx)*(1-sy) + noise(ix+1,iy,seed)*sx*(1-sy) + noise(ix,iy+1,seed)*(1-sx)*sy + noise(ix+1,iy+1,seed)*sx*sy;
};

function initWorld(config) {
  const W = config.width, H = config.height, seed = config.seed;
  const surfaceH = [];
  for (let x = 0; x < W; x++) {
    surfaceH.push(Math.floor(70 + smoothNoise(x*0.02,0,seed)*30 + smoothNoise(x*0.005,100,seed)*40));
  }
  const tiles = [];
  for (let x = 0; x < W; x++) {
    tiles[x] = [];
    for (let y = 0; y < H; y++) {
      const sh = surfaceH[x], depth = y - sh;
      let type = 0; // AIR
      let wall = 0, isActive = true;
      if (y > sh) {
        const caveNoise = smoothNoise(x*0.08,y*0.08,seed+500);
        const caveNoise2 = smoothNoise(x*0.04,y*0.06,seed+600);
        const isCave = caveNoise > 0.55 && caveNoise2 > 0.4;
        if (depth <= 3) { type = depth===1 ? TILE_GRASS$ : TILE_DIRT$; }
        else if (depth <= 25) { type = isCave ? 0 : TILE_DIRT$; }
        else if (depth <= 160) { type = isCave ? 0 : TILE_STONE$; }
        else { type = TILE_STONE$; }
        if (isCave && type === 0) wall = 1;
      } else { type = 0; isActive = false; }
      if (type === 0) isActive = false;
      tiles[x][y] = {type, hp:type===0?0:100, wall, isActive};
    }
  }
  // Sand
  for (let x = 0; x < W; x++) {
    if (smoothNoise(x*0.1,0,seed+1000) > 0.65) {
      for (let dy = 0; dy < 4 + Math.floor(smoothNoise(x,0,seed+2000)*4); dy++) {
        const y = surfaceH[x]+1+dy;
        if (y < H && tiles[x][y].type === TILE_DIRT$) tiles[x][y].type = TILE_SAND$;
      }
      if (surfaceH[x]+1 < H && tiles[x][surfaceH[x]+1].type === TILE_GRASS$) tiles[x][surfaceH[x]+1].type = TILE_SAND$;
    }
  }
  // Clay
  for (let x = 0; x < W; x++) {
    if (smoothNoise(x*0.07,300,seed+3000) > 0.7) {
      for (let dy = 3; dy < 7; dy++) {
        const y = surfaceH[x]+dy;
        if (y < H && tiles[x][y].type === TILE_DIRT$) tiles[x][y].type = TILE_CLAY$;
      }
    }
  }
  // Ores
  const ores = [
    {type:TILE_COPPER$,depth:5,freq:0.03},{type:TILE_IRON$,depth:12,freq:0.025},
    {type:TILE_SILVER$,depth:25,freq:0.018},{type:TILE_GOLD$,depth:45,freq:0.012}
  ];
  for (const ore of ores) {
    for (let x = 0; x < W; x++) {
      for (let y = surfaceH[x]+ore.depth; y < H-20; y++) {
        if (tiles[x][y].type === TILE_STONE$ && smoothNoise(x*1.5,y*1.5,seed+ore.type*1000) > 1-ore.freq) {
          for (let dx=-1;dx<=1;dx++) for (let dy=-1;dy<=1;dy++) {
            const nx=x+dx,ny=y+dy;
            if (nx>=0&&nx<W&&ny>=0&&ny<H&&(tiles[nx][ny].type===TILE_STONE$||tiles[nx][ny].type===TILE_DIRT$)) {
              if (Math.random()<0.7) tiles[nx][ny].type = ore.type;
            }
          }
        }
      }
    }
  }
  // Trees
  for (let x = 2; x < W-2; x++) {
    const sh = surfaceH[x];
    if (Math.abs(surfaceH[x]-surfaceH[x+1])<=1 && Math.abs(surfaceH[x]-surfaceH[x-1])<=1 &&
        tiles[x][sh+1] && tiles[x][sh+1].type===TILE_GRASS$ && Math.random()<0.18) {
      const treeH = 4+Math.floor(Math.random()*4);
      for (let ty=sh-treeH;ty<=sh;ty++) {
        if (ty>=0&&tiles[x][ty].type===0) { tiles[x][ty]={type:TILE_WOOD$,hp:100,wall:0,isActive:true}; }
      }
      for (let lx=x-2;lx<=x+2;lx++) for (let ly=sh-treeH-2;ly<=sh-treeH+1;ly++) {
        if (lx>=0&&lx<W&&ly>=0&&ly<H&&tiles[lx][ly].type===0&&Math.abs(lx-x)+Math.abs(ly-(sh-treeH))<3&&Math.random()<0.8) {
          tiles[lx][ly] = {type:TILE_LEAVES$,hp:40,wall:0,isActive:true};
        }
      }
    }
  }
  // Spawn flat
  const spawnX = Math.floor(W/2), spawnY = surfaceH[spawnX]-2;
  for (let sx=spawnX-5;sx<=spawnX+5;sx++) {
    if (sx>=0&&sx<W) {
      for (let sy=surfaceH[sx]-8;sy<=surfaceH[sx];sy++) {
        if (sy>=0&&(tiles[sx][sy].type===TILE_WOOD$||tiles[sx][sy].type===TILE_LEAVES$)) {
          tiles[sx][sy] = {type:0,hp:0,wall:0,isActive:false};
        }
      }
      tiles[sx][surfaceH[sx]+1].type = TILE_GRASS$;
    }
  }
  // Starter chest
  const cx=spawnX+6,cy=surfaceH[cx];
  if (cx<W&&cy>0&&!tiles[cx][cy].isActive) tiles[cx][cy]={type:TILE_CHEST$,hp:100,wall:0,isActive:true};

  return {width:W,height:H,tiles,spawnX,spawnY,seed};
}
