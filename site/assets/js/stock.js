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
  // sold-out options can't be added to the cart
  for (const select of document.querySelectorAll('select[name="variant"]')) {
    for (const option of select.options) {
      const available = byId.get(option.value) ?? 0;
      option.disabled = available <= 0;
      if (available <= 0 && !option.textContent.endsWith("(sold out)")) option.textContent += " (sold out)";
    }
    const firstAvailable = [...select.options].find((o) => !o.disabled);
    if (select.selectedOptions[0]?.disabled && firstAvailable) select.value = firstAvailable.value;
    const button = select.form?.querySelector('button[type="submit"]');
    if (button) button.disabled = !firstAvailable;
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
