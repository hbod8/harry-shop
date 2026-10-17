// Small helpers shared by the site's scripts.
// API data is always written with textContent or el(), never innerHTML.
window.shop = {
  async get(path) {
    const res = await fetch(path, { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error(`${res.status} ${path}`);
    return res.json();
  },

  // integer cents to "$35.00"
  money(cents) {
    const c = Math.round(cents);
    return `$${Math.floor(c / 100)}.${String(c % 100).padStart(2, "0")}`;
  },

  stockLabel(available) {
    if (available <= 0) return "Sold out";
    if (available <= 3) return `${available} left`;
    return "In stock";
  },

  // el("a", { href: "/x", class: "chip" }, "text", childNode)
  el(tag, attrs = {}, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
    for (const c of children) node.append(c);
    return node;
  },
};
