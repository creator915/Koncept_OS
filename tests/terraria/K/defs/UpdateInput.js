/**
 * updateInput: Capture keyboard and mouse state.
 *
 * @param {{keys: object, mouseX: number, mouseY: number, mouseLeft: boolean, mouseRight: boolean, mouseWheel: number, canvasRect: object, tileSize: number, cameraX: number, cameraY: number}} raw
 * @returns {{keys: object, mouseX: number, mouseY: number, mouseLeft: boolean, mouseRight: boolean, mouseWheel: number, mouseTileX: number, mouseTileY: number}}
 *
 * @example
 * updateInput({keys:{},mouseX:400,mouseY:300,mouseLeft:false,mouseRight:false,mouseWheel:0,canvasRect:{left:0,top:0}})  // → input snapshot
 */
function updateInput(raw) { throw new Error("updateInput: contract-only; implement in K/frags/UpdateInput.js"); }
