import { workflow } from "@polythought/workflow"

export default workflow("Retried", (w) => {
  const manual = w.trigger("manual", "trigger.manual")
  const books = w.node("books", "polymarket.orderbook", {}, { retry: 0 })
  const ask = w.node("ask", "http.request", {
    method: "POST",
    url: "https://api.example.com/markets",
  }, { continueOnFail: true, retry: 2 })

  manual.to(books)
  books.to(ask)
})
