// Renders an unlisted product from /item/#<product id>. Unlisted products
// are hidden from API lists but readable by id, so the link is the secret.
(async () => {
  const root = document.getElementById("item");
  const id = location.hash.slice(1);
  const { el, money } = shop;

  const fail = (message) => root.replaceChildren(
    el("h1", {}, "Not found"),
    el("p", {}, message, " ", el("a", { href: "/products/" }, "Back to the shop"), "."));

  if (!/^[a-z0-9]{15}$/.test(id)) return fail("This link looks incomplete.");

  let p;
  try {
    p = await shop.get(`/api/collections/products/records/${id}?expand=variants_via_product,terms`);
  } catch {
    return fail("This item isn't available.");
  }

  document.title = `${p.title} · ${document.title.split(" · ").pop()}`;
  const variants = (p.expand?.variants_via_product ?? []).sort((a, b) => a.position - b.position);
  const prices = variants.map((v) => v.price_cents);
  const min = Math.min(...prices), max = Math.max(...prices);
  const image = (file, thumb) =>
    `/api/files/${p.collectionId}/${p.id}/${encodeURIComponent(file)}${thumb ? `?thumb=${thumb}` : ""}`;

  const gallery = el("div", { class: "gallery" },
    p.images.length
      ? el("img", { src: image(p.images[0], "1200x0"), alt: p.title })
      : el("div", { class: "placeholder large", "aria-hidden": "true" }, el("span", {}, p.title)));

  const rows = variants.map((v) => el("tr", {},
    el("th", { scope: "row" }, v.name),
    el("td", {}, money(v.price_cents)),
    el("td", {}, el("span", { class: "stock", "data-stock": v.id }, "Checking stock…"))));

  const chips = (p.expand?.terms ?? []).map((t) => el("span", {}, t.name));

  root.dataset.product = p.id;
  root.replaceChildren(gallery, el("div", { class: "details" },
    el("p", { class: "muted" }, p.product_type[0].toUpperCase() + p.product_type.slice(1)),
    el("h1", {}, p.title),
    el("p", { class: "price large" }, prices.length ? (min === max ? money(min) : `from ${money(min)}`) : ""),
    rows.length ? el("table", { class: "variants" }, el("tbody", {}, ...rows)) : "",
    variants.length ? addToCartForm(p, variants) : "",
    el("div", { class: "prose" }, ...paragraphs(p.description)),
    chips.length ? el("p", { class: "chips" }, ...chips) : ""));

  shop.showStock(variants);
  const form = root.querySelector("form[data-add-to-cart]");
  if (form) shop.wireAddToCart(form);
})();

function addToCartForm(p, variants) {
  const { el, money } = shop;
  const options = variants.map((v) =>
    el("option", { value: v.id, "data-name": `${p.title} — ${v.name}` }, `${v.name} · ${money(v.price_cents)}`));
  return el("form", { class: "add-to-cart", "data-add-to-cart": "" },
    el("div", { class: "add-row" },
      el("select", { name: "variant", "aria-label": "Option" }, ...options),
      el("input", { type: "number", name: "qty", value: "1", min: "1", max: "99", "aria-label": "Quantity" }),
      el("button", { type: "submit" }, "Add to cart")),
    el("p", { class: "muted", "data-cart-status": "", role: "status" }));
}

// The description is plain text: blank lines split paragraphs, single
// newlines become line breaks. Built with text nodes, so never markup.
function paragraphs(text) {
  return (text ?? "").replace(/\r\n/g, "\n").split(/\n\s*\n/)
    .map((block) => block.trim())
    .filter(Boolean)
    .map((block) => {
      const p = document.createElement("p");
      block.split("\n").forEach((line, i) => {
        if (i) p.append(document.createElement("br"));
        p.append(document.createTextNode(line));
      });
      return p;
    });
}
