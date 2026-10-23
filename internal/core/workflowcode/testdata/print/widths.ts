import { workflow } from "@polythought/workflow"

export default workflow("Widths", (w) => {
  const fits = w.node("fits", "output.log", {
    message: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  })
  const over = w.node("over", "output.log", {
    message: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  })
  const emoji = w.node("emoji", "output.log", {
    message: "🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀yyyyyy",
  })
  const emoji2 = w.node("emoji2", "output.log", {
    message: "🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀yyyyyyy",
  })
  const opts = w.node("opts", "logic.if", {
    field: "spread",
    operator: "lt",
    value: "0.02",
    extra: "a long value to push the line past its width",
  }, { name: "A name", notes: "Short notes", continueOnFail: true })
  const longopts = w.node("longopts", "logic.if", {
    field: "f",
  }, {
    name: "A name long enough that the options do not fit after the settings' closing brace at all",
  })
  const multiline = w.node("multiline", "output.log", {
    message: "short",
  }, {
    notes: `two
lines`,
  })
  const nosettings = w.node("nosettings", "output.log", {}, {
    name: "Only a name, long enough to break the line past the width of one hundred",
  })
})
