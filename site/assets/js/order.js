// Stripe sends buyers here with ?session_id=. The orders, order_lines and
// order_totals rules let exactly that session's order be read. The webhook
// confirms payment, so the page waits briefly if it hasn't arrived yet.
(async () => {
  const root = document.getElementById("order");
  const { el, money } = shop;
  const session = new URLSearchParams(location.search).get("session_id") ?? "";
  const q = `session_id=${encodeURIComponent(session)}`;

  if (!session) {
    root.replaceChildren(el("p", {}, "This link is missing its order details."));
    return;
  }

  const load = async () => {
    const [orders, lines, totals] = await Promise.all([
      shop.get(`/api/collections/orders/records?${q}`),
      shop.get(`/api/collections/order_lines/records?${q}&sort=created&perPage=100`),
      shop.get(`/api/collections/order_totals/records?${q}`),
    ]);
    return { order: orders.items[0], lines: lines.items, totals: totals.items[0] };
  };

  let data;
  for (let attempt = 0; attempt < 15; attempt++) {
    try {
      data = await load();
    } catch {
      data = undefined;
    }
    if (data?.order && data.order.status !== "pending") break;
    if (data?.order) root.replaceChildren(el("p", { "aria-busy": "true" }, "Confirming your payment…"));
    await new Promise((r) => setTimeout(r, 2000));
  }

  if (!data?.order) {
    root.replaceChildren(el("p", {}, "We couldn't find this order. If you were charged, email Harry with your receipt."));
    return;
  }
  const { order, lines, totals } = data;
  shop.cart.save([]); // the order exists, so the cart has done its job

  const heading = {
    pending: "We're still confirming your payment. This page will update when you reload it.",
    unconfirmed: "Order received. Harry will confirm the final price.",
    confirmed: "Thank you! Your payment went through and your order is confirmed.",
    fulfilled: "Your order has shipped.",
    cancelled: "This order was cancelled.",
  }[order.status] ?? "";

  const items = lines.filter((l) => l.kind === "item");
  const extras = lines.filter((l) => l.kind === "shipping" || l.kind === "tax");
  const row = (label, cents) => el("tr", {}, el("td", {}, label), el("td", { class: "num" }, money(cents)));

  const address = [order.ship_line1, order.ship_line2,
    [order.ship_city, order.ship_state, order.ship_postal].filter(Boolean).join(" ")].filter(Boolean);

  root.replaceChildren(
    el("p", { class: "lead" }, heading),
    el("p", { class: "muted" }, `Order #${order.number} · ${order.email}`),
    el("table", { class: "order-lines" }, el("tbody", {},
      ...items.map((l) => row(`${l.quantity} × ${l.name}`, l.quantity * l.unit_cents)),
      ...extras.map((l) => row(l.name, l.quantity * l.unit_cents)),
      el("tr", {}, el("th", {}, "Total"), el("th", { class: "num" }, money(totals?.total ?? 0))))),
    totals && totals.paid ? el("p", {}, `Paid ${money(totals.paid)}`, totals.balance > 0 ? ` · ${money(totals.balance)} still owed` : "") : "",
    address.length ? el("p", {}, el("strong", {}, "Shipping to"), el("br"), order.name, ...address.flatMap((a) => [el("br"), a])) : "",
    order.tracking_number ? el("p", {}, `Tracking number: ${order.tracking_number}`) : "",
    el("p", {}, el("a", { href: "/products/" }, "Back to the shop")));
})();
