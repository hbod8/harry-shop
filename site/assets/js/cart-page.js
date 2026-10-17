// The cart page: re-reads every line's variant (and its product) from the
// API, drops lines that are gone or inactive, caps quantities at what's in
// stock, and shows how the cart will be paid (one channel per cart). Prices
// shown here are for display; checkout reprices on the server.
(async () => {
  const root = document.getElementById("cart");
  const { el, money } = shop;
  const notices = [];

  // Variants are fetched one by one by id: an unlisted product's variants
  // can't be listed, only viewed.
  const fetchLine = async (line) => {
    if (!/^[a-z0-9]{15}$/.test(line.variant)) return { line, variant: null };
    try {
      const variant = await shop.get(`/api/collections/variants/records/${line.variant}?expand=product`);
      return { line, variant };
    } catch {
      return { line, variant: null };
    }
  };

  const [items, channels] = await Promise.all([
    Promise.all(shop.cart.lines().map(fetchLine)),
    shop.get("/api/collections/channels/records?filter=(active=true)").then((d) => d.items).catch(() => []),
  ]);

  // drop lines whose variant or product no longer exists or is inactive
  const gone = items.filter((i) => !i.variant);
  if (gone.length) {
    gone.forEach((i) => shop.cart.remove(i.line.variant));
    notices.push(`${gone.length === 1 ? "An item is" : `${gone.length} items are`} no longer available and ${gone.length === 1 ? "was" : "were"} removed.`);
  }
  const lines = items.filter((i) => i.variant);

  // cap quantities at available stock
  for (const i of lines) {
    const available = i.variant.on_hand - i.variant.reserved;
    if (available > 0 && i.line.qty > available) {
      shop.cart.setQty(i.line.variant, available);
      notices.push(`Only ${available} of ${label(i)} ${available === 1 ? "is" : "are"} available, so your quantity was reduced.`);
    }
  }

  render();

  function label(i) {
    return `${i.variant.expand.product.title} — ${i.variant.name}`;
  }

  function productURL(product) {
    return product.listed ? `/products/${product.slug}/` : `/item/#${product.id}`;
  }

  function render() {
    const qtys = new Map(shop.cart.lines().map((l) => [l.variant, l.qty]));
    const current = lines.filter((i) => qtys.has(i.line.variant));

    const noticeNodes = notices.map((n) => el("p", { class: "notice", role: "status" }, n));
    if (!current.length) {
      root.replaceChildren(...noticeNodes,
        el("p", {}, "Your cart is empty. ", el("a", { href: "/products/" }, "Browse the shop"), "."));
      return;
    }

    let subtotal = 0;
    let soldOut = false;
    const rows = current.map((i) => {
      const v = i.variant;
      const p = v.expand.product;
      const qty = qtys.get(v.id);
      const available = v.on_hand - v.reserved;
      if (available <= 0) soldOut = true;
      else subtotal += qty * v.price_cents;

      const qtyInput = el("input", {
        type: "number", min: "1", max: String(Math.max(1, Math.min(available, shop.cart.maxQty))),
        value: String(qty), "aria-label": `Quantity of ${label(i)}`,
      });
      qtyInput.addEventListener("change", () => {
        const n = Math.max(1, Math.min(parseInt(qtyInput.value, 10) || 1, available, shop.cart.maxQty));
        shop.cart.setQty(v.id, n);
        notices.length = 0;
        render();
      });
      const removeButton = el("button", { type: "button", class: "secondary outline" }, "Remove");
      removeButton.addEventListener("click", () => {
        shop.cart.remove(v.id);
        notices.length = 0;
        render();
      });

      const image = p.images.length
        ? el("img", { src: `/api/files/${p.collectionId}/${p.id}/${encodeURIComponent(p.images[0])}?thumb=600x0`, alt: "" })
        : el("div", { class: "placeholder", "aria-hidden": "true" });

      return el("li", { class: "cart-line" },
        el("a", { href: productURL(p), class: "cart-image" }, image),
        el("div", { class: "cart-name" },
          el("a", { href: productURL(p) }, p.title),
          el("small", { class: "muted" }, ` ${v.name} · ${money(v.price_cents)}`),
          available <= 0 ? el("small", { class: "stock", "data-level": "out" }, " Sold out") : ""),
        el("div", { class: "cart-qty" }, available > 0 ? qtyInput : ""),
        el("div", { class: "cart-total" }, available > 0 ? money(qty * v.price_cents) : "—"),
        el("div", { class: "cart-remove" }, removeButton));
    });

    root.replaceChildren(...noticeNodes,
      el("ul", { class: "cart-lines" }, ...rows),
      el("p", { class: "cart-subtotal" }, "Subtotal ", el("strong", {}, money(subtotal))),
      el("p", { class: "muted" }, el("small", {}, "Shipping and tax are added at checkout.")),
      soldOut ? el("p", { class: "notice" }, "Remove sold-out items to check out.") : checkoutNode(current));
  }

  // Each product is paid through exactly one channel, so a cart can be
  // checked out only if all its products share one.
  function checkoutNode(current) {
    const byId = new Map(channels.map((c) => [c.id, c]));
    const ids = new Set(current.map((i) => i.variant.expand.product.channel));
    const channel = ids.size === 1 ? byId.get([...ids][0]) : null;
    if (channel) return checkoutForm(channel);

    const groups = new Map();
    for (const i of current) {
      const name = byId.get(i.variant.expand.product.channel)?.name ?? "Unavailable";
      groups.set(name, [...(groups.get(name) ?? []), i.variant.expand.product.title]);
    }
    return el("p", { class: "notice" },
      "These items are paid for differently, so please order them separately: ",
      [...groups].map(([name, titles]) => `${titles.join(", ")} (${name})`).join("; "), ".");
  }

  function checkoutForm(channel) {
    const remembered = (() => { try { return JSON.parse(localStorage.getItem("checkout") ?? "{}"); } catch { return {}; } })();
    const email = el("input", { type: "email", name: "email", required: "", autocomplete: "email", value: remembered.email ?? "" });
    const name = el("input", { type: "text", name: "name", required: "", autocomplete: "name", maxlength: "200", value: remembered.name ?? "" });
    const isCard = channel.provider === "stripe";
    const status = el("p", { role: "status" });
    const button = el("button", { type: "submit" }, isCard ? "Continue to payment" : "Place order");

    const form = el("form", { class: "checkout" },
      el("h2", {}, "Check out"),
      el("label", {}, "Email", email),
      el("label", {}, "Name", name),
      el("p", {}, "Payment: ", el("strong", {}, channel.name),
        isCard ? " (secure checkout by Stripe)" : ". Harry will send payment details; nothing is charged now."),
      button, status);

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      try { localStorage.setItem("checkout", JSON.stringify({ email: email.value, name: name.value })); } catch {}
      button.setAttribute("aria-busy", "true");
      button.disabled = true;
      status.textContent = "";
      try {
        const res = await fetch("/shop/checkout", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ lines: shop.cart.lines(), email: email.value, name: name.value }),
        });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body.message || "Something went wrong. Please try again.");
        if (body.url) {
          location.assign(body.url); // Stripe's hosted checkout
          return;
        }
        shop.cart.save([]);
        root.replaceChildren(
          el("h2", {}, "Order placed"),
          el("p", {}, `Thanks, ${name.value}. Your order number is `, el("strong", {}, `#${body.number}`), "."),
          el("p", {}, "Harry will be in touch with payment details. Nothing has been charged."),
          el("p", {}, el("a", { href: "/products/" }, "Back to the shop")));
      } catch (err) {
        status.textContent = err.message;
        status.className = "notice";
        button.removeAttribute("aria-busy");
        button.disabled = false;
      }
    });
    return form;
  }
})();
