import { workflow } from "@polythought/workflow"

export default workflow("Spread watch", (w) => {
  const manual = w.trigger("manual", "trigger.manual")
  const tight = w.node("tight", "logic.if", {
    field: "spread",
    operator: "lt",
    value: "0.02",
  }, { name: "Tight spread" })
  const log = w.node("log", "output.log", {
    message: "Tight: {{ question }}, at {{ spread }}, worth a closer look",
  })
  const failed = w.node("failed", "output.log", {}, { continueOnFail: true })

  manual.to(tight)
  tight.port("true").to(log)
  tight.port("error").to(failed.input("b"))
})
