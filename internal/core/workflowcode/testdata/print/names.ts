import { workflow } from "@polythought/workflow"

export default workflow("w", (w) => {
  const orderBooks = w.node("order-books", "output.log")
  const orderBooks2 = w.node("orderBooks", "output.log")
  const new2 = w.node("new", "output.log")
  const w2 = w.node("w", "output.log")
  const node1st = w.node("1st", "output.log")
  const node = w.node("--", "output.log")
  const straßeSSe = w.node("straße-ße", "output.log")
  const überNode = w.node("über node", "output.log")
  const 日本語 = w.node("日本-語", "output.log")
  const aBC = w.node("a.b.c", "output.log")
  const $dollar = w.node("$dollar", "output.log")
  const _under = w.node("_under", "output.log")
  const x𝒜y = w.node("x-𝒜y", "output.log")
  const workflow2 = w.node("workflow", "output.log")
  const node3 = w.node("node", "output.log")
  const node3 = w.node("node", "output.log")

  orderBooks.to(new2)
  orderBooks2.to(w2)
})
