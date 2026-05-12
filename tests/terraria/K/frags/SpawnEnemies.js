function spawnEnemies(spawnContext) {
  const {time, player, world, enemies, spawnCap} = spawnContext;
  const cap = spawnCap || 20;
  const tileSize = 16;

  if (enemies.length >= cap) return {enemies};

  // Spawn rate: one attempt per frame
  const px = player.x / tileSize;
  const py = player.y / tileSize;
  const depth = py - (world.spawnY || 80);

  // Base spawn chance
  let spawnChance = 0.02; // 2% per frame
  if (!time.isDaytime) spawnChance = 0.06; // higher at night
  if (time.isBloodMoon) spawnChance = 0.15;

  // Depth modifier
  if (depth > 30) spawnChance *= 1.5;
  if (depth > 60) spawnChance *= 1.8;
  if (depth > 100) spawnChance *= 2.0;

  // Don't spawn too close to player
  const minDist = 12; // tiles
  const maxDist = 30;

  if (Math.random() >= spawnChance) return {enemies};

  // Pick random spawn position
  const angle = Math.random() * Math.PI * 2;
  const dist = minDist + Math.random() * (maxDist - minDist);
  const spawnTX = Math.floor(px + Math.cos(angle) * dist);
  const spawnTY = Math.floor(py + Math.sin(angle) * dist * 0.5);

  // Clamp to world
  if (spawnTX < 2 || spawnTX >= world.width - 2 || spawnTY < 2 || spawnTY >= world.height - 2) return {enemies};

  // Check spawn location is air with ground below
  const spawnTile = world.tiles[spawnTX] && world.tiles[spawnTX][spawnTY];
  const tileBelow = world.tiles[spawnTX] && world.tiles[spawnTX][spawnTY + 1];

  if (!spawnTile || spawnTile.isActive) return {enemies};
  if (!tileBelow || !tileBelow.isActive) return {enemies}; // Need ground

  // Pick enemy type based on depth and time
  let type = 0; // Green Slime default
  const rand = Math.random();

  if (!time.isDaytime) {
    // Night: zombies and flying eyes
    if (rand < 0.4) type = 1; // Zombie
    else if (rand < 0.55 && depth > 10) type = 2; // Flying Eye
    else type = 0; // Slime
  } else {
    // Day: mostly slimes, some skeletons deeper
    if (depth > 30 && rand < 0.15) type = 3; // Blue Slime
    else if (depth > 50 && rand < 0.1) type = 4; // Skeleton
    else if (depth > 20 && rand < 0.05) type = 5; // Cave Bat
    else type = 0;
  }

  if (time.isBloodMoon) {
    // More dangerous enemies
    if (rand < 0.3) type = 1;
    else if (rand < 0.5) type = 2;
    else type = 4;
  }

  const def = ENEMY_DEFS[type];
  if (!def) return {enemies};

  // Create enemy
  const newEnemy = {
    id: Date.now() + Math.random(),
    type,
    x: spawnTX * tileSize + tileSize/2,
    y: spawnTY * tileSize + tileSize/2,
    vx: 0, vy: 0,
    width: def.width,
    height: def.height,
    hp: def.hp,
    hpMax: def.hp,
    damage: def.damage,
    defense: def.defense,
    ai: {jumpTimer: 0, jumpCooldown: 0, phase: Math.random() * Math.PI * 2},
    immunityTimer: 0,
    isOnGround: false,
    facing: 1
  };

  enemies.push(newEnemy);
  return {enemies};
}
