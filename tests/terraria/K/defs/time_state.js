/**
 * @typedef {Object} TimeState
 * @property {number} ticks - Game time 0-86400 (24h cycle)
 * @property {number} dayNumber - Current day counter
 * @property {boolean} isDaytime - Daytime flag
 * @property {boolean} isBloodMoon - Blood moon active
 * @property {boolean} isDawn - Dawn transition (brief)
 * @property {boolean} isDusk - Dusk transition (brief)
 * @property {number} dayLength - Ticks per full day (14400 = 4 min real)
 */
