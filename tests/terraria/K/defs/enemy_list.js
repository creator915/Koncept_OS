/**
 * @typedef {Object} Enemy
 * @property {number} id - Unique ID
 * @property {number} type - Enemy type (0=GreenSlime, 1=Zombie, 2=FlyingEye, 3=BlueSlime, 4=Skeleton)
 * @property {number} x - Center X (tiles)
 * @property {number} y - Center Y (tiles)
 * @property {number} vx - Horizontal velocity
 * @property {number} vy - Vertical velocity
 * @property {number} width - Hitbox width
 * @property {number} height - Hitbox height
 * @property {number} hp - Current health
 * @property {number} hpMax - Max health
 * @property {number} damage - Contact damage
 * @property {number} defense - Defense value
 * @property {object} ai - AI state data
 * @property {number} immunityTimer - Invincibility frames
 * @property {boolean} isOnGround - Touching ground
 * @property {number} facing - Direction -1/1
 */

/**
 * @typedef {Array<Enemy>} EnemyList
 */
