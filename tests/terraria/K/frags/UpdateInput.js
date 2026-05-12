function updateInput(raw) {
  const keys = {};
  for (const k of Object.keys(raw.keys)) {
    keys[k] = raw.keys[k];
  }

  // Compute mouse tile position relative to world (caller provides camera offset)
  const mx = raw.mouseX || 0;
  const my = raw.mouseY || 0;

  const tileSize = raw.tileSize || 16;
  const camX = raw.cameraX || 0;
  const camY = raw.cameraY || 0;

  const mouseTileX = Math.floor((mx + camX) / tileSize);
  const mouseTileY = Math.floor((my + camY) / tileSize);

  return {
    keys,
    mouseX: mx,
    mouseY: my,
    mouseLeft: !!raw.mouseLeft,
    mouseRight: !!raw.mouseRight,
    mouseWheel: raw.mouseWheel || 0,
    mouseTileX,
    mouseTileY
  };
}
