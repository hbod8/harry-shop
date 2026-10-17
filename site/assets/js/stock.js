// Fills each [data-stock="<variant id>"] badge with live stock
// (on_hand - reserved).

// showStock labels the badges from variant records already fetched.
shop.showStock = (variants) => {
  const byId = new Map(variants.map((v) => [v.id, v.on_hand - v.reserved]));
  for (const badge of document.querySelectorAll("[data-stock]")) {
    const available = byId.get(badge.dataset.stock) ?? 0;
    badge.textContent = shop.stockLabel(available);
    badge.dataset.level = available <= 0 ? "out" : available <= 3 ? "low" : "ok";
  }
};

// loadStock lists a listed product's variants. Unlisted products' variants
// can't be listed, so the item page passes its expanded variants instead.
shop.loadStock = async (productId) => {
  try {
    const filter = encodeURIComponent(`product='${productId}'`);
    const data = await shop.get(
      `/api/collections/variants/records?perPage=100&fields=id,on_hand,reserved&filter=${filter}`);
    shop.showStock(data.items);
  } catch {
    for (const badge of document.querySelectorAll("[data-stock]")) badge.textContent = "";
  }
};

{
  const product = document.querySelector("[data-product]");
  if (product && product.dataset.product) shop.loadStock(product.dataset.product);
}
