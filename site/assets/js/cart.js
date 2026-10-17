// The cart lives in localStorage as [{variant, qty}]. It is only a list of
// choices: the cart page re-reads every variant from the API, and checkout
// reprices everything on the server.
shop.cart = {
  key: "cart",
  maxLines: 20,
  maxQty: 99,

  lines() {
    try {
      const lines = JSON.parse(localStorage.getItem(this.key) ?? "[]");
      return Array.isArray(lines)
        ? lines.filter((l) => typeof l.variant === "string" && Number.isInteger(l.qty) && l.qty > 0)
        : [];
    } catch {
      return [];
    }
  },

  save(lines) {
    try {
      localStorage.setItem(this.key, JSON.stringify(lines.slice(0, this.maxLines)));
    } catch {
      // storage blocked (private mode): the cart just won't persist
    }
    this.updateBadge();
  },

  // add returns false when the cart is full.
  add(variant, qty) {
    const lines = this.lines();
    const line = lines.find((l) => l.variant === variant);
    if (line) {
      line.qty = Math.min(line.qty + qty, this.maxQty);
    } else {
      if (lines.length >= this.maxLines) return false;
      lines.push({ variant, qty: Math.min(qty, this.maxQty) });
    }
    this.save(lines);
    return true;
  },

  setQty(variant, qty) {
    this.save(this.lines()
      .map((l) => (l.variant === variant ? { ...l, qty: Math.min(qty, this.maxQty) } : l))
      .filter((l) => l.qty > 0));
  },

  remove(variant) {
    this.save(this.lines().filter((l) => l.variant !== variant));
  },

  count() {
    return this.lines().reduce((n, l) => n + l.qty, 0);
  },

  updateBadge() {
    const n = this.count();
    for (const badge of document.querySelectorAll("[data-cart-count]")) {
      badge.textContent = n ? `(${n})` : "";
    }
  },
};

// wireAddToCart connects an add-to-cart form: a variant <select>, a qty
// input and a status line. Sold-out options are disabled by showStock.
shop.wireAddToCart = (form) => {
  const status = form.querySelector("[data-cart-status]");
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const variant = form.elements.variant.value;
    const qty = Math.max(1, parseInt(form.elements.qty.value, 10) || 1);
    const option = form.elements.variant.selectedOptions[0];
    if (!variant || option?.disabled) {
      status.textContent = "That option is sold out.";
      return;
    }
    if (!shop.cart.add(variant, qty)) {
      status.textContent = `Your cart is full (${shop.cart.maxLines} lines).`;
      return;
    }
    status.replaceChildren(`Added ${qty} × ${option.dataset.name} to your cart. `,
      shop.el("a", { href: "/cart/" }, "View cart"));
  });
};

shop.cart.updateBadge();
for (const form of document.querySelectorAll("form[data-add-to-cart]")) shop.wireAddToCart(form);
