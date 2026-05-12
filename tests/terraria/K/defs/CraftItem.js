/**
 * craftItem: Check recipe and craft if ingredients available.
 *
 * @param {{recipeId: number, inventory: Array, recipes: Array}} craftRequest
 * @returns {{success: boolean, inventory: Array, error: string}}
 *
 * @example
 * craftItem({recipeId:0,inventory:[...],recipes:[...]})  // → {success:true,inventory:[...],error:""}
 * @example boundary
 * craftItem({recipeId:0,inventory:[],recipes:[...]})  // → {success:false,inventory:[],error:"missing ingredients"}
 */
function craftItem(craftRequest) { throw new Error("craftItem: contract-only; implement in K/frags/CraftItem.js"); }
