/**
 * @typedef {Object} PlayerState
 * @property {number} x - Player center X (tiles)
 * @property {number} y - Player center Y (tiles)
 * @property {number} vx - Horizontal velocity
 * @property {number} vy - Vertical velocity
 * @property {number} width - Player width in tiles
 * @property {number} height - Player height in tiles
 * @property {number} facing - Direction: -1 left, 1 right
 * @property {number} hp - Current health
 * @property {number} hpMax - Maximum health (100-400)
 * @property {number} mp - Current mana
 * @property {number} mpMax - Maximum mana
 * @property {number} defense - Defense value
 * @property {Array<{itemId: number, count: number}>} inventory - 50 slots
 * @property {number} hotbarSlot - Selected hotbar index (0-9)
 * @property {number} attackCooldown - Frames until next attack
 * @property {number} immunityTimer - Invincibility frames
 * @property {boolean} isOnGround - Touching ground
 * @property {number} spawnX - Bed spawn X
 * @property {number} spawnY - Bed spawn Y
 * @property {number} coins - Copper coins held
 */
