// Recipe definitions
const RECIPES = [
  // result: {itemId, count}, ingredients: [{itemId, count}], station: tileType (0=none)
  {id:0, result:{itemId:14, count:4}, ingredients:[{itemId:5,count:1}], station:0, name:'Wood Planks (x4)'},
  {id:1, result:{itemId:14, count:1}, ingredients:[{itemId:5,count:1}], station:TILE_WORKBENCH, name:'Wood Planks'},
  {id:2, result:{itemId:15, count:3}, ingredients:[{itemId:32,count:1},{itemId:5,count:1}], station:0, name:'Torch (x3)'},
  {id:3, result:{itemId:16, count:1}, ingredients:[{itemId:14,count:10}], station:0, name:'Work Bench'},
  {id:4, result:{itemId:17, count:1}, ingredients:[{itemId:2,count:20},{itemId:5,count:4},{itemId:15,count:3}], station:TILE_WORKBENCH, name:'Furnace'},
  {id:5, result:{itemId:18, count:1}, ingredients:[{itemId:11,count:5}], station:TILE_WORKBENCH, name:'Anvil'},
  {id:6, result:{itemId:10, count:1}, ingredients:[{itemId:6,count:3}], station:TILE_FURNACE, name:'Copper Bar'},
  {id:7, result:{itemId:11, count:1}, ingredients:[{itemId:7,count:3}], station:TILE_FURNACE, name:'Iron Bar'},
  {id:8, result:{itemId:12, count:1}, ingredients:[{itemId:8,count:4}], station:TILE_FURNACE, name:'Silver Bar'},
  {id:9, result:{itemId:13, count:1}, ingredients:[{itemId:9,count:4}], station:TILE_FURNACE, name:'Gold Bar'},
  {id:10, result:{itemId:20, count:1}, ingredients:[{itemId:10,count:8},{itemId:5,count:4}], station:TILE_ANVIL, name:'Copper Pickaxe'},
  {id:11, result:{itemId:21, count:1}, ingredients:[{itemId:10,count:6},{itemId:5,count:4}], station:TILE_ANVIL, name:'Copper Axe'},
  {id:12, result:{itemId:22, count:1}, ingredients:[{itemId:10,count:8}], station:TILE_ANVIL, name:'Copper Sword'},
  {id:13, result:{itemId:23, count:1}, ingredients:[{itemId:11,count:10},{itemId:5,count:4}], station:TILE_ANVIL, name:'Iron Pickaxe'},
  {id:14, result:{itemId:24, count:1}, ingredients:[{itemId:11,count:8},{itemId:5,count:4}], station:TILE_ANVIL, name:'Iron Axe'},
  {id:15, result:{itemId:25, count:1}, ingredients:[{itemId:11,count:10}], station:TILE_ANVIL, name:'Iron Sword'},
  {id:16, result:{itemId:26, count:1}, ingredients:[{itemId:12,count:10},{itemId:5,count:4}], station:TILE_ANVIL, name:'Silver Pickaxe'},
  {id:17, result:{itemId:27, count:1}, ingredients:[{itemId:12,count:10}], station:TILE_ANVIL, name:'Silver Sword'},
  {id:18, result:{itemId:28, count:1}, ingredients:[{itemId:13,count:12},{itemId:5,count:4}], station:TILE_ANVIL, name:'Gold Pickaxe'},
  {id:19, result:{itemId:29, count:1}, ingredients:[{itemId:13,count:12}], station:TILE_ANVIL, name:'Gold Sword'},
  {id:20, result:{itemId:30, count:1}, ingredients:[{itemId:14,count:12}], station:TILE_WORKBENCH, name:'Wood Bow'},
  {id:21, result:{itemId:31, count:10}, ingredients:[{itemId:5,count:1},{itemId:2,count:1}], station:TILE_WORKBENCH, name:'Wooden Arrow (x10)'},
  {id:22, result:{itemId:34, count:1}, ingredients:[{itemId:14,count:15}], station:TILE_WORKBENCH, name:'Wood Helmet'},
  {id:23, result:{itemId:35, count:1}, ingredients:[{itemId:14,count:20}], station:TILE_WORKBENCH, name:'Wood Chestplate'},
  {id:24, result:{itemId:36, count:1}, ingredients:[{itemId:14,count:15}], station:TILE_WORKBENCH, name:'Wood Boots'},
  {id:25, result:{itemId:37, count:1}, ingredients:[{itemId:10,count:10}], station:TILE_ANVIL, name:'Copper Helmet'},
  {id:26, result:{itemId:38, count:1}, ingredients:[{itemId:10,count:15}], station:TILE_ANVIL, name:'Copper Chestplate'},
  {id:27, result:{itemId:39, count:1}, ingredients:[{itemId:10,count:10}], station:TILE_ANVIL, name:'Copper Boots'},
  {id:28, result:{itemId:40, count:1}, ingredients:[{itemId:11,count:10}], station:TILE_ANVIL, name:'Iron Helmet'},
  {id:29, result:{itemId:41, count:1}, ingredients:[{itemId:11,count:15}], station:TILE_ANVIL, name:'Iron Chestplate'},
  {id:30, result:{itemId:42, count:1}, ingredients:[{itemId:11,count:10}], station:TILE_ANVIL, name:'Iron Boots'},
  {id:31, result:{itemId:43, count:1}, ingredients:[{itemId:32,count:2},{itemId:2,count:1}], station:TILE_WORKBENCH, name:'Lesser Healing Potion'},
  {id:32, result:{itemId:19, count:4}, ingredients:[{itemId:5,count:1}], station:TILE_WORKBENCH, name:'Wood Platform (x4)'},
];

function craftItem(craftRequest) {
  const {recipeId, inventory, recipes} = craftRequest;
  const recipeList = recipes || RECIPES;
  const recipe = recipeList.find(r => r.id === recipeId);

  if (!recipe) {
    return {success:false, inventory, error:'Unknown recipe'};
  }

  // Check ingredients
  const invCopy = JSON.parse(JSON.stringify(inventory));
  for (const ing of recipe.ingredients) {
    let remaining = ing.count;
    for (let i = 0; i < invCopy.length && remaining > 0; i++) {
      const slot = invCopy[i];
      if (slot && slot.itemId === ing.itemId && slot.count > 0) {
        const take = Math.min(slot.count, remaining);
        slot.count -= take;
        remaining -= take;
        if (slot.count <= 0) invCopy[i] = null;
      }
    }
    if (remaining > 0) {
      return {success:false, inventory, error:`Missing ${ing.count} of item ${ing.itemId}`};
    }
  }

  // Add result to invCopy
  let resultRemaining = recipe.result.count;
  // Try stack first
  for (let i = 0; i < invCopy.length && resultRemaining > 0; i++) {
    const slot = invCopy[i];
    if (slot && slot.itemId === recipe.result.itemId) {
      const maxStack = (ITEMS[recipe.result.itemId] && ITEMS[recipe.result.itemId].maxStack) || 999;
      const canAdd = maxStack - slot.count;
      const toAdd = Math.min(resultRemaining, canAdd);
      slot.count += toAdd;
      resultRemaining -= toAdd;
    }
  }
  // Then empty slots
  for (let i = 0; i < invCopy.length && resultRemaining > 0; i++) {
    if (!invCopy[i] || invCopy[i].count <= 0) {
      const maxStack = (ITEMS[recipe.result.itemId] && ITEMS[recipe.result.itemId].maxStack) || 999;
      const toAdd = Math.min(resultRemaining, maxStack);
      invCopy[i] = {itemId: recipe.result.itemId, count: toAdd};
      resultRemaining -= toAdd;
    }
  }

  return {success:true, inventory: invCopy, error:''};
}
